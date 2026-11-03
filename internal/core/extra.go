package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ServiceSettings are the saved choices for one extra service.
type ServiceSettings struct {
	// Enabled is true after the user starts the service, and false after
	// the user stops it. "Start all" and start at login use it.
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port,omitempty"`
	DataDir string `json:"dataDir,omitempty"`
	// Source is "flyenv" when DataDir is a FlyEnv data folder.
	Source string `json:"source,omitempty"`
	// Domains are the domain endings for the wildcard DNS, for example "test".
	Domains []string `json:"domains,omitempty"`
}

// Link is a URL that a service card can open, for example the Mailpit inbox.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// extraService is a service that Benchtop starts as a child process:
// databases, caches and mail tools. Caddy, PHP-FPM and FTP have their own code.
type extraService interface {
	ID() string
	Name() string
	Kind() string // "database" or "mail"
	Formula() string
	Installed() bool
	Version() string
	DefaultPort() int
	// prepare writes config files and makes the data folder. It runs before each start.
	prepare(m *Manager) error
	command(m *Manager) (bin string, args []string)
	// afterStart runs once the process is up, for example to set a password.
	afterStart(m *Manager) error
	ready(m *Manager) bool
	detail(m *Manager) string
	files(m *Manager) []ConfigFile
	links(m *Manager) []Link
	env(m *Manager) string
}

// extraServices lists the extra services, installed or not.
func extraServices() []extraService {
	var out []extraService
	out = append(out, mysqlServices()...)
	out = append(out, postgresServices()...)
	out = append(out, simpleServices()...)
	out = append(out, &dnsService{}, &nginxService{})
	return out
}

func findExtra(id string) (extraService, bool) {
	for _, s := range extraServices() {
		if s.ID() == id {
			return s, true
		}
	}
	return nil, false
}

func brewPrefix(formula string) string {
	for _, p := range []string{"/opt/homebrew/opt", "/usr/local/opt"} {
		dir := filepath.Join(p, formula)
		if exists(dir) {
			return dir
		}
	}
	return ""
}

// brewVersion reads a version such as "8.4.4" from the Cellar folder name.
func brewVersion(formula string) string {
	prefix := brewPrefix(formula)
	if prefix == "" {
		return ""
	}
	target, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return ""
	}
	v := filepath.Base(target)
	if i := strings.Index(v, "_"); i > 0 {
		v = v[:i]
	}
	return v
}

func (m *Manager) serviceSettings(id string) ServiceSettings {
	return m.Config().Services[id]
}

func (m *Manager) setServiceSettings(id string, fn func(s *ServiceSettings)) error {
	return m.updateConfig(func(c *Config) error {
		s := c.Services[id]
		fn(&s)
		c.Services[id] = s
		return nil
	})
}

// servicePort returns the saved port. The first time, it saves the first
// free port from the default, so a port that another program holds (for
// example 3306 for a Docker MySQL) does not block the start.
func (m *Manager) servicePort(s extraService) (int, error) {
	if p := m.serviceSettings(s.ID()).Port; p > 0 {
		return p, nil
	}
	port := s.DefaultPort()
	for portInUse(port) && port < s.DefaultPort()+20 {
		port++
	}
	return port, m.setServiceSettings(s.ID(), func(st *ServiceSettings) { st.Port = port })
}

func (m *Manager) servicePortOrDefault(s extraService) int {
	if p := m.serviceSettings(s.ID()).Port; p > 0 {
		return p
	}
	return s.DefaultPort()
}

func (m *Manager) extraDir(s extraService) string { return m.path("services", s.ID()) }
func (m *Manager) extraPIDFile(id string) string  { return m.path("run", id+".pid") }
func (m *Manager) extraLog(id string) string      { return m.path("logs", id+".log") }

func (m *Manager) extraRunning(id string) (int, bool) {
	pid := readPID(m.extraPIDFile(id))
	return pid, pidAlive(pid)
}

