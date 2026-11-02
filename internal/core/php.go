package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type PHPVersion struct {
	Key  string `json:"key"`  // "8.2"
	Full string `json:"full"` // "8.2.35"
	FPM  string `json:"fpm"`
}

var phpVerRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// DiscoverPHP finds PHP versions installed with Homebrew, including the
// shivammathur/php tap, which uses the same opt layout.
func DiscoverPHP() []PHPVersion {
	byKey := map[string]PHPVersion{}
	for _, prefix := range []string{"/opt/homebrew/opt", "/usr/local/opt"} {
		matches, _ := filepath.Glob(filepath.Join(prefix, "php*"))
		for _, opt := range matches {
			base := filepath.Base(opt)
			if base != "php" && !strings.HasPrefix(base, "php@") {
				continue
			}
			fpm := filepath.Join(opt, "sbin", "php-fpm")
			if _, err := os.Stat(fpm); err != nil {
				continue
			}
			target, err := filepath.EvalSymlinks(opt)
			if err != nil {
				continue
			}
			mm := phpVerRe.FindStringSubmatch(filepath.Base(target))
			if mm == nil {
				continue
			}
			key := mm[1] + "." + mm[2]
			if _, ok := byKey[key]; ok {
				continue
			}
			byKey[key] = PHPVersion{Key: key, Full: mm[0], FPM: fpm}
		}
	}
	out := make([]PHPVersion, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i].Key, out[j].Key) })
	return out
}

func versionLess(a, b string) bool {
	var a1, a2, b1, b2 int
	fmt.Sscanf(a, "%d.%d", &a1, &a2)
	fmt.Sscanf(b, "%d.%d", &b1, &b2)
	if a1 != b1 {
		return a1 < b1
	}
	return a2 < b2
}

func findPHP(key string) (PHPVersion, bool) {
	for _, v := range DiscoverPHP() {
		if v.Key == key {
			return v, true
		}
	}
	return PHPVersion{}, false
}

func phpSocket(key string) string {
	return "/tmp/benchtop-php" + strings.ReplaceAll(key, ".", "") + ".sock"
}

func (m *Manager) phpDir(key string) string { return m.path("php", key) }

func (m *Manager) phpPIDFile(key string) string {
	return filepath.Join(m.phpDir(key), "run", "php-fpm.pid")
}

// PHPConfigPath is the php-fpm.conf for a version. The user can edit it.
func (m *Manager) PHPConfigPath(key string) string {
	return filepath.Join(m.phpDir(key), "php-fpm.conf")
}

func (m *Manager) phpRunning(key string) (int, bool) {
	pid := readPID(m.phpPIDFile(key))
	return pid, pidAlive(pid)
}

// ensurePHPConfig writes a default php-fpm.conf only when none exists,
// so the user's own changes stay.
func (m *Manager) ensurePHPConfig(key string) error {
	dir := m.phpDir(key)
	for _, d := range []string{"run", "log"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	conf := m.PHPConfigPath(key)
	if _, err := os.Stat(conf); err == nil {
		return nil
	}
	content := fmt.Sprintf(`; Created by Benchtop. You can edit this file. Restart PHP %[1]s to apply.
[global]
pid = %[2]s/run/php-fpm.pid
error_log = %[2]s/log/php-fpm.log
log_level = notice
daemonize = yes

[www]
listen = %[3]s
listen.mode = 0660
pm = dynamic
pm.max_children = 20
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 6
catch_workers_output = yes
decorate_workers_output = no
request_slowlog_timeout = 30
slowlog = %[2]s/log/php-fpm-slow.log
`, key, dir, phpSocket(key))
	return os.WriteFile(conf, []byte(content), 0o644)
}

func (m *Manager) startPHP(key string) error {
	v, ok := findPHP(key)
	if !ok {
		return fmt.Errorf("PHP %s is not installed. Run: brew install php@%s", key, key)
	}
	if _, running := m.phpRunning(key); running {
		return nil
	}
	if err := m.ensurePHPConfig(key); err != nil {
		return err
	}
	os.Remove(phpSocket(key))
	logPath := filepath.Join(m.phpDir(key), "log", "start.log")
	if err := runDetached(logPath, v.FPM, "-y", m.PHPConfigPath(key), "-p", m.phpDir(key), "-D"); err != nil {
		return fmt.Errorf("PHP %s: %w", key, err)
	}
	for i := 0; i < 30; i++ {
		if _, running := m.phpRunning(key); running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("PHP %s did not start. See %s", key, filepath.Join(m.phpDir(key), "log", "php-fpm.log"))
}

func (m *Manager) stopPHP(key string) error {
	pid, running := m.phpRunning(key)
	if !running {
		return nil
	}
	// SIGQUIT lets workers finish their current request.
	if err := syscall.Kill(pid, syscall.SIGQUIT); err != nil {
		return fmt.Errorf("stop PHP %s: %w", key, err)
	}
	if !waitDead(pid, 5*time.Second) {
		syscall.Kill(pid, syscall.SIGKILL)
		waitDead(pid, 2*time.Second)
	}
	os.Remove(m.phpPIDFile(key))
	return nil
}

// startNeededPHP starts each PHP version that a site uses.
func (m *Manager) startNeededPHP() error {
	needed := map[string]bool{}
	for _, s := range m.Config().Sites {
		if s.PHP != "" {
			needed[s.PHP] = true
		}
	}
	var errs []string
	keys := make([]string, 0, len(needed))
	for k := range needed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := m.startPHP(k); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}
