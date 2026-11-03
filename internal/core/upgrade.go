package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// outdatedEntry is a newer release of an installed formula.
type outdatedEntry struct {
	Full    string `json:"full"`    // the full name, for example "shivammathur/php/php@7.4"
	Version string `json:"version"` // the new version, for example "8.4.13_1"
}

var (
	outdatedMu sync.RWMutex
	outdated   = map[string]outdatedEntry{} // by short formula name
	upgrading  = map[string]bool{}
)

// formulaOfBin finds the formula of a Homebrew binary, for example
// "pure-ftpd" for /opt/homebrew/sbin/pure-ftpd.
func formulaOfBin(path string) string {
	if path == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	// The formula folder follows "Cellar", or the last "opt" (the first one
	// is /opt itself).
	parts := strings.Split(path, "/")
	at := -1
	for i, p := range parts {
		if p == "Cellar" {
			at = i
			break
		}
		if p == "opt" && i > 1 {
			at = i
		}
	}
	if at < 0 || at+1 >= len(parts) {
		return ""
	}
	return parts[at+1]
}

func shortFormula(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// withoutRevision turns "8.2.35_1" into "8.2.35". A new revision is a
// rebuild of the same version, so Benchtop does not offer it.
func withoutRevision(v string) string {
	if i := strings.Index(v, "_"); i > 0 {
		return v[:i]
	}
	return v
}

// serviceFormula is the Homebrew formula that a service runs from, or "".
func (m *Manager) serviceFormula(id string) string {
	switch {
	case id == "caddy":
		return formulaOfBin(m.caddyBin())
	case id == "ftp":
		return formulaOfBin(m.pureFTPdBin())
	case strings.HasPrefix(id, "php-"):
		if v, ok := findPHP(strings.TrimPrefix(id, "php-")); ok {
			return formulaOfBin(v.FPM)
		}
		return ""
	}
	if s, ok := findExtra(id); ok && s.Installed() && s.Formula() != "" {
		return shortFormula(s.Formula())
	}
	return ""
}

// updateFor gives the new version of a formula, or "" when there is none.
func updateFor(formula string) string {
	outdatedMu.RLock()
	e, ok := outdated[formula]
	outdatedMu.RUnlock()
	if !ok || withoutRevision(e.Version) == brewVersion(formula) {
		return ""
	}
	return withoutRevision(e.Version)
}

func isUpgrading(formula string) bool {
	outdatedMu.RLock()
	defer outdatedMu.RUnlock()
	return upgrading[formula]
}

// addUpdates fills the update fields of the cards.
func (m *Manager) addUpdates(list []ServiceStatus) {
	for i := range list {
		if !list[i].Installed {
			continue
		}
		f := m.serviceFormula(list[i].ID)
		if f == "" {
			continue
		}
		list[i].Update = updateFor(f)
		list[i].Upgrading = isUpgrading(f)
	}
}

// refreshOutdated asks Homebrew which installed formulas have a newer
// release. It uses what Homebrew already knows and does not run "brew update".
func refreshOutdated(brew string, env []string) (map[string]outdatedEntry, error) {
	cmd := exec.Command(brew, "outdated", "--json=v2")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, brewError("brew outdated", err)
	}
	var parsed struct {
		Formulae []struct {
			Name    string `json:"name"`
			Current string `json:"current_version"`
			Pinned  bool   `json:"pinned"`
		} `json:"formulae"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	list := map[string]outdatedEntry{}
	for _, f := range parsed.Formulae {
		if !f.Pinned {
			list[shortFormula(f.Name)] = outdatedEntry{Full: f.Name, Version: f.Current}
		}
	}
	return list, nil
}

func setOutdated(list map[string]outdatedEntry) {
	outdatedMu.Lock()
	outdated = list
	outdatedMu.Unlock()
}

// Upgrade runs "brew upgrade" for the formula of a service in the
// background, then restarts the service if it ran. The output goes to
// the "brew" log.
func (m *Manager) Upgrade(id string) error {
	formula := m.serviceFormula(id)
	if formula == "" {
		return fmt.Errorf("%s does not come from Homebrew, so Benchtop cannot upgrade it", id)
	}
	if updateFor(formula) == "" {
		return fmt.Errorf("%s is already the newest version", formula)
	}
	brew := brewBin()
	if brew == "" {
		return errors.New("Homebrew is not installed. See https://brew.sh")
	}
	outdatedMu.Lock()
	if upgrading[formula] {
		outdatedMu.Unlock()
		return nil
	}
	upgrading[formula] = true
	full := outdated[formula].Full
	outdatedMu.Unlock()

	running := false
	for _, s := range m.Status() {
		if s.ID == id {
			running = s.Running
		}
	}
	installs.Add(1)
	go func() {
		defer installs.Done()
		defer func() {
			outdatedMu.Lock()
			delete(upgrading, formula)
			outdatedMu.Unlock()
		}()
		f, err := os.OpenFile(m.path("logs", "brew.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "\n--- %s: brew upgrade %s\n", time.Now().Format(time.RFC3339), full)
		cmd := exec.Command(brew, "upgrade", full)
		// Upgrade this formula and what it needs, not the other programs that use it.
		cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "NONINTERACTIVE=1", "HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK=1")
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f, "--- brew upgrade %s failed: %v\n", full, err)
			return
		}
		outdatedMu.Lock()
		delete(outdated, formula)
		outdatedMu.Unlock()
		m.saveOutdated()
		if !running {
			fmt.Fprintf(f, "--- brew upgrade %s finished\n", full)
			return
		}
		if err := m.restartAfterUpgrade(id); err != nil {
			fmt.Fprintf(f, "--- upgraded, but %s did not start again: %v\n", id, err)
			return
		}
		fmt.Fprintf(f, "--- brew upgrade %s finished, and %s runs again\n", full, id)
	}()
	return nil
}

// restartAfterUpgrade starts the new binary. An extra service keeps its
// "enabled" choice, because the user did not stop it.
func (m *Manager) restartAfterUpgrade(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if s, ok := findExtra(id); ok {
		if err := m.stopExtra(s, false); err != nil {
			return err
		}
		return m.startExtra(s, false)
	}
	if err := m.stop(id); err != nil {
		return err
	}
	return m.start(id, false)
}
