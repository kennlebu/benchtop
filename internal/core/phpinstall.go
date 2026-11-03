package core

import (
	"regexp"
	"sort"
	"strings"
)

// phpTap has the PHP versions that Homebrew core does not keep: old ones
// such as 7.4, and previews of the next release.
const phpTap = "shivammathur/php/"

// oldestPHP is the oldest PHP version that the dashboard offers.
const oldestPHP = "7.4"

var phpFormulaRe = regexp.MustCompile(`^(` + regexp.QuoteMeta(phpTap) + `)?php(@([0-9]+\.[0-9]+))?$`)

// phpSearchPatterns are added to the catalog search.
var phpSearchPatterns = []string{"php", phpTap + "php"}

// PHPChoice is a PHP version that the user can install.
type PHPChoice struct {
	Key        string `json:"key"` // "8.4"
	Formula    string `json:"formula"`
	Version    string `json:"version,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
	// Preview is a version that is newer than the newest PHP in Homebrew
	// core, so it is not a final release yet.
	Preview bool `json:"preview,omitempty"`
}

// phpFormulas maps each PHP version to its formula. A formula from Homebrew
// core comes before the same version from the tap.
func phpFormulas() map[string]PHPChoice {
	out := map[string]PHPChoice{}
	newestCore := ""
	for _, name := range catalogNames() {
		m := phpFormulaRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		e, _ := catalogGet(name)
		if e.Disabled {
			continue
		}
		key := m[3]
		if key == "" {
			key = majorMinor(e.Version)
		}
		if key == "" {
			continue
		}
		fromCore := m[1] == ""
		if fromCore && (newestCore == "" || versionLess(newestCore, key)) {
			newestCore = key
		}
		if old, ok := out[key]; ok && (!strings.HasPrefix(old.Formula, phpTap) || !fromCore) {
			continue
		}
		out[key] = PHPChoice{Key: key, Formula: name, Version: e.Version, Deprecated: e.Deprecated}
	}
	for k, c := range out {
		c.Preview = newestCore != "" && versionLess(newestCore, k)
		out[k] = c
	}
	return out
}

// phpFormulaFor is the formula to install for a PHP version, or "".
func phpFormulaFor(key string) string {
	if c, ok := phpFormulas()[key]; ok {
		return c.Formula
	}
	if versionLess(key, "8.1") {
		return phpTap + "php@" + key
	}
	return "php@" + key
}

// PHPChoices are the PHP versions that are not installed, the newest first.
func (m *Manager) PHPChoices() []PHPChoice {
	installed := map[string]bool{}
	for _, v := range DiscoverPHP() {
		installed[v.Key] = true
	}
	var out []PHPChoice
	for k, c := range phpFormulas() {
		if !installed[k] && !versionLess(k, oldestPHP) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[j].Key, out[i].Key) })
	return out
}

// PHPInstalling is the PHP version that Homebrew installs now, or "".
func (m *Manager) PHPInstalling() string {
	installMu.Lock()
	defer installMu.Unlock()
	for f := range installing {
		if mm := phpFormulaRe.FindStringSubmatch(f); mm != nil {
			if mm[3] != "" {
				return mm[3]
			}
			if e, ok := catalogGet(f); ok {
				return majorMinor(e.Version)
			}
		}
	}
	return ""
}
