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

// A parked folder gives each project folder in it a site: <name>.test.
// Parked sites are not saved in the config. Benchtop finds them again when
// the folders change. A saved site for the same project or domain wins, so
// the user can change a parked site by saving it as a normal site.

var parkedMu sync.RWMutex

// parkedSites is the result of the last scan, for each Manager home.
var parkedSites = map[string][]Site{}

// skipFolders are not projects.
var skipFolders = map[string]bool{"node_modules": true, "vendor": true}

// Parked lists the parked folders.
func (m *Manager) Parked() []string { return append([]string{}, m.Config().Parked...) }

// Park adds a folder and applies the new sites.
func (m *Manager) Park(dir string) ([]string, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if !filepath.IsAbs(dir) {
		return nil, errors.New("the folder must be an absolute path")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("folder %s does not exist", dir)
	}
	if home, _ := os.UserHomeDir(); dir == home || dir == "/" {
		return nil, errors.New("park a folder of projects, not your home folder")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	err := m.updateConfig(func(c *Config) error {
		if contains(c.Parked, dir) {
			return nil
		}
		c.Parked = append(c.Parked, dir)
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.scanParked()
	return m.applySites(), nil
}

// Unpark removes a parked folder. Its sites go away; the files stay.
func (m *Manager) Unpark(dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	m.opMu.Lock()
	defer m.opMu.Unlock()
	err := m.updateConfig(func(c *Config) error {
		var keep []string
		for _, d := range c.Parked {
			if d != dir {
				keep = append(keep, d)
			}
		}
		if len(keep) == len(c.Parked) {
			return fmt.Errorf("%s is not parked", dir)
		}
		c.Parked = keep
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.scanParked()
	return m.applySites(), nil
}

// scanParked reads the parked folders again. It tells if the list of
// parked sites changed.
func (m *Manager) scanParked() bool {
	var found []Site
	installed := DiscoverPHP()
	for _, dir := range m.Config().Parked {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || skipFolders[name] {
				continue
			}
			project := filepath.Join(dir, name)
			if st, err := os.Stat(project); err != nil || !st.IsDir() {
				continue // a file, or a broken link
			}
			slug := slugify(name)
			if slug == "" {
				continue
			}
			root := project
			for _, index := range []string{"index.php", "index.html"} {
				if exists(filepath.Join(project, "public", index)) {
					root = filepath.Join(project, "public")
					break
				}
			}
			php, _ := suggestPHP(project, root, installed)
			found = append(found, Site{
				ID: "parked-" + slug, Domains: []string{slug + ".test"}, Root: root, PHP: php,
				Parked: dir,
			})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	parkedMu.Lock()
	defer parkedMu.Unlock()
	changed := sitesKey(parkedSites[m.Home]) != sitesKey(found)
	parkedSites[m.Home] = found
	return changed
}

func sitesKey(list []Site) string {
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s|%s|%s|%s\n", s.ID, s.Root, s.PHP, strings.Join(s.Domains, ","))
	}
	return b.String()
}

// mergeParked adds the parked sites to the saved ones. A parked site is
// left out when a saved site uses its domain or its project folder.
func (m *Manager) mergeParked(saved []Site) []Site {
	parkedMu.RLock()
	parked := parkedSites[m.Home]
	parkedMu.RUnlock()
	out := append([]Site(nil), saved...)
	domains := map[string]bool{}
	for _, s := range saved {
		for _, d := range s.Domains {
			domains[d] = true
		}
	}
	for _, p := range parked {
		project := p.Root
		if filepath.Base(project) == "public" {
			project = filepath.Dir(project)
		}
		taken := domains[p.Domains[0]]
		for _, s := range saved {
			if s.Root == project || strings.HasPrefix(s.Root, project+"/") {
				taken = true
			}
		}
		if !taken {
			domains[p.Domains[0]] = true
			out = append(out, p)
		}
	}
	return out
}

// Sites lists the saved sites, then the parked sites.
func (m *Manager) Sites() []Site { return m.mergeParked(m.Config().Sites) }

// siteConfig is the config with the parked sites in its site list, for
// the Caddy and Nginx files.
func (m *Manager) siteConfig() Config {
	c := m.Config()
	c.Sites = m.mergeParked(c.Sites)
	return c
}

// CheckParked looks for new, changed or removed projects in the parked
// folders. When it finds a change, it reloads Caddy and Nginx. It changes
// /etc/hosts only when no password is needed, so a new folder never opens
// a password dialog: the site row shows a hint instead.
func (m *Manager) CheckParked() {
	if len(m.Config().Parked) == 0 {
		return
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if !m.scanParked() {
		return
	}
	if _, running := m.caddyRunning(); running {
		m.reloadCaddy()
	} else {
		m.writeCaddyfile()
	}
	m.applyNginx()
}
