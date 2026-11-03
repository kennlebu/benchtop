package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveSite adds a new site (empty ID) or replaces the site with the same ID.
// It returns warnings that do not stop the save.
func (m *Manager) SaveSite(s Site) (Site, []string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	s, warnings, err := m.saveSite(s)
	if err != nil {
		return s, nil, err
	}
	return s, dedupe(append(warnings, m.applySites()...)), nil
}

func dedupe(list []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range list {
		for _, line := range strings.Split(item, "\n") {
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}

// saveSite stores the site in the config only. The caller runs applySites.
func (m *Manager) saveSite(s Site) (Site, []string, error) {
	s, err := NormalizeSite(s)
	if err != nil {
		return s, nil, err
	}
	var warnings []string
	if st, err := os.Stat(s.Root); err != nil || !st.IsDir() {
		warnings = append(warnings, fmt.Sprintf("root folder %s does not exist", s.Root))
	}
	if s.PHP != "" {
		if _, ok := findPHP(s.PHP); !ok {
			warnings = append(warnings, fmt.Sprintf("PHP %s is not installed. Run: brew install php@%s", s.PHP, s.PHP))
		}
	}

	cfg := m.Config()
	if strings.HasPrefix(s.ID, "parked-") {
		saved := false
		for _, other := range cfg.Sites {
			saved = saved || other.ID == s.ID
		}
		if !saved {
			s.ID = "" // a parked site that the user changes becomes a saved site
		}
	}
	for _, other := range cfg.Sites {
		if other.ID == s.ID {
			continue
		}
		for _, d := range other.Domains {
			for _, nd := range s.Domains {
				if d == nd {
					return s, nil, fmt.Errorf("domain %s is already used by site %s", d, other.ID)
				}
			}
		}
	}

	if s.ID == "" {
		s.ID = uniqueID(cfg.Sites, slugify(s.Domains[0]))
		cfg.Sites = append(cfg.Sites, s)
	} else {
		found := false
		for i := range cfg.Sites {
			if cfg.Sites[i].ID == s.ID {
				cfg.Sites[i] = s
				found = true
			}
		}
		if !found {
			return s, nil, fmt.Errorf("no site with id %q", s.ID)
		}
	}

	check := cfg
	check.Sites = m.mergeParked(cfg.Sites)
	if err := m.validateCaddyfile(check); err != nil {
		return s, nil, err
	}
	if err := m.updateConfig(func(c *Config) error { c.Sites = cfg.Sites; return nil }); err != nil {
		return s, nil, err
	}
	return s, warnings, nil
}

func (m *Manager) DeleteSite(idOrDomain string) ([]string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	found := false
	err := m.updateConfig(func(c *Config) error {
		var keep []Site
		for _, s := range c.Sites {
			if s.ID == idOrDomain || contains(s.Domains, idOrDomain) {
				found = true
				continue
			}
			keep = append(keep, s)
		}
		if !found {
			return fmt.Errorf("no site %q", idOrDomain)
		}
		if keep == nil {
			keep = []Site{}
		}
		c.Sites = keep
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.applySites(), nil
}

// applySites pushes the site list to Caddy, PHP and /etc/hosts.
func (m *Manager) applySites() []string {
	var warnings []string
	if _, running := m.caddyRunning(); running {
		if err := m.startNeededPHP(); err != nil {
			warnings = append(warnings, err.Error())
		}
		if err := m.reloadCaddy(); err != nil {
			warnings = append(warnings, "caddy reload: "+err.Error())
		}
	} else if err := m.writeCaddyfile(); err != nil {
		warnings = append(warnings, err.Error())
	}
	if err := m.applyNginx(); err != nil {
		warnings = append(warnings, "nginx: "+err.Error())
	}
	if err := m.syncHosts(); err != nil {
		warnings = append(warnings, "hosts: "+err.Error())
	}
	return warnings
}

func uniqueID(sites []Site, base string) string {
	if base == "" {
		base = "site"
	}
	id := base
	for n := 2; ; n++ {
		taken := false
		for _, s := range sites {
			if s.ID == id {
				taken = true
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

const (
	hostsFile  = "/etc/hosts"
	hostsBegin = "# >>> Benchtop (managed block, do not edit) >>>"
	hostsEnd   = "# <<< Benchtop <<<"

	// The project was called DevCenter before v0.2.
	legacyHostsBegin = "# >>> DevCenter (managed block, do not edit) >>>"
	legacyHostsEnd   = "# <<< DevCenter <<<"
)

// hostsDomains lists domains that need an /etc/hosts entry.
func (m *Manager) hostsDomains() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range m.Sites() {
		for _, d := range s.Domains {
			if strings.HasPrefix(d, "*.") || d == "localhost" || strings.HasSuffix(d, ".localhost") || seen[d] || m.wildcardCovers(d) {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// hostsBlock names the marked block that this Benchtop home owns. Only
// the default home owns the main block (and the old DevCenter block), so a
// second home, for example a test copy, cannot remove the main entries.
type hostsBlock struct {
	begin, end string
	legacy     bool
}

func (m *Manager) hostsBlock() hostsBlock {
	if filepath.Clean(m.Home) == defaultHomePath() {
		return hostsBlock{begin: hostsBegin, end: hostsEnd, legacy: true}
	}
	return hostsBlock{
		begin: fmt.Sprintf("# >>> Benchtop %s (managed block, do not edit) >>>", m.Home),
		end:   fmt.Sprintf("# <<< Benchtop %s <<<", m.Home),
	}
}

func renderHosts(current string, domains []string, b hostsBlock) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(current, "\n") {
		switch {
		case line == b.begin || (b.legacy && line == legacyHostsBegin):
			inBlock = true
		case line == b.end || (b.legacy && line == legacyHostsEnd):
			inBlock = false
		case !inBlock:
			kept = append(kept, line)
		}
	}
	out := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n"
	if len(domains) == 0 {
		return out
	}
	out += "\n" + b.begin + "\n"
	for _, d := range domains {
		out += fmt.Sprintf("127.0.0.1\t%s\n::1\t\t%s\n", d, d)
	}
	return out + b.end + "\n"
}

// hostsSyncCmd returns the root shell command that updates /etc/hosts, or
// "" when the file is already correct.
func (m *Manager) hostsSyncCmd() (string, error) {
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return "", err
	}
	next := renderHosts(string(data), m.hostsDomains(), m.hostsBlock())
	if next == string(data) {
		return "", nil
	}
	tmp := m.path("tmp", "hosts")
	if err := os.WriteFile(tmp, []byte(next), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("/bin/cp %s /etc/hosts && /bin/chmod 644 /etc/hosts && /usr/bin/dscacheutil -flushcache && (/usr/bin/killall -HUP mDNSResponder || true)",
		shQuote(tmp)), nil
}

func (m *Manager) syncHosts() error {
	cmd, err := m.hostsSyncCmd()
	if err != nil || cmd == "" {
		return err
	}
	return runAdmin("Benchtop needs your password to add your site domains to /etc/hosts.", cmd)
}

// SyncHosts updates /etc/hosts with the domains of all sites.
func (m *Manager) SyncHosts() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.syncHosts()
}

// SiteURL returns the main URL of a site.
func SiteURL(s Site) string {
	scheme := "http"
	if s.HTTPS {
		scheme = "https"
	}
	return scheme + "://" + strings.TrimPrefix(s.Domains[0], "*.")
}
