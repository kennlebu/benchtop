package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	installMu  sync.Mutex
	installing = map[string]bool{}
	// installs counts the Homebrew and PECL installs that run.
	installs sync.WaitGroup
)

// WaitInstalls waits for the installs that run in the background. The
// command line uses it, because its process must not end first.
func (m *Manager) WaitInstalls() { installs.Wait() }

func (m *Manager) isInstalling(formula string) bool {
	installMu.Lock()
	defer installMu.Unlock()
	return installing[formula]
}

func brewBin() string {
	return findBinary("", "/opt/homebrew/bin/brew", "/usr/local/bin/brew")
}

// Install runs "brew install" for a service in the background. The output
// goes to the "brew" log. The dashboard sees the end through the status.
func (m *Manager) Install(id string) error {
	if strings.HasPrefix(id, "php-") {
		key := strings.TrimPrefix(id, "php-")
		if _, ok := findPHP(key); ok {
			return nil
		}
		if !phpVersionRe.MatchString(key) || versionLess(key, oldestPHP) {
			return fmt.Errorf("Benchtop cannot install PHP %q. Use PHP %s or newer, for example php-8.4", key, oldestPHP)
		}
		return m.installFormula(phpFormulaFor(key))
	}
	s, ok := findExtra(id)
	if !ok {
		ids := []string{"php-<version>"}
		for _, e := range extraServices() {
			if e.Formula() != "" && !e.Installed() {
				ids = append(ids, e.ID())
			}
		}
		return fmt.Errorf("Benchtop cannot install %q. You can install: %s", id, strings.Join(ids, ", "))
	}
	if s.Installed() {
		return nil
	}
	return m.installFormula(s.Formula())
}

var phpVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

// installFormula runs "brew install" in the background.
func (m *Manager) installFormula(formula string) error {
	brew := brewBin()
	if brew == "" {
		return errors.New("Homebrew is not installed. See https://brew.sh")
	}
	installMu.Lock()
	if installing[formula] {
		installMu.Unlock()
		return nil
	}
	installing[formula] = true
	installMu.Unlock()

	installs.Add(1)
	go func() {
		defer installs.Done()
		defer func() {
			installMu.Lock()
			delete(installing, formula)
			installMu.Unlock()
		}()
		f, err := os.OpenFile(m.path("logs", "brew.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "\n--- %s: brew install %s\n", time.Now().Format(time.RFC3339), formula)
		cmd := exec.Command(brew, "install", formula)
		cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "NONINTERACTIVE=1")
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f, "--- brew install %s failed: %v\n", formula, err)
			return
		}
		fmt.Fprintf(f, "--- brew install %s finished\n", formula)
	}()
	return nil
}
