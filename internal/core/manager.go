package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Manager owns the config and controls all services.
type Manager struct {
	Home string

	cfgMu sync.Mutex
	cfg   *Config

	// opMu makes start, stop and config changes run one at a time.
	opMu sync.Mutex
}

func DefaultHome() string {
	if h := os.Getenv("BENCHTOP_HOME"); h != "" {
		return h
	}
	return defaultHomePath()
}

// defaultHomePath is ~/.benchtop, whatever BENCHTOP_HOME says.
func defaultHomePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".benchtop")
}

func New(home string) (*Manager, error) {
	if os.Getenv("BENCHTOP_HOME") == "" {
		if err := migrateLegacyHome(home); err != nil {
			return nil, fmt.Errorf("move data from ~/.devcenter: %w", err)
		}
	}
	m := &Manager{Home: home}
	for _, d := range []string{"", "caddy", "caddy/data", "php", "ftp", "logs", "logs/sites", "tmp"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			return nil, err
		}
	}
	cfg, err := loadConfig(m.configPath())
	if err != nil {
		return nil, err
	}
	m.cfg = cfg
	return m, nil
}

func (m *Manager) configPath() string { return filepath.Join(m.Home, "config.json") }
func (m *Manager) path(p ...string) string {
	return filepath.Join(append([]string{m.Home}, p...)...)
}

