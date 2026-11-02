package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
)

// keychainService names a Keychain item. A Benchtop home other than the
// default one (for example a test copy) gets its own items.
func (m *Manager) keychainService(id string) string {
	name := "Benchtop " + id
	if m.Home != defaultHomePath() {
		sum := sha256.Sum256([]byte(m.Home))
		name += " " + hex.EncodeToString(sum[:4])
	}
	return name
}

// SavePassword stores a password in the macOS login Keychain.
func (m *Manager) savePassword(id, account, password string) error {
	out, err := exec.Command("/usr/bin/security", "add-generic-password", "-U",
		"-s", m.keychainService(id), "-a", account, "-w", password).CombinedOutput()
	if err != nil {
		return errors.New("keychain: " + strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) loadPassword(id, account string) (string, bool) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password",
		"-s", m.keychainService(id), "-a", account, "-w").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

func (m *Manager) deletePassword(id, account string) {
	exec.Command("/usr/bin/security", "delete-generic-password", "-s", m.keychainService(id), "-a", account).Run()
}
