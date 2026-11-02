package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	knownEditors   = []string{"Visual Studio Code", "Cursor", "Zed", "PhpStorm", "Sublime Text", "Nova", "BBEdit", "TextEdit"}
	knownTerminals = []string{"iTerm", "Warp", "Ghostty", "WezTerm", "Terminal"}
)

func appInstalled(name string) bool {
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", "/System/Applications", "/System/Applications/Utilities", filepath.Join(home, "Applications")} {
		if exists(filepath.Join(dir, name+".app")) {
			return true
		}
	}
	return false
}

// InstalledApps lists the editors and terminals found on this Mac.
func InstalledApps() (editors, terminals []string) {
	editors, terminals = []string{}, []string{}
	for _, a := range knownEditors {
		if appInstalled(a) {
			editors = append(editors, a)
		}
	}
	for _, a := range knownTerminals {
		if appInstalled(a) {
			terminals = append(terminals, a)
		}
	}
	return editors, terminals
}

func (m *Manager) editorApp() string {
	if e := m.Config().Editor; e != "" {
		return e
	}
	if eds, _ := InstalledApps(); len(eds) > 0 {
		return eds[0]
	}
	return "TextEdit"
}

func (m *Manager) terminalApp() string {
	if t := m.Config().Terminal; t != "" {
		return t
	}
	if _, terms := InstalledApps(); len(terms) > 0 {
		return terms[0]
	}
	return "Terminal"
}

// Open opens a path with a macOS app. What is "finder", "editor" or "terminal".
func (m *Manager) Open(what, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	if !exists(path) {
		return fmt.Errorf("%s does not exist", path)
	}
	var args []string
	switch what {
	case "finder":
		if st, _ := os.Stat(path); st.IsDir() {
			args = []string{path}
		} else {
			args = []string{"-R", path}
		}
	case "editor":
		args = []string{"-a", m.editorApp(), path}
	case "terminal":
		dir := path
		if st, _ := os.Stat(path); !st.IsDir() {
			dir = filepath.Dir(path)
		}
		args = []string{"-a", m.terminalApp(), dir}
	default:
		return fmt.Errorf("unknown target %q", what)
	}
	if out, err := exec.Command("/usr/bin/open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("open: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ConfigFile is a file that the user can open from a service card.
type ConfigFile struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Note  string `json:"note,omitempty"`
}

var (
	iniMu    sync.Mutex
	iniCache = map[string]string{}
)

// phpIniPath asks the PHP binary which php.ini it loads. PHP-FPM from
// the same Homebrew formula loads the same file.
func phpIniPath(v PHPVersion) string {
	iniMu.Lock()
	defer iniMu.Unlock()
	if p, ok := iniCache[v.FPM]; ok {
		return p
	}
	bin := filepath.Join(filepath.Dir(filepath.Dir(v.FPM)), "bin", "php")
	out, _ := run(bin, "-r", "echo php_ini_loaded_file();")
	iniCache[v.FPM] = strings.TrimSpace(out)
	return iniCache[v.FPM]
}

func (m *Manager) serviceFiles(id string) []ConfigFile {
	switch {
	case id == "caddy":
		return []ConfigFile{{Label: "Caddyfile", Path: m.caddyfilePath(), Note: "Benchtop makes this file. Use the Extra field of a site for changes."}}
	case strings.HasPrefix(id, "php-"):
		key := strings.TrimPrefix(id, "php-")
		v, ok := findPHP(key)
		if !ok {
			return nil
		}
		var files []ConfigFile
		if ini := phpIniPath(v); ini != "" {
			files = append(files, ConfigFile{Label: "php.ini", Path: ini, Note: "Restart PHP " + key + " after you save."})
		}
		if err := m.ensurePHPConfig(key); err == nil {
			files = append(files, ConfigFile{Label: "php-fpm.conf", Path: m.PHPConfigPath(key), Note: "Restart PHP " + key + " after you save."})
		}
		return files
	case id == "ftp":
		return []ConfigFile{{Label: "pure-ftpd.conf", Path: m.ftpConf(), Note: "Benchtop makes this file on each FTP start."}}
	}
	if s, ok := findExtra(id); ok && s.Installed() {
		return s.files(m)
	}
	return nil
}