func copyServices(in map[string]ServiceSettings) map[string]ServiceSettings {
	out := make(map[string]ServiceSettings, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Config returns a copy of the current config.
func (m *Manager) Config() Config {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	c := *m.cfg
	c.Sites = append([]Site(nil), m.cfg.Sites...)
	c.Services = copyServices(m.cfg.Services)
	return c
}

func (m *Manager) updateConfig(fn func(c *Config) error) error {
	m.cfgMu.Lock()
	defer m.cfgMu.Unlock()
	next := *m.cfg
	next.Sites = append([]Site(nil), m.cfg.Sites...)
	next.Services = copyServices(m.cfg.Services)
	if err := fn(&next); err != nil {
		return err
	}
	if err := saveConfig(m.configPath(), &next); err != nil {
		return err
	}
	m.cfg = &next
	return nil
}

type ServiceStatus struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Kind      string       `json:"kind"`
	Version   string       `json:"version"`
	Installed bool         `json:"installed"`
	Running   bool         `json:"running"`
	PID       int          `json:"pid"`
	Detail    string       `json:"detail"`
	UsedBy    []string     `json:"usedBy,omitempty"`
	Admin     bool         `json:"admin"`
	Files     []ConfigFile `json:"files,omitempty"`
	// These fields are for extra services (databases and mail).
	Enabled      bool   `json:"enabled,omitempty"`
	Formula      string `json:"formula,omitempty"`
	Installing   bool   `json:"installing,omitempty"`
	DataSource   string `json:"dataSource,omitempty"`
	Links        []Link `json:"links,omitempty"`
	FlyEnvData   string `json:"flyenvData,omitempty"`
	HasShell     bool   `json:"hasShell,omitempty"`
	HasDatabases bool   `json:"hasDatabases,omitempty"`
}

func (m *Manager) Status() []ServiceStatus {
	cfg := m.Config()
	var out []ServiceStatus

	pid, running := m.caddyRunning()
	caddy := ServiceStatus{ID: "caddy", Name: "Caddy", Kind: "web", Installed: m.caddyBin() != "", Running: running, PID: pid}
	caddy.Version = m.caddyVersion()
	caddy.Detail = fmt.Sprintf("HTTP :%d, HTTPS :%d, %d sites", cfg.HTTPPort, cfg.HTTPSPort, len(cfg.Sites))
	out = append(out, caddy)

	used := map[string][]string{}
	for _, s := range cfg.Sites {
		if s.PHP != "" {
			used[s.PHP] = append(used[s.PHP], s.Domains[0])
		}
	}
	versions := DiscoverPHP()
	known := map[string]bool{}
	for _, v := range versions {
		known[v.Key] = true
		pid, running := m.phpRunning(v.Key)
		out = append(out, ServiceStatus{
			ID: "php-" + v.Key, Name: "PHP-FPM " + v.Key, Kind: "php", Version: v.Full,
			Installed: true, Running: running, PID: pid, Detail: m.phpSocket(v.Key), UsedBy: used[v.Key],
		})
	}
	var missing []string
	for k := range used {
		if !known[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	for _, k := range missing {
		out = append(out, ServiceStatus{ID: "php-" + k, Name: "PHP-FPM " + k, Kind: "php", Detail: "Run: brew install php@" + k, UsedBy: used[k]})
	}

	pid, running = m.ftpRunning()
	users, _ := m.FTPUsers()
	out = append(out, ServiceStatus{
		ID: "ftp", Name: "Pure-FTPd", Kind: "ftp", Installed: m.pureFTPdBin() != "", Running: running, PID: pid, Admin: true,
		Detail: fmt.Sprintf("port %d, %d users", cfg.FTP.Port, len(users)),
	})
	out = append(out, m.extraStatus()...)
	for i := range out {
		for _, f := range m.serviceFiles(out[i].ID) {
			if exists(f.Path) {
				out[i].Files = append(out[i].Files, f)
			}
		}
	}
	return out
}

// Start starts one service: "caddy", "php-8.2", "php" (all needed), or "ftp".
func (m *Manager) Start(id string, force bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.start(id, force)
}

func (m *Manager) start(id string, force bool) error {
	switch {
	case id == "caddy":
		return m.startCaddy(force)
	case id == "php":
		return m.startNeededPHP()
	case strings.HasPrefix(id, "php-"):
		return m.startPHP(strings.TrimPrefix(id, "php-"))
	case id == "ftp":
		return m.startFTP(force)
	}
	if s, ok := findExtra(id); ok {
		return m.startExtra(s, force)
	}
	return fmt.Errorf("unknown service %q", id)
}

func (m *Manager) Stop(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.stop(id)
}

func (m *Manager) stop(id string) error {
	switch {
	case id == "caddy":
		return m.stopCaddy()
	case id == "php":
		var errs []error
		for _, v := range DiscoverPHP() {
			errs = append(errs, m.stopPHP(v.Key))
		}
		return errors.Join(errs...)
	case strings.HasPrefix(id, "php-"):
		return m.stopPHP(strings.TrimPrefix(id, "php-"))
	case id == "ftp":
		return m.stopFTP()
	}
	if s, ok := findExtra(id); ok {
		return m.stopExtra(s, true)
	}
	return fmt.Errorf("unknown service %q", id)
}

func (m *Manager) Restart(id string, force bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := m.stop(id); err != nil {
		return err
	}
	return m.start(id, force)
}

// StartAll starts PHP for each site, then Caddy, then FTP. It asks for the
// admin password at most once, for the hosts file and FTP together.
func (m *Manager) StartAll() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	var errs []error
	if err := m.startNeededPHP(); err != nil {
		errs = append(errs, err)
	}
	if err := m.startCaddy(false); err != nil {
		errs = append(errs, fmt.Errorf("caddy: %w", err))
	}
	errs = append(errs, m.startEnabledExtras())
	if err := m.applyNginx(); err != nil {
		errs = append(errs, fmt.Errorf("nginx: %w", err))
	}

	var adminCmds []string
	hostsCmd, err := m.hostsSyncCmd()
	if err != nil {
		errs = append(errs, fmt.Errorf("hosts: %w", err))
	} else if hostsCmd != "" {
		adminCmds = append(adminCmds, hostsCmd)
	}
	cfg := m.Config()
	if cfg.FTP.Enabled {
		if _, running := m.ftpRunning(); !running {
			cmd, err := m.ftpStartCmd(false)
			if err != nil {
				errs = append(errs, fmt.Errorf("ftp: %w", err))
			} else {
				adminCmds = append(adminCmds, cmd)
			}
		}
	}
	if err := runAdmin("Benchtop needs your password to update /etc/hosts and start the FTP server.", adminCmds...); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (m *Manager) StopAll() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	var errs []error
	errs = append(errs, m.stopCaddy())
	for _, v := range DiscoverPHP() {
		errs = append(errs, m.stopPHP(v.Key))
	}
	for _, s := range extraServices() {
		errs = append(errs, m.stopExtra(s, false))
	}
	if _, running := m.ftpRunning(); running {
		errs = append(errs, m.stopFTP())
	}
	return errors.Join(errs...)
}

// LogFiles lists log files the user can view, by short name.
func (m *Manager) LogFiles() map[string]string {
	files := map[string]string{
		"caddy":     m.path("logs", "caddy.log"),
		"caddy-run": m.path("logs", "caddy-run.log"),
		"ftp":       m.path("logs", "ftp-transfer.log"),
		"login":     m.path("logs", "login.log"),
		"dashboard": m.path("logs", "ui.log"),
		"brew":      m.path("logs", "brew.log"),
		"pecl":      m.path("logs", "pecl.log"),
	}
	for _, s := range extraServices() {
		if s.Installed() {
			files[s.ID()] = m.extraLog(s.ID())
		}
	}
	for _, v := range DiscoverPHP() {
		files["php-"+v.Key] = filepath.Join(m.phpDir(v.Key), "log", "php-fpm.log")
	}
	for _, s := range m.Config().Sites {
		files["site-"+s.ID] = m.path("logs", "sites", s.ID+".log")
	}
	return files
}

// TailLog returns the last n lines of a log file.
func (m *Manager) TailLog(name string, n int) (string, error) {
	p, ok := m.LogFiles()[name]
	if !ok {
		return "", fmt.Errorf("unknown log %q", name)
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	const max = 256 * 1024
	st, _ := f.Stat()
	off := int64(0)
	if st.Size() > max {
		off = st.Size() - max
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return "", err
	}
	return lastLines(strings.TrimRight(string(buf), "\n"), n), nil
}

// migrateLegacyHome moves ~/.devcenter (the name before v0.2) to the new
// home. A symlink stays at the old path, so services that were started
// before the move keep working until they restart.
func migrateLegacyHome(home string) error {
	userHome, _ := os.UserHomeDir()
	old := filepath.Join(userHome, ".devcenter")
	st, err := os.Lstat(old)
	if err != nil || !st.IsDir() {
		return nil
	}
	if _, err := os.Stat(home); err == nil {
		return nil
	}
	if err := os.Rename(old, home); err != nil {
		return err
	}
	if err := os.Symlink(home, old); err != nil {
		return err
	}
	confs, _ := filepath.Glob(filepath.Join(home, "php", "*", "php-fpm.conf"))
	for _, c := range confs {
		data, err := os.ReadFile(c)
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(data), old, home)
		text = strings.ReplaceAll(text, "/tmp/devcenter-php", "/tmp/benchtop-php")
		text = strings.ReplaceAll(text, "Created by DevCenter", "Created by Benchtop")
		if err := os.WriteFile(c, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// startEnabledExtras starts the extra services that the user started last time.
func (m *Manager) startEnabledExtras() error {
	var errs []error
	for _, s := range extraServices() {
		if m.serviceSettings(s.ID()).Enabled && s.Installed() {
			errs = append(errs, m.startExtra(s, false))
		}
	}
	return errors.Join(errs...)
}
