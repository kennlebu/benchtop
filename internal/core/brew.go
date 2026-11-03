package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	// installErrors holds why the last install of a formula failed.
	installErrors = map[string]string{}
	// installs counts the Homebrew and PECL installs that run.
	installs sync.WaitGroup
)

// WaitInstalls waits for the installs that run in the background. The
// command line uses it, because its process must not end first.
func (m *Manager) WaitInstalls() { installs.Wait() }

// InstallError says why the last install of a formula failed, or "".
func (m *Manager) InstallError(formula string) string {
	installMu.Lock()
	defer installMu.Unlock()
	return installErrors[formula]
}

var untrustedTapRe = regexp.MustCompile(`brew trust ([a-z0-9_-]+/[a-z0-9_-]+)\b`)

// installFailure explains a failed install in one sentence.
func installFailure(output string) string {
	if strings.Contains(output, "untrusted tap") {
		if mm := untrustedTapRe.FindStringSubmatch(output); mm != nil {
			return fmt.Sprintf("Homebrew does not trust the tap %s. If you trust it, run: brew trust %s. Then install again.", mm[1], mm[1])
		}
	}
	return "The install failed. See the brew log."
}

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
	delete(installErrors, formula)
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
		var out bytes.Buffer
		cmd.Stdout = io.MultiWriter(f, &out)
		cmd.Stderr = cmd.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f, "--- brew install %s failed: %v\n", formula, err)
			installMu.Lock()
			installErrors[formula] = installFailure(out.String())
			installMu.Unlock()
			return
		}
		fmt.Fprintf(f, "--- brew install %s finished\n", formula)
	}()
	return nil
}
