package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PHPExtension is one PECL extension for one PHP version.
type PHPExtension struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"` // the .so file is in the extension folder
	Enabled   bool   `json:"enabled"`   // Benchtop's conf.d file loads it
	Loaded    bool   `json:"loaded"`    // php -m lists it
	Note      string `json:"note,omitempty"`
}

type PHPExtensions struct {
	Version    string         `json:"version"`
	Loaded     []string       `json:"loaded"`
	Extensions []PHPExtension `json:"extensions"`
	ConfDir    string         `json:"confDir"`
	Installing string         `json:"installing,omitempty"`
}

// knownExtensions are the PECL extensions that the dashboard offers.
var knownExtensions = []struct{ name, note string }{
	{"xdebug", "Step debugging and coverage"},
	{"redis", "Redis client (phpredis)"},
	{"apcu", "In-memory cache"},
	{"pcov", "Fast code coverage"},
	{"imagick", "Needs: brew install imagemagick"},
	{"mongodb", "MongoDB driver"},
}

var zendExtensions = map[string]bool{"xdebug": true, "opcache": true}

var extNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,30}$`)

func phpBin(v PHPVersion, name string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(v.FPM)), "bin", name)
}

func phpQuery(v PHPVersion, code string) string {
	out, _ := run(phpBin(v, "php"), "-r", code)
	return strings.TrimSpace(out)
}

func phpConfDir(v PHPVersion) string {
	// PHP_CONFIG_FILE_SCAN_DIR is the conf.d folder of this build.
	return phpQuery(v, "echo PHP_CONFIG_FILE_SCAN_DIR;")
}

func extIniPath(confDir, name string) string {
	return filepath.Join(confDir, "benchtop-"+name+".ini")
}

// ListPHPExtensions shows the known PECL extensions and their state.
func (m *Manager) ListPHPExtensions(key string) (PHPExtensions, error) {
	v, ok := findPHP(key)
	if !ok {
		return PHPExtensions{}, fmt.Errorf("PHP %s is not installed", key)
	}
	confDir := phpConfDir(v)
	out := PHPExtensions{Version: key, ConfDir: confDir, Installing: m.peclInstalling(key)}
	mods, _ := run(phpBin(v, "php"), "-m")
	loaded := map[string]bool{}
	for _, line := range strings.Split(mods, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if !loaded[strings.ToLower(line)] {
			out.Loaded = append(out.Loaded, line)
		}
		loaded[strings.ToLower(line)] = true
	}
	sort.Strings(out.Loaded)
	extDir := phpQuery(v, `echo ini_get("extension_dir");`)
	seen := map[string]bool{}
	add := func(name, note string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out.Extensions = append(out.Extensions, PHPExtension{
			Name: name, Note: note,
			Installed: exists(filepath.Join(extDir, name+".so")),
			Enabled:   exists(extIniPath(confDir, name)),
			Loaded:    loaded[name] || (name == "xdebug" && loaded["xdebug"]),
		})
	}
	for _, k := range knownExtensions {
		add(k.name, k.note)
	}
	// Other .so files that PECL put in the extension folder.
	files, _ := filepath.Glob(filepath.Join(extDir, "*.so"))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".so")
		if name != "opcache" {
			add(name, "")
		}
	}
	return out, nil
}

// SetPHPExtension turns an installed extension on or off with a file in
// conf.d, then restarts PHP-FPM if it runs. It does not change php.ini.
func (m *Manager) SetPHPExtension(key, name string, on bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	v, ok := findPHP(key)
	if !ok {
		return fmt.Errorf("PHP %s is not installed", key)
	}
	if !extNameRe.MatchString(name) {
		return fmt.Errorf("extension name %q is not valid", name)
	}
	ini := extIniPath(phpConfDir(v), name)
	if on {
		extDir := phpQuery(v, `echo ini_get("extension_dir");`)
		if !exists(filepath.Join(extDir, name+".so")) {
			return fmt.Errorf("%s is not installed for PHP %s", name, key)
		}
		if err := os.WriteFile(ini, []byte(extensionIni(name)), 0o644); err != nil {
			return err
		}
		// php -m fails to start when the extension cannot load.
		if out, err := run(phpBin(v, "php"), "-m"); err != nil || strings.Contains(out, "Warning") {
			os.Remove(ini)
			return fmt.Errorf("PHP %s cannot load %s: %s", key, name, lastLines(out, 4))
		}
	} else if err := os.Remove(ini); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, running := m.phpRunning(key); running {
		if err := m.stopPHP(key); err != nil {
			return err
		}
		return m.startPHP(key)
	}
	return nil
}

func extensionIni(name string) string {
	head := "; Created by Benchtop. Turn it off in the dashboard, or delete this file.\n"
	if !zendExtensions[name] {
		return head + fmt.Sprintf("extension=%s.so\n", name)
	}
	ini := head + fmt.Sprintf("zend_extension=%s.so\n", name)
	if name == "xdebug" {
		// Debug only when the request asks for it (browser extension or
		// XDEBUG_TRIGGER), so normal requests stay fast.
		ini += "xdebug.mode=debug,develop\nxdebug.start_with_request=trigger\nxdebug.client_host=127.0.0.1\nxdebug.client_port=9003\n"
	}
	return ini
}

var (
	peclMu     sync.Mutex
	peclActive = map[string]string{} // PHP version → extension
)

func (m *Manager) peclInstalling(key string) string {
	peclMu.Lock()
	defer peclMu.Unlock()
	return peclActive[key]
}

// peclPackage picks a release that supports the PHP version.
func peclPackage(name, key string) string {
	if name == "xdebug" && versionLess(key, "8.0") {
		return "xdebug-3.1.6" // the last release for PHP 7.x
	}
	if name == "pcov" && versionLess(key, "7.1") {
		return "pcov-1.0.6"
	}
	return name
}

// InstallPHPExtension builds an extension with PECL in the background. The
// output goes to the "pecl" log. When it ends well, Benchtop turns it on.
func (m *Manager) InstallPHPExtension(key, name string) error {
	v, ok := findPHP(key)
	if !ok {
		return fmt.Errorf("PHP %s is not installed", key)
	}
	if !extNameRe.MatchString(name) {
		return fmt.Errorf("extension name %q is not valid", name)
	}
	peclMu.Lock()
	if peclActive[key] != "" {
		peclMu.Unlock()
		return fmt.Errorf("PECL already installs %s for PHP %s", peclActive[key], key)
	}
	peclActive[key] = name
	peclMu.Unlock()

	go func() {
		defer func() {
			peclMu.Lock()
			delete(peclActive, key)
			peclMu.Unlock()
		}()
		f, err := os.OpenFile(m.path("logs", "pecl.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		pkg := peclPackage(name, key)
		fmt.Fprintf(f, "\n--- %s: pecl install %s (PHP %s)\n", time.Now().Format(time.RFC3339), pkg, key)
		// php_ini="" stops PECL from adding a line to php.ini. Benchtop
		// loads the extension from its own conf.d file instead.
		cmd := exec.Command(phpBin(v, "pecl"), "-d", "php_ini=", "install", pkg)
		cmd.Env = append(os.Environ(), "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
		cmd.Stdin = strings.NewReader("\n\n\n\n\n\n") // accept the default answers
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f, "--- pecl install %s failed: %v\n", pkg, err)
			return
		}
		if err := m.SetPHPExtension(key, name, true); err != nil {
			fmt.Fprintf(f, "--- installed, but could not turn it on: %v\n", err)
			return
		}
		fmt.Fprintf(f, "--- %s is installed and on for PHP %s\n", name, key)
	}()
	return nil
}
