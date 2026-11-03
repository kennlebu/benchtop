package core

import (
	"fmt"
	"os"
	"path/filepath"
)

// SiteFix is a one-click fix for a site problem.
type SiteFix struct {
	Label string `json:"label"`
	// Action is "set-root" (Value is the new root) or "start" (Value is a service ID).
	Action string `json:"action"`
	Value  string `json:"value"`
}

type SiteHint struct {
	Level   string   `json:"level"` // "error" or "warn"
	Message string   `json:"message"`
	Fix     *SiteFix `json:"fix,omitempty"`
}

// SiteHealth finds likely causes of a 404 or a 502 for a site. It only
// checks files and processes, so it is fast enough to run on each refresh.
func (m *Manager) SiteHealth(s Site, running map[string]bool) []SiteHint {
	hints := []SiteHint{}
	if st, err := os.Stat(s.Root); err != nil || !st.IsDir() {
		return []SiteHint{{Level: "error", Message: "The root folder does not exist."}}
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
