package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// family groups the Homebrew formulas of one service, for example mysql,
// mysql@8.4 and mysql@8.0. Each formula is one version that can run next
// to the others, with its own port and data folder.
type family struct {
	id, name string
	tap      string // for example "mongodb/brew/"; "" for homebrew/core
	base     string // the formula name without "@version"
	// known are the formulas to show when the catalog is empty, for
	// example before the first refresh or with no network.
	known []string
	// oldest is the oldest version to offer. Older ones are end of life.
	oldest string
	make   func(f familyFormula) extraService
}

// familyFormula is one formula of a family.
type familyFormula struct {
	formula string // "mysql@8.4", or "mongodb/brew/mongodb-community@8.0"
	opt     string // the folder name in Homebrew's opt: "mongodb-community@8.0"
	key     string // "8.4"; "" for the plain formula (the newest version)
	id      string // "mysql-8.4", or the family ID for the plain formula
	label   string // the version to show: "8.4", or "26.10" for the plain formula
}

// VersionChoice is a version that the user can install from a card.
type VersionChoice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// catalogEntry is what Homebrew says about one formula.
type catalogEntry struct {
	Version    string `json:"version"`
	Deprecated bool   `json:"deprecated,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
}

type catalogFile struct {
	Updated  time.Time               `json:"updated"`
	Formulas map[string]catalogEntry `json:"formulas"`
}

var (
	catalogMu sync.RWMutex
	catalog   = map[string]catalogEntry{}
)

func catalogGet(formula string) (catalogEntry, bool) {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	e, ok := catalog[formula]
	return e, ok
}

func catalogNames() []string {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	return out
}

func families() []family {
	return []family{
		{id: "mysql", name: "MySQL", base: "mysql", known: []string{"mysql", "mysql@8.4", "mysql@8.0"}, oldest: "8.0",
			make: func(f familyFormula) extraService {
				return &mysqlService{key: f.key, label: f.label, formula: f.formula}
			}},
		{id: "mariadb", name: "MariaDB", base: "mariadb", known: []string{"mariadb", "mariadb@11.8", "mariadb@11.4", "mariadb@10.11"}, oldest: "10.11",
			make: func(f familyFormula) extraService {
				return &mysqlService{key: f.key, label: f.label, formula: f.formula, maria: true}
			}},
		{id: "postgres", name: "PostgreSQL", base: "postgresql", known: []string{"postgresql@17", "postgresql@16", "postgresql@15", "postgresql@14"}, oldest: "14",
			make: func(f familyFormula) extraService { return &postgresService{key: f.key, formula: f.formula} }},
		{id: "redis", name: "Redis", base: "redis", known: []string{"redis"}, oldest: "7",
			make: func(f familyFormula) extraService { return simpleVersion(redisService(), f) }},
		{id: "mongodb", name: "MongoDB", tap: "mongodb/brew/", base: "mongodb-community", known: []string{"mongodb/brew/mongodb-community"}, oldest: "7.0",
			make: func(f familyFormula) extraService { return simpleVersion(mongoService(), f) }},
	}
}

// formulas lists the versions of a family: the ones in the catalog, the
// ones that are installed, and the known ones. The newest comes first.
func (fam family) formulas() []familyFormula {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(fam.tap+fam.base) + `(@[0-9][0-9.]*)?$`)
	names := map[string]bool{}
	for _, n := range fam.known {
		names[n] = true
	}
	for _, n := range catalogNames() {
		if re.MatchString(n) {
			names[n] = true
		}
	}
	for _, p := range []string{"/opt/homebrew/opt", "/usr/local/opt"} {
		dirs, _ := filepath.Glob(filepath.Join(p, fam.base+"*"))
		for _, d := range dirs {
			if n := fam.tap + filepath.Base(d); re.MatchString(n) {
				names[n] = true
			}
		}
	}
	var out []familyFormula
	for n := range names {
		f := familyFormula{formula: n, opt: strings.TrimPrefix(n, fam.tap), id: fam.id}
		if i := strings.Index(n, "@"); i > 0 {
			f.key = n[i+1:]
			f.id = fam.id + "-" + f.key
			f.label = f.key
		} else {
			v := brewVersion(f.opt)
			if v == "" {
				if e, ok := catalogGet(n); ok {
					v = e.Version
				}
			}
			f.label = majorMinor(v)
		}
		installed := brewPrefix(f.opt) != ""
		if e, ok := catalogGet(n); ok && e.Disabled && !installed {
			continue
		}
		if !installed && f.key != "" && fam.oldest != "" && versionLess(f.key, fam.oldest) {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].label, out[j].label
		if a == "" || b == "" {
			return a == "" && b != "" // the plain formula first when its version is not known
		}
		return versionLess(b, a)
	})
	return out
}

func majorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func familyOf(id string) string {
	if i := strings.Index(id, "-"); i > 0 {
		return id[:i]
	}
	return id
}

func (m *Manager) catalogPath() string { return m.path("tmp", "brew-catalog.json") }

// LoadCatalog reads the saved Homebrew catalog. RefreshCatalog makes it.
func (m *Manager) LoadCatalog() time.Time {
	data, err := os.ReadFile(m.catalogPath())
	if err != nil {
		return time.Time{}
	}
	var c catalogFile
	if json.Unmarshal(data, &c) != nil {
		return time.Time{}
	}
	catalogMu.Lock()
	catalog = c.Formulas
	catalogMu.Unlock()
	return c.Updated
}

// RefreshCatalog asks Homebrew which versions of each service exist. It
// takes a second or two, so the dashboard runs it in the background, at
// most once a day.
func (m *Manager) RefreshCatalog(maxAge time.Duration) error {
	if updated := m.LoadCatalog(); time.Since(updated) < maxAge {
		return nil
	}
	brew := brewBin()
	if brew == "" {
		return nil
	}
	var parts []string
	for _, fam := range families() {
		parts = append(parts, regexp.QuoteMeta(fam.tap+fam.base))
	}
	env := append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1")
	search := exec.Command(brew, "search", "/^("+strings.Join(parts, "|")+`)(@[0-9.]+)?$/`)
	search.Env = env
	out, err := search.Output()
	if err != nil {
		return brewError("brew search", err)
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		return nil
	}
	// Ask about each tap on its own: Homebrew refuses a tap that the
	// user does not trust, and that must not hide the other versions.
	groups := map[string][]string{}
	for _, n := range names {
		tap := ""
		if i := strings.LastIndex(n, "/"); i > 0 {
			tap = n[:i]
		}
		groups[tap] = append(groups[tap], n)
	}
	c := catalogFile{Updated: time.Now(), Formulas: map[string]catalogEntry{}}
	for tap, group := range groups {
		entries, err := brewInfo(brew, env, group)
		if err != nil && tap == "" {
			return err
		}
		for _, n := range group {
			c.Formulas[n] = entries[n] // no version when the tap is not trusted
		}
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(m.catalogPath(), data, 0o644); err != nil {
		return err
	}
	catalogMu.Lock()
	catalog = c.Formulas
	catalogMu.Unlock()
	return nil
}

func brewInfo(brew string, env, names []string) (map[string]catalogEntry, error) {
	info := exec.Command(brew, append([]string{"info", "--json=v2"}, names...)...)
	info.Env = env
	out, err := info.Output()
	if err != nil {
		return nil, brewError("brew info", err)
	}
	var parsed struct {
		Formulae []struct {
			FullName   string `json:"full_name"`
			Deprecated bool   `json:"deprecated"`
			Disabled   bool   `json:"disabled"`
			Versions   struct {
				Stable string `json:"stable"`
			} `json:"versions"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	entries := map[string]catalogEntry{}
	for _, f := range parsed.Formulae {
		entries[f.FullName] = catalogEntry{Version: f.Versions.Stable, Deprecated: f.Deprecated, Disabled: f.Disabled}
	}
	return entries, nil
}

func brewError(what string, err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return fmt.Errorf("%s: %s", what, lastLines(string(exit.Stderr), 3))
	}
	return fmt.Errorf("%s: %w", what, err)
}