func (m *Manager) startExtra(s extraService, force bool) error {
	if !s.Installed() {
		return fmt.Errorf("%s is not installed. Install it from the dashboard, or run: brew install %s", s.Name(), s.Formula())
	}
	if _, running := m.extraRunning(s.ID()); running {
		return nil
	}
	port, err := m.servicePort(s)
	if err != nil {
		return err
	}
	if err := m.freePort(port, force); err != nil {
		return err
	}
	if err := os.MkdirAll(m.extraDir(s), 0o755); err != nil {
		return err
	}
	if err := s.prepare(m); err != nil {
		return fmt.Errorf("%s: %w", s.Name(), err)
	}
	bin, args := s.command(m)
	if err := m.spawn(s.ID(), bin, args...); err != nil {
		return fmt.Errorf("%s: %w", s.Name(), err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !s.ready(m) {
		if _, running := m.extraRunning(s.ID()); !running {
			return fmt.Errorf("%s stopped during start:\n%s", s.Name(), m.logTail(s.ID(), 12))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not get ready in 30 seconds. See the %s log", s.Name(), s.ID())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := s.afterStart(m); err != nil {
		return fmt.Errorf("%s: %w", s.Name(), err)
	}
	return m.setServiceSettings(s.ID(), func(st *ServiceSettings) { st.Enabled = true })
}

func (m *Manager) stopExtra(s extraService, remember bool) error {
	pid, running := m.extraRunning(s.ID())
	if running {
		syscall.Kill(pid, syscall.SIGTERM)
		// Databases can take some seconds to shut down cleanly.
		if !waitDead(pid, 20*time.Second) {
			syscall.Kill(pid, syscall.SIGKILL)
			waitDead(pid, 2*time.Second)
		}
	}
	os.Remove(m.extraPIDFile(s.ID()))
	if hook, ok := s.(interface{ afterStop(*Manager, bool) error }); ok {
		if err := hook.afterStop(m, remember); err != nil {
			return err
		}
	}
	if !remember {
		return nil
	}
	return m.setServiceSettings(s.ID(), func(st *ServiceSettings) { st.Enabled = false })
}

// spawn starts a process that runs in the foreground, as a detached child.
// Its output goes to the service log, and its PID goes to a PID file.
func (m *Manager) spawn(id, bin string, args ...string) error {
	if err := os.MkdirAll(m.path("run"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(m.extraLog(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- %s: %s %s\n", time.Now().Format(time.RFC3339), bin, strings.Join(args, " "))
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(m.extraPIDFile(id), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return err
	}
	// Wait in the background, so the child does not stay a zombie when it exits.
	go cmd.Wait()
	return nil
}

func (m *Manager) logTail(id string, n int) string {
	data, _ := os.ReadFile(m.extraLog(id))
	return lastLines(strings.TrimSpace(string(data)), n)
}

func (m *Manager) extraStatus() []ServiceStatus {
	var out []ServiceStatus
	for _, s := range extraServices() {
		pid, running := m.extraRunning(s.ID())
		st := m.serviceSettings(s.ID())
		status := ServiceStatus{
			ID: s.ID(), Name: s.Name(), Kind: s.Kind(), Version: s.Version(), Installed: s.Installed(),
			Running: running, PID: pid, Enabled: st.Enabled, Formula: s.Formula(),
			Installing: m.isInstalling(s.Formula()), DataSource: st.Source,
			Admin: s.ID() == "dns", // the first start writes /etc/resolver
		}
		if status.Installed {
			status.Detail = s.detail(m)
			status.Links = s.links(m)
			status.FlyEnvData = FlyEnvData(s.ID())
			_, status.HasDatabases = s.(databaseService)
			status.HasShell = status.HasDatabases
			if simple, ok := s.(*simpleService); ok {
				status.HasShell = simple.hasShell()
			}
		}
		out = append(out, status)
	}
	// Show one card for each kind that is not installed: the newest formula.
	return hideOldUninstalled(out)
}

func hideOldUninstalled(list []ServiceStatus) []ServiceStatus {
	family := func(id string) string {
		if i := strings.Index(id, "-"); i > 0 {
			return id[:i]
		}
		return id
	}
	installed := map[string]bool{}
	for _, s := range list {
		if s.Installed {
			installed[family(s.ID)] = true
		}
	}
	shown := map[string]bool{}
	var out []ServiceStatus
	for _, s := range list {
		f := family(s.ID)
		if s.Installed || s.Installing || s.Running {
			out = append(out, s)
			continue
		}
		if !installed[f] && !shown[f] {
			shown[f] = true
			out = append(out, s)
		}
	}
	return out
}

// ServiceSettingsUpdate changes the port or the data folder of an extra service.
type ServiceSettingsUpdate struct {
	Domains []string `json:"domains,omitempty"`
	Port    *int     `json:"port,omitempty"`
	DataDir *string  `json:"dataDir,omitempty"`
	Source  *string  `json:"source,omitempty"`
}

func (m *Manager) UpdateServiceSettings(id string, u ServiceSettingsUpdate) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	s, ok := findExtra(id)
	if !ok {
		return fmt.Errorf("unknown service %q", id)
	}
	if _, running := m.extraRunning(id); running && u.DataDir != nil {
		return fmt.Errorf("stop %s before you change its data folder", s.Name())
	}
	for i, d := range u.Domains {
		d = strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
		if !domainRe.MatchString(d) || strings.Contains(d, ".") {
			return fmt.Errorf("%q is not a domain ending. Use one word, for example test", d)
		}
		u.Domains[i] = d
	}
	if _, running := m.extraRunning(id); running && u.Domains != nil {
		return fmt.Errorf("stop %s before you change the domain endings", s.Name())
	}
	if u.Port != nil && (*u.Port < 1 || *u.Port > 65535) {
		return fmt.Errorf("port %d is not valid", *u.Port)
	}
	if u.DataDir != nil && *u.DataDir != "" && !filepath.IsAbs(*u.DataDir) {
		return errors.New("the data folder must be an absolute path")
	}
	return m.setServiceSettings(id, func(st *ServiceSettings) {
		if u.Port != nil {
			st.Port = *u.Port
		}
		if u.DataDir != nil {
			st.DataDir = *u.DataDir
		}
		if u.Source != nil {
			st.Source = *u.Source
		}
		if u.Domains != nil {
			st.Domains = u.Domains
		}
	})
}

func randomPassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// openInTerminal runs a command in the user's terminal app, through a
// .command file that the terminal opens.
func (m *Manager) openInTerminal(name string, argv ...string) error {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shQuote(a)
	}
	path := m.path("tmp", name+".command")
	script := "#!/bin/sh\nclear\nexec " + strings.Join(quoted, " ") + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return err
	}
	if out, err := exec.Command("/usr/bin/open", "-a", m.terminalApp(), path).CombinedOutput(); err != nil {
		return fmt.Errorf("open: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
