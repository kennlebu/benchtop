package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

var (
	installMu  sync.Mutex
	installing = map[string]bool{}
)

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
	s, ok := findExtra(id)
	if !ok {
		return fmt.Errorf("Benchtop cannot install %q", id)
	}
	if s.Installed() {
		return nil
	}
	brew := brewBin()
	if brew == "" {
		return errors.New("Homebrew is not installed. See https://brew.sh")
	}
	installMu.Lock()
	if installing[s.Formula()] {
		installMu.Unlock()
		return nil
	}
	installing[s.Formula()] = true
	installMu.Unlock()

	go func() {
		defer func() {
			installMu.Lock()
			delete(installing, s.Formula())
			installMu.Unlock()
		}()
		f, err := os.OpenFile(m.path("logs", "brew.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "\n--- %s: brew install %s\n", time.Now().Format(time.RFC3339), s.Formula())
		cmd := exec.Command(brew, "install", s.Formula())
		cmd.Env = append(os.Environ(), "HOMEBREW_NO_ENV_HINTS=1", "NONINTERACTIVE=1")
		cmd.Stdout, cmd.Stderr = f, f
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(f, "--- brew install %s failed: %v\n", s.Formula(), err)
			return
		}
		fmt.Fprintf(f, "--- brew install %s finished\n", s.Formula())
	}()
	return nil
}
