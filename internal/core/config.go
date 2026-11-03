package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Site is one local website served by Caddy.
type Site struct {
	ID      string   `json:"id"`
	Domains []string `json:"domains"`
	Root    string   `json:"root"`
	// PHP is a version key such as "8.2". Empty means a static site.
	PHP   string `json:"php"`
	HTTPS bool   `json:"https"`
	// Server is "" for Caddy, or "nginx" for Nginx behind Caddy.
	Server string `json:"server,omitempty"`
	// Extra is raw text added inside the site block: Caddyfile text, or
	// Nginx text for an Nginx site.
	Extra string `json:"extra,omitempty"`
}

type FTPSettings struct {
	Enabled          bool   `json:"enabled"`
	Port             int    `json:"port"`
	PassivePortRange string `json:"passivePortRange"`
}

// Binaries lets the user override where tools are found.
type Binaries struct {
	Caddy    string `json:"caddy,omitempty"`
	PureFTPd string `json:"pureFtpd,omitempty"`
	PurePW   string `json:"purePw,omitempty"`
}

// LoginSettings controls what starts when the user logs in to macOS.
type LoginSettings struct {
	Enabled bool `json:"enabled"`
	Web     bool `json:"web"` // Caddy and the PHP versions that sites use
	UI      bool `json:"ui"`
	FTP     bool `json:"ftp"`
	// Extras are the databases and other services that were on last time.
	Extras bool `json:"extras"`
}

type Config struct {
	HTTPPort   int           `json:"httpPort"`
	HTTPSPort  int           `json:"httpsPort"`
	CaddyAdmin string        `json:"caddyAdmin"`
	UIPort     int           `json:"uiPort"`
	FTP        FTPSettings   `json:"ftp"`
	Bin        Binaries      `json:"bin"`
	Login      LoginSettings `json:"login"`
	// Editor and Terminal are macOS app names. Empty means detect.
	Editor   string `json:"editor,omitempty"`
	Terminal string `json:"terminal,omitempty"`
	Sites    []Site `json:"sites"`
	// Services holds settings for databases and other extra services, by ID.
	Services map[string]ServiceSettings `json:"services,omitempty"`
}

func defaultConfig() *Config {
	return &Config{
		HTTPPort:   80,
		HTTPSPort:  443,
		CaddyAdmin: "localhost:2099",
		UIPort:     7788,
		FTP:        FTPSettings{Enabled: true, Port: 21, PassivePortRange: "39000 40000"},
		Login:      LoginSettings{Web: true, UI: true, FTP: true, Extras: true},
		Sites:      []Site{},
		Services:   map[string]ServiceSettings{},
	}
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if cfg.Sites == nil {
		cfg.Sites = []Site{}
	}
	if cfg.Services == nil {
		cfg.Services = map[string]ServiceSettings{}
	}
	return cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var domainRe = regexp.MustCompile(`^(\*\.)?[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)

// NormalizeSite cleans the input and checks that the site is valid.
func NormalizeSite(s Site) (Site, error) {
	var domains []string
	seen := map[string]bool{}
	for _, d := range s.Domains {
		for _, part := range strings.FieldsFunc(d, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
			part = strings.ToLower(strings.TrimSpace(part))
			part = strings.TrimPrefix(strings.TrimPrefix(part, "http://"), "https://")
			part = strings.TrimSuffix(part, "/")
			if part == "" || seen[part] {
				continue
			}
			if !domainRe.MatchString(part) {
				return s, fmt.Errorf("domain %q is not valid", part)
			}
			seen[part] = true
			domains = append(domains, part)
		}
	}
	if len(domains) == 0 {
		return s, errors.New("add at least one domain")
	}
	s.Domains = domains
	s.Root = strings.TrimSpace(s.Root)
	if s.Root == "" {
		return s, errors.New("root folder is required")
	}
	if strings.HasPrefix(s.Root, "~/") {
		home, _ := os.UserHomeDir()
		s.Root = filepath.Join(home, s.Root[2:])
	}
	if !filepath.IsAbs(s.Root) {
		return s, fmt.Errorf("root folder %q must be an absolute path", s.Root)
	}
	if strings.ContainsAny(s.Root, "\"\n") {
		return s, errors.New("root folder must not contain quotes or new lines")
	}
	s.PHP = strings.TrimSpace(strings.TrimPrefix(s.PHP, "php"))
	if s.Server = strings.ToLower(strings.TrimSpace(s.Server)); s.Server == "caddy" {
		s.Server = ""
	}
	if s.Server != "" && s.Server != "nginx" {
		return s, fmt.Errorf("web server %q is not known. Use caddy or nginx", s.Server)
	}
	return s, nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, "*.")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
