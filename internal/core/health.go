package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SiteFix is a one-click fix for a site problem.
type SiteFix struct {
	Label string `json:"label"`
	// Action is "set-root" (Value is the new root), "start" (Value is a
	// service ID) or "hosts" (sync /etc/hosts).
	Action string `json:"action"`
	Value  string `json:"value"`
}

type SiteHint struct {
	Level   string   `json:"level"` // "error" or "warn"
	Message string   `json:"message"`
	Fix     *SiteFix `json:"fix,omitempty"`
}

// inHosts tells if /etc/hosts has a line for the domain.
func inHosts(hosts, domain string) bool {
	for _, line := range strings.Split(hosts, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		for _, name := range f[min(1, len(f)):] {
			if name == domain {
				return true
			}
		}
	}
	return false
}

// subdomainHint tells when the subdomains of a site cannot work, because
// the wildcard DNS does not answer for them.
func subdomainHint(m *Manager, s Site) *SiteHint {
	var wild string
	for _, h := range s.Hosts() {
		if strings.HasPrefix(h, "*.") {
			wild = h
			break
		}
	}
	// A parked site has subdomains on by default. It gets no hint, because
	// the user did not ask for them.
	if wild == "" || s.Parked != "" || m.wildcardCovers("x"+wild[1:]) {
		return nil
	}
	if _, running := m.extraRunning("dns"); !running {
		return &SiteHint{Level: "warn", Message: "Subdomains such as " + "api" + wild[1:] + " need wildcard domains.",
			Fix: &SiteFix{Label: "Turn on wildcard domains", Action: "start", Value: "dns"}}
	}
	tld := wild[strings.LastIndex(wild, ".")+1:]
	return &SiteHint{Level: "warn", Message: "Subdomains need the domain ending ." + tld + ". Add it in the menu of the Wildcard domains card."}
}

// SiteHealth finds likely causes of a 404 or a 502 for a site. It only
// checks files and processes, so it is fast enough to run on each refresh.
func (m *Manager) SiteHealth(s Site, running map[string]bool) []SiteHint {
	hints := []SiteHint{}
	if st, err := os.Stat(s.Root); err != nil || !st.IsDir() {
		return []SiteHint{{Level: "error", Message: "The root folder does not exist."}}
	}
	if d := s.Domains[0]; !strings.HasSuffix(d, ".localhost") && d != "localhost" && !strings.HasPrefix(d, "*.") && !m.wildcardCovers(d) {
		if hosts, err := os.ReadFile(hostsFile); err == nil && !inHosts(string(hosts), d) {
			hints = append(hints, SiteHint{Level: "warn", Message: "This domain is not in /etc/hosts yet, so the browser cannot find it.",
				Fix: &SiteFix{Label: "Sync /etc/hosts", Action: "hosts"}})
		}
	}
	if wild := subdomainHint(m, s); wild != nil {
		hints = append(hints, *wild)
	}
	if s.Server == "nginx" {
		if !(&nginxService{}).Installed() {
			hints = append(hints, SiteHint{Level: "error", Message: "This site uses Nginx, but Nginx is not installed. Install it on the Services page."})
		} else if _, up := m.extraRunning("nginx"); running["caddy"] && !up {
			hints = append(hints, SiteHint{Level: "error", Message: "Nginx is stopped, so this site gives a 502 error.",
				Fix: &SiteFix{Label: "Start Nginx", Action: "start", Value: "nginx"}})
		}
	}
	if s.PHP != "" {
		id := "php-" + s.PHP
		if _, ok := findPHP(s.PHP); !ok {
			hints = append(hints, SiteHint{Level: "error", Message: fmt.Sprintf("PHP %s is not installed. Run: brew install php@%s", s.PHP, s.PHP)})
		} else if running["caddy"] && !running[id] {
			hints = append(hints, SiteHint{Level: "error", Message: fmt.Sprintf("PHP %s is stopped, so PHP pages give a 502 error.", s.PHP),
				Fix: &SiteFix{Label: "Start PHP " + s.PHP, Action: "start", Value: id}})
		}
	}
	hasIndex := exists(filepath.Join(s.Root, "index.html")) || exists(filepath.Join(s.Root, "index.htm")) ||
		(s.PHP != "" && exists(filepath.Join(s.Root, "index.php")))
	if !hasIndex {
		pub := filepath.Join(s.Root, "public")
		if exists(filepath.Join(pub, "index.php")) || exists(filepath.Join(pub, "index.html")) {
			hints = append(hints, SiteHint{Level: "warn", Message: "No index file in the root, so / gives a 404. The app is probably in public/.",
				Fix: &SiteFix{Label: "Use public/", Action: "set-root", Value: pub}})
		} else if s.PHP == "" && exists(filepath.Join(s.Root, "index.php")) {
			hints = append(hints, SiteHint{Level: "warn", Message: "This site has index.php, but no PHP version is set."})
		} else {
			hints = append(hints, SiteHint{Level: "warn", Message: "No index file in the root, so / gives a 404."})
		}
	}
	return hints
}
