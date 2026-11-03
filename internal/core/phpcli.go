package core

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// PHPForDir picks the PHP version for a folder: the version of the site
// that holds the folder, then composer.json, then the newest version.
// It returns the version and the reason, for "benchtop which".
func (m *Manager) PHPForDir(dir string) (PHPVersion, string, error) {
	dir = filepath.Clean(dir)
	installed := DiscoverPHP()
	if len(installed) == 0 {
		return PHPVersion{}, "", fmt.Errorf("no PHP version is installed. Run: brew install php")
	}
	var best Site
	bestLen := -1
	for _, s := range m.Config().Sites {
		if s.PHP == "" {
			continue
		}
		for _, root := range []string{s.Root, filepath.Dir(s.Root)} {
			// A site root is often public/ inside the project folder.
			if root == filepath.Dir(s.Root) && filepath.Base(s.Root) != "public" {
				continue
			}
			if (dir == root || strings.HasPrefix(dir, root+"/")) && len(root) > bestLen {
				best, bestLen = s, len(root)
			}
		}
	}
	if bestLen >= 0 {
		if v, ok := findPHP(best.PHP); ok {
			return v, fmt.Sprintf("site %s uses PHP %s", best.Domains[0], best.PHP), nil
		}
		return PHPVersion{}, "", fmt.Errorf("site %s uses PHP %s, which is not installed", best.Domains[0], best.PHP)
	}
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, "composer.json")) {
			key, note := suggestPHP(d, d, installed)
			if v, ok := findPHP(key); ok {
				return v, note, nil
			}
			break
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	v := installed[len(installed)-1]
	return v, "no site or composer.json here, so the newest PHP", nil
}

// PHPCommand builds the command for "benchtop php" or "benchtop composer".
func (m *Manager) PHPCommand(dir, tool string, args []string) (*exec.Cmd, error) {
	v, _, err := m.PHPForDir(dir)
	if err != nil {
		return nil, err
	}
	php := phpBin(v, "php")
	switch tool {
	case "php":
		return exec.Command(php, args...), nil
	case "composer":
		composer := findBinary("", "/opt/homebrew/bin/composer", "/usr/local/bin/composer", "composer")
		if composer == "" {
			return nil, fmt.Errorf("composer is not installed. Run: brew install composer")
		}
		return exec.Command(php, append([]string{composer}, args...)...), nil
	}
	return nil, fmt.Errorf("unknown tool %q", tool)
}
