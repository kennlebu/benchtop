package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

func (m *Manager) caddyBin() string {
	return findBinary(m.Config().Bin.Caddy, "/opt/homebrew/bin/caddy", "/usr/local/bin/caddy", "caddy")
}

func (m *Manager) caddyfilePath() string { return m.path("caddy", "Caddyfile") }
func (m *Manager) caddyPIDFile() string  { return m.path("caddy", "caddy.pid") }

var (
	caddyVerOnce sync.Once
	caddyVer     string
)

func (m *Manager) caddyVersion() string {
	caddyVerOnce.Do(func() {
		if bin := m.caddyBin(); bin != "" {
			out, _ := run(bin, "version")
			if f := strings.Fields(out); len(f) > 0 {
				caddyVer = strings.TrimPrefix(f[0], "v")
			}
		}
	})
	return caddyVer
}

func (m *Manager) caddyRunning() (int, bool) {
	pid := readPID(m.caddyPIDFile())
	return pid, pidAlive(pid)
}

// renderCaddyfile builds the full Caddyfile from the config.
func (m *Manager) renderCaddyfile(cfg Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# Created by Benchtop from %s. Do not edit: Benchtop replaces this file.
# Put custom directives in the "extra" field of a site.
{
	admin %s
	http_port %d
	https_port %d
	storage file_system %s
	skip_install_trust
	log {
		output file %s {
			roll_size 10mb
			roll_keep 5
		}
	}
	pki {
		ca local {
			name "Benchtop Local CA"
		}
	}
}
`, m.configPath(), cfg.CaddyAdmin, cfg.HTTPPort, cfg.HTTPSPort, m.path("caddy", "data"), m.path("logs", "caddy.log"))

	for _, s := range cfg.Sites {
		addrs := make([]string, len(s.Domains))
		for i, d := range s.Domains {
			if s.HTTPS {
				addrs[i] = d
			} else {
				addrs[i] = "http://" + d
			}
		}
		fmt.Fprintf(&b, "\n# site: %s\n%s {\n", s.ID, strings.Join(addrs, ", "))
		if s.HTTPS {
			b.WriteString("\ttls internal\n")
		}
		fmt.Fprintf(&b, "\troot * %q\n", s.Root)
		b.WriteString("\tencode zstd gzip\n")
		if s.PHP != "" {
			fmt.Fprintf(&b, "\tphp_fastcgi unix/%s\n", phpSocket(s.PHP))
		}
		b.WriteString("\tfile_server\n")
		if extra := strings.TrimSpace(s.Extra); extra != "" {
			for _, line := range strings.Split(extra, "\n") {
				b.WriteString("\t" + strings.TrimRight(line, " \t") + "\n")
			}
		}
		fmt.Fprintf(&b, "\tlog {\n\t\toutput file %s {\n\t\t\troll_size 10mb\n\t\t\troll_keep 3\n\t\t}\n\t}\n}\n",
			m.path("logs", "sites", s.ID+".log"))
	}
	return b.String()
}

// validateCaddyfile checks a config with Caddy before we save it.
func (m *Manager) validateCaddyfile(cfg Config) error {
	bin := m.caddyBin()
	if bin == "" {
		return nil
	}
	tmp := m.path("tmp", "Caddyfile.check")
	if err := os.WriteFile(tmp, []byte(m.renderCaddyfile(cfg)), 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := run(bin, "adapt", "--config", tmp, "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("caddy rejected the config: %w", err)
	}
	return nil
}

func (m *Manager) writeCaddyfile() error {
	content := []byte(m.renderCaddyfile(m.Config()))
	old, _ := os.ReadFile(m.caddyfilePath())
	if bytes.Equal(old, content) {
		return nil
	}
	return os.WriteFile(m.caddyfilePath(), content, 0o644)
}

func (m *Manager) startCaddy(force bool) error {
	bin := m.caddyBin()
	if bin == "" {
		return fmt.Errorf("caddy is not installed. Run: brew install caddy")
	}
	if _, running := m.caddyRunning(); running {
		return m.reloadCaddy()
	}
	if err := m.writeCaddyfile(); err != nil {
		return err
	}
	cfg := m.Config()
	ports := []int{cfg.HTTPPort}
	for _, s := range cfg.Sites {
		if s.HTTPS {
			ports = append(ports, cfg.HTTPSPort)
			break
		}
	}
	for _, p := range ports {
		if err := m.freePort(p, force); err != nil {
			return err
		}
	}
	return runDetached(m.path("logs", "caddy-run.log"), bin, "start",
		"--config", m.caddyfilePath(), "--adapter", "caddyfile", "--pidfile", m.caddyPIDFile())
}

func (m *Manager) reloadCaddy() error {
	if err := m.writeCaddyfile(); err != nil {
		return err
	}
	if _, running := m.caddyRunning(); !running {
		return nil
	}
	_, err := run(m.caddyBin(), "reload", "--config", m.caddyfilePath(), "--adapter", "caddyfile")
	return err
}

func (m *Manager) stopCaddy() error {
	pid, running := m.caddyRunning()
	if !running {
		return nil
	}
	if _, err := run(m.caddyBin(), "stop", "--config", m.caddyfilePath(), "--adapter", "caddyfile"); err != nil {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	if !waitDead(pid, 5*time.Second) {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	os.Remove(m.caddyPIDFile())
	return nil
}

// freePort returns an error when another program holds the port. With
// force, it stops that program (with the admin password if needed).
func (m *Manager) freePort(port int, force bool) error {
	if !portInUse(port) {
		return nil
	}
	if !force {
		return &PortInUseError{Port: port, Owner: portOwner(port)}
	}
	cmd := fmt.Sprintf("/usr/sbin/lsof -t -nP -iTCP:%d -sTCP:LISTEN | /usr/bin/xargs kill", port)
	if _, err := run("/bin/sh", "-c", cmd); err != nil || waitPortFree(port) != nil {
		if err := runAdmin(fmt.Sprintf("Benchtop needs your password to stop the program on port %d.", port), cmd); err != nil {
			return err
		}
	}
	return waitPortFree(port)
}

func waitPortFree(port int) error {
	for i := 0; i < 30; i++ {
		if !portInUse(port) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &PortInUseError{Port: port}
}

// TrustCA adds the Caddy local CA to the macOS keychain, for HTTPS sites.
func (m *Manager) TrustCA() error {
	root := m.path("caddy", "data", "pki", "authorities", "local", "root.crt")
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("no CA yet. Turn on HTTPS for a site and start Caddy first")
	}
	return runAdmin("Benchtop needs your password to trust its local HTTPS certificate.",
		"/usr/bin/security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "+shQuote(root))
}

// CaddyfilePath is shown to the user for reference.
func (m *Manager) CaddyfilePath() string { return filepath.Clean(m.caddyfilePath()) }
