package core

import (
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

const loginLabel = "dev.benchtop.login"

// LoginPlistPath is the LaunchAgent that starts services at login.
func LoginPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", loginLabel+".plist")
}

// executable is the path of the running benchtop binary.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Settings is the part of the config that the settings page can change.
type Settings struct {
	Login     LoginSettings `json:"login"`
	Editor    string        `json:"editor"`
	Terminal  string        `json:"terminal"`
	HTTPPort  int           `json:"httpPort"`
	HTTPSPort int           `json:"httpsPort"`
	FTPPort   int           `json:"ftpPort"`
}

// UpdateSettings saves the settings, then applies them. It returns notes
// for changes that need a restart.
func (m *Manager) UpdateSettings(s Settings) ([]string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	for _, p := range []int{s.HTTPPort, s.HTTPSPort, s.FTPPort} {
		if p < 1 || p > 65535 {
			return nil, fmt.Errorf("port %d is not valid", p)
		}
	}
	if s.HTTPPort == s.HTTPSPort {
		return nil, errors.New("the HTTP and HTTPS ports must be different")
	}
	old := m.Config()
	err := m.updateConfig(func(c *Config) error {
		c.Login = s.Login
		c.Editor = strings.TrimSpace(s.Editor)
		c.Terminal = strings.TrimSpace(s.Terminal)
		c.HTTPPort, c.HTTPSPort, c.FTP.Port = s.HTTPPort, s.HTTPSPort, s.FTPPort
		return nil
	})
	if err != nil {
		return nil, err
	}
	var notes []string
	if err := m.writeLoginAgent(); err != nil {
		notes = append(notes, "start at login: "+err.Error())
	}
	if old.HTTPPort != s.HTTPPort || old.HTTPSPort != s.HTTPSPort {
		if err := m.reloadCaddy(); err != nil {
			notes = append(notes, "caddy: "+err.Error())
		}
	}
	if _, running := m.ftpRunning(); running && old.FTP.Port != s.FTPPort {
		notes = append(notes, "Restart FTP to use the new port.")
	}
	return notes, nil
}

// writeLoginAgent adds or removes the LaunchAgent. macOS runs it at the
// next login. It runs "benchtop start --login" one time, and does not
// keep any service alive, so the Start and Stop buttons stay in control.
func (m *Manager) writeLoginAgent() error {
	path := LoginPlistPath()
	if !m.Config().Login.Enabled {
		exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), loginLabel)).Run()
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := executable()
	if err != nil {
		return err
	}
	env := ""
	if h := os.Getenv("BENCHTOP_HOME"); h != "" {
		env = fmt.Sprintf("\t\t<key>BENCHTOP_HOME</key>\n\t\t<string>%s</string>\n", xmlEscape(h))
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>start</string>
		<string>--login</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
%s	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, loginLabel, xmlEscape(exe), env, xmlEscape(m.path("logs", "login.log")), xmlEscape(m.path("logs", "login.log")))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plist), 0o644)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// StartAtLogin starts the services that the login settings name.
func (m *Manager) StartAtLogin() error {
	cfg := m.Config()
	if !cfg.Login.Enabled {
		return nil
	}
	var errs []error
	if cfg.Login.UI {
		if _, err := m.StartUIBackground(); err != nil {
			errs = append(errs, fmt.Errorf("dashboard: %w", err))
		}
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	var adminCmds []string
	if cfg.Login.Web {
		if err := m.startNeededPHP(); err != nil {
			errs = append(errs, err)
		}
		if err := m.startCaddy(false); err != nil {
			errs = append(errs, fmt.Errorf("caddy: %w", err))
		}
		if cmd, err := m.hostsSyncCmd(); err != nil {
			errs = append(errs, fmt.Errorf("hosts: %w", err))
		} else if cmd != "" {
			adminCmds = append(adminCmds, cmd)
		}
	}
	if cfg.Login.Extras {
		if err := m.startEnabledExtras(); err != nil {
			errs = append(errs, err)
		}
	}
	if cfg.Login.FTP && cfg.FTP.Enabled {
		if _, running := m.ftpRunning(); !running {
			if cmd, err := m.ftpStartCmd(false); err != nil {
				errs = append(errs, fmt.Errorf("ftp: %w", err))
			} else {
				adminCmds = append(adminCmds, cmd)
			}
		}
	}
	if err := runAdmin("Benchtop needs your password to start the FTP server at login.", adminCmds...); err != nil {
		errs = append(errs, err)
	} else if len(adminCmds) > 0 && cfg.Login.FTP {
		m.waitFTP()
	}
	return errors.Join(errs...)
}

func (m *Manager) uiPIDFile() string { return m.path("ui.pid") }

// UIURL is the address of the dashboard.
func (m *Manager) UIURL() string { return fmt.Sprintf("http://127.0.0.1:%d", m.Config().UIPort) }

// StartUIBackground runs the dashboard as a background process, so it
// stays up after the terminal closes.
func (m *Manager) StartUIBackground() (string, error) {
	url := m.UIURL()
	if portInUse(m.Config().UIPort) {
		return url, nil
	}
	exe, err := executable()
	if err != nil {
		return "", err
	}
	logf, err := os.OpenFile(m.path("logs", "ui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "ui", "--no-open")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	os.WriteFile(m.uiPIDFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	cmd.Process.Release()
	for i := 0; i < 30; i++ {
		if portInUse(m.Config().UIPort) {
			return url, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("the dashboard did not start. See %s", m.path("logs", "ui.log"))
}

// StopUIBackground stops a dashboard that StartUIBackground started.
func (m *Manager) StopUIBackground() error {
	pid := readPID(m.uiPIDFile())
	if !pidAlive(pid) {
		os.Remove(m.uiPIDFile())
		if portInUse(m.Config().UIPort) {
			return fmt.Errorf("a dashboard runs on port %d, but Benchtop did not start it in the background. Stop it with Ctrl-C in its terminal", m.Config().UIPort)
		}
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	waitDead(pid, 3*time.Second)
	os.Remove(m.uiPIDFile())
	return nil
}
