package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SiteSuggestion is what Benchtop proposes for a folder in "Add site".
type SiteSuggestion struct {
	Root      string `json:"root"`
	RootNote  string `json:"rootNote,omitempty"`
	Domain    string `json:"domain"`
	PHP       string `json:"php"`
	PHPNote   string `json:"phpNote,omitempty"`
	Framework string `json:"framework,omitempty"`
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func detectFramework(dir string) string {
	switch {
	case exists(filepath.Join(dir, "artisan")):
		return "Laravel"
	case exists(filepath.Join(dir, "bin", "console")) && exists(filepath.Join(dir, "symfony.lock")):
		return "Symfony"
	case exists(filepath.Join(dir, "wp-config.php")) || exists(filepath.Join(dir, "wp-config-sample.php")):
		return "WordPress"
	}
	return ""
}

// SuggestSite looks at a folder and proposes a root, a domain and a PHP version.
func (m *Manager) SuggestSite(folder string) (SiteSuggestion, error) {
	folder = filepath.Clean(strings.TrimSpace(folder))
	if !filepath.IsAbs(folder) {
		return SiteSuggestion{}, fmt.Errorf("folder must be an absolute path")
	}
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return SiteSuggestion{}, fmt.Errorf("folder %s does not exist", folder)
	}
	project, root := folder, folder
	if filepath.Base(folder) == "public" {
		project = filepath.Dir(folder)
	} else if exists(filepath.Join(folder, "public", "index.php")) || exists(filepath.Join(folder, "public", "index.html")) {
		root = filepath.Join(folder, "public")
	}

	s := SiteSuggestion{Root: root, Framework: detectFramework(project)}
	if root != folder {
		what := "the app"
		if s.Framework != "" {
			what = s.Framework
		}
		s.RootNote = fmt.Sprintf("Uses public/, because %s serves files from there.", what)
	}
	s.Domain = m.freeDomain(slugify(filepath.Base(project)) + ".test")
	s.PHP, s.PHPNote = suggestPHP(project, root, DiscoverPHP())
	return s, nil
}

func (m *Manager) freeDomain(d string) string {
	if d == ".test" {
		d = "site.test"
	}
	base := strings.TrimSuffix(d, ".test")
	for n := 2; m.siteWithDomain(d); n++ {
		d = fmt.Sprintf("%s-%d.test", base, n)
	}
	return d
}

type composerJSON struct {
	Require map[string]string `json:"require"`
	Config  struct {
		Platform map[string]string `json:"platform"`
	} `json:"config"`
}

func suggestPHP(project, root string, installed []PHPVersion) (string, string) {
	if len(installed) == 0 {
		return "", "No PHP version is installed."
	}
	newest := installed[len(installed)-1].Key
	data, err := os.ReadFile(filepath.Join(project, "composer.json"))
	if err != nil {
		if matches, _ := filepath.Glob(filepath.Join(root, "*.php")); len(matches) > 0 {
			return newest, "No composer.json. Uses the newest installed PHP."
		}
		return "", "No PHP files found. This looks like a static site."
	}
	var c composerJSON
	if err := json.Unmarshal(data, &c); err != nil {
		return newest, "composer.json is not valid JSON. Uses the newest installed PHP."
	}
	if p := c.Config.Platform["php"]; p != "" {
		if v, ok := parseVersion(p); ok {
			key := fmt.Sprintf("%d.%d", v[0], v[1])
			for _, iv := range installed {
				if iv.Key == key {
					return key, fmt.Sprintf("From config.platform.php (%s) in composer.json.", p)
				}
			}
			return key, fmt.Sprintf("composer.json asks for PHP %s, which is not installed.", p)
		}
	}
	constraint := c.Require["php"]
	if constraint == "" {
		return newest, "composer.json does not name a PHP version. Uses the newest installed PHP."
	}
	// The oldest version that matches is the safest for older code.
	for _, iv := range installed {
		if v, ok := parseVersion(iv.Full); ok && satisfies(constraint, v) {
			return iv.Key, fmt.Sprintf("Oldest installed version that matches \"php\": \"%s\" in composer.json.", constraint)
		}
	}
	return newest, fmt.Sprintf("No installed PHP matches \"%s\". Uses the newest installed PHP.", constraint)
}

type semver [3]int

var verNumRe = regexp.MustCompile(`^v?(\d+)(?:\.(\d+|\*|x))?(?:\.(\d+|\*|x))?`)

func parseVersion(s string) (semver, bool) {
	mm := verNumRe.FindStringSubmatch(strings.TrimSpace(s))
	if mm == nil {
		return semver{}, false
	}
	var v semver
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(mm[i+1])
	}
	return v, true
}

// parts counts the numbers in a version such as "8.2" (2) or "8.2.1" (3).
func parts(s string) int {
	mm := verNumRe.FindStringSubmatch(strings.TrimSpace(s))
	n := 0
	for i := 1; i <= 3; i++ {
		if mm != nil && mm[i] != "" && mm[i] != "*" && mm[i] != "x" {
			n++
		}
	}
	return n
}

func cmp(a, b semver) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// satisfies checks a Composer version constraint. It supports the forms
// that PHP projects use: ^, ~, >=, >, <=, <, =, !=, wildcards, ranges with
// spaces or commas (AND), and | or || (OR).
func satisfies(constraint string, v semver) bool {
	for _, alt := range regexp.MustCompile(`\s*\|\|?\s*`).Split(constraint, -1) {
		ok := true
		for _, term := range strings.FieldsFunc(alt, func(r rune) bool { return r == ' ' || r == ',' }) {
			if !satisfiesTerm(term, v) {
				ok = false
				break
			}
		}
		if ok && strings.TrimSpace(alt) != "" {
			return true
		}
	}
	return false
}

func satisfiesTerm(t string, v semver) bool {
	op := ""
	for _, o := range []string{">=", "<=", "!=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(t, o) {
			op, t = o, strings.TrimSpace(t[len(o):])
			break
		}
	}
	t = strings.TrimSuffix(t, "@dev")
	if t == "*" {
		return true
	}
	b, ok := parseVersion(t)
	if !ok {
		return true // ignore what we cannot read
	}
	n := parts(t)
	switch op {
	case ">=":
		return cmp(v, b) >= 0
	case ">":
		return cmp(v, b) > 0
	case "<=":
		return cmp(v, b) <= 0
	case "<":
		return cmp(v, b) < 0
	case "!=":
		return cmp(v, b) != 0
	case "^":
		upper := semver{b[0] + 1, 0, 0}
		if b[0] == 0 {
			upper = semver{0, b[1] + 1, 0}
		}
		return cmp(v, b) >= 0 && cmp(v, upper) < 0
	case "~":
		upper := semver{b[0] + 1, 0, 0}
		if n >= 3 {
			upper = semver{b[0], b[1] + 1, 0}
		}
		return cmp(v, b) >= 0 && cmp(v, upper) < 0
	}
	// No operator: an exact version or a wildcard such as 8.2.* or 8.*.
	switch n {
	case 1:
		return v[0] == b[0]
	case 2:
		return v[0] == b[0] && v[1] == b[1]
	}
	return cmp(v, b) == 0
}
