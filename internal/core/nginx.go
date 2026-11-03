package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// nginxService runs Nginx behind Caddy. Caddy keeps ports 80 and 443 and
// sends the domains of Nginx sites to Nginx on 127.0.0.1, so each site can
// use Caddy or Nginx and only one program needs the system ports.
type nginxService struct{}

func (s *nginxService) ID() string       { return "nginx" }
func (s *nginxService) Name() string     { return "Nginx" }
func (s *nginxService) Kind() string     { return "nginx" }
func (s *nginxService) Formula() string  { return "nginx" }
func (s *nginxService) Installed() bool  { return exists(s.bin()) }
func (s *nginxService) Version() string  { return brewVersion("nginx") }
func (s *nginxService) DefaultPort() int { return 8081 }
func (s *nginxService) bin() string      { return filepath.Join(brewPrefix("nginx"), "bin", "nginx") }

// etcDir is where Homebrew keeps mime.types and fastcgi_params, for
// example /opt/homebrew/etc/nginx.
func (s *nginxService) etcDir() string {
	return filepath.Join(filepath.Dir(filepath.Dir(brewPrefix("nginx"))), "etc", "nginx")
}

func (s *nginxService) conf(m *Manager) string { return filepath.Join(m.extraDir(s), "nginx.conf") }

func nginxSites(cfg Config) []Site {
	var out []Site
	for _, s := range cfg.Sites {
		if s.Server == "nginx" {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) renderNginx(cfg Config) string {
	s := &nginxService{}
	dir := m.extraDir(s)
	var b strings.Builder
	fmt.Fprintf(&b, `# Created by Benchtop from %s. Do not edit: Benchtop replaces this file.
# Put custom directives in the "extra" field of a site.
worker_processes 1;
error_log %s;
pid %s;
events { worker_connections 256; }
http {
	include %s;
	default_type application/octet-stream;
	sendfile on;
	client_max_body_size 128m;
	access_log %s;
	client_body_temp_path %s;
	fastcgi_temp_path %s;
	proxy_temp_path %s;
	# Caddy sends the real scheme and client address.
	real_ip_header X-Forwarded-For;
	set_real_ip_from 127.0.0.1;
	map $http_x_forwarded_proto $fastcgi_https { default ""; https on; }
`, m.configPath(), m.extraLog(s.ID()), filepath.Join(dir, "nginx.pid"), filepath.Join(s.etcDir(), "mime.types"),
		m.path("logs", "nginx-access.log"), filepath.Join(dir, "temp", "body"), filepath.Join(dir, "temp", "fastcgi"), filepath.Join(dir, "temp", "proxy"))
	port := m.servicePortOrDefault(s)
	for _, site := range nginxSites(cfg) {
		fmt.Fprintf(&b, "\n\t# site: %s\n\tserver {\n\t\tlisten 127.0.0.1:%d;\n\t\tserver_name %s;\n\t\troot \"%s\";\n",
			site.ID, port, strings.Join(site.Domains, " "), site.Root)
		if site.PHP != "" {
			fmt.Fprintf(&b, `		index index.php index.html;
		location / {
			try_files $uri $uri/ /index.php?$query_string;
		}
		location ~ \.php$ {
			try_files $uri =404;
			fastcgi_pass unix:%s;
			fastcgi_index index.php;
			include %s;
			fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
			fastcgi_param HTTPS $fastcgi_https;
		}
`, m.phpSocket(site.PHP), filepath.Join(s.etcDir(), "fastcgi_params"))
		} else {
			b.WriteString("\t\tindex index.html index.htm;\n\t\tlocation / {\n\t\t\ttry_files $uri $uri/ =404;\n\t\t}\n")
		}
		if extra := strings.TrimSpace(site.Extra); extra != "" {
			for _, line := range strings.Split(extra, "\n") {
				b.WriteString("\t\t" + strings.TrimRight(line, " \t") + "\n")
			}
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (s *nginxService) prepare(m *Manager) error {
	for _, d := range []string{"body", "fastcgi", "proxy"} {
		if err := os.MkdirAll(filepath.Join(m.extraDir(s), "temp", d), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(s.conf(m), []byte(m.renderNginx(m.siteConfig())), 0o644); err != nil {
		return err
	}
	if _, err := run(s.bin(), "-t", "-p", m.extraDir(s), "-c", s.conf(m)); err != nil {
		return fmt.Errorf("nginx rejected the config: %w", err)
	}
	return nil
}

func (s *nginxService) command(m *Manager) (string, []string) {
	return s.bin(), []string{"-p", m.extraDir(s), "-c", s.conf(m), "-g", "daemon off;"}
}

func (s *nginxService) ready(m *Manager) bool       { return dialTCP(m.servicePortOrDefault(s)) }
func (s *nginxService) afterStart(m *Manager) error { return nil }

func (s *nginxService) detail(m *Manager) string {
	return fmt.Sprintf("port %d (behind Caddy), %d sites", m.servicePortOrDefault(s), len(nginxSites(m.siteConfig())))
}

func (s *nginxService) files(m *Manager) []ConfigFile {
	return []ConfigFile{{Label: "nginx.conf", Path: s.conf(m), Note: "Benchtop makes this file. Use the Extra field of a site for changes."}}
}
func (s *nginxService) links(m *Manager) []Link { return nil }
func (s *nginxService) env(m *Manager) string   { return "" }

// applyNginx writes the Nginx config and reloads Nginx. It starts Nginx
// when Caddy runs and a site needs it.
func (m *Manager) applyNginx() error {
	s := &nginxService{}
	need := len(nginxSites(m.siteConfig())) > 0
	_, running := m.extraRunning(s.ID())
	if !s.Installed() {
		if need {
			return fmt.Errorf("a site uses Nginx, but Nginx is not installed. Install it on the Services page")
		}
		return nil
	}
	if running {
		if err := s.prepare(m); err != nil {
			return err
		}
		_, err := run(s.bin(), "-p", m.extraDir(s), "-c", s.conf(m), "-s", "reload")
		return err
	}
	if _, caddyUp := m.caddyRunning(); need && caddyUp {
		return m.startExtra(s, false)
	}
	return nil
}
