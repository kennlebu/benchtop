package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FlyEnvHome is where FlyEnv (formerly PhpWebStudy) keeps its data.
func FlyEnvHome() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "PhpWebStudy", "server")
}

var (
	flyRootRe = regexp.MustCompile(`^\s*root\s+\*\s+"?([^"]+)"?\s*$`)
	flyPHPRe  = regexp.MustCompile(`enable-php-select\s+(\d)(\d+)`)
)

// ImportFlyEnv copies Caddy sites and FTP users from FlyEnv. It skips
// sites and users that already exist. It returns a report, line by line.
func (m *Manager) ImportFlyEnv(flyHome string) ([]string, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	var report []string
	files, err := filepath.Glob(filepath.Join(flyHome, "vhost", "caddy", "*.conf"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		report = append(report, "no Caddy sites found in "+filepath.Join(flyHome, "vhost", "caddy"))
	}
	for _, f := range files {
		s, skipped, err := parseFlyEnvCaddy(f)
		for _, d := range skipped {
			report = append(report, fmt.Sprintf("%s: skipped domain %q (not valid)", filepath.Base(f), d))
		}
		if err != nil {
			report = append(report, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		if m.siteWithDomain(s.Domains[0]) {
			report = append(report, fmt.Sprintf("site %s: already exists, skipped", s.Domains[0]))
			continue
		}
		saved, warnings, err := m.saveSite(s)
		if err != nil {
			report = append(report, fmt.Sprintf("site %s: %v", s.Domains[0], err))
			continue
		}
		report = append(report, fmt.Sprintf("site %s: imported (root %s, PHP %s)", strings.Join(saved.Domains, ", "), saved.Root, orStatic(saved.PHP)))
		for _, w := range warnings {
			report = append(report, "  warning: "+w)
		}
	}

	for _, w := range m.applySites() {
		report = append(report, "warning: "+w)
	}

	ftpReport, err := m.importFlyEnvFTP(filepath.Join(flyHome, "ftp", "pureftpd.passwd"))
	if err != nil {
		report = append(report, "ftp: "+err.Error())
	}
	return append(report, ftpReport...), nil
}

func orStatic(php string) string {
	if php == "" {
		return "none"
	}
	return php
}

func (m *Manager) siteWithDomain(d string) bool {
	for _, s := range m.Config().Sites {
		if contains(s.Domains, d) {
			return true
		}
	}
	return false
}

func parseFlyEnvCaddy(path string) (Site, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Site{}, nil, err
	}
	var s Site
	var skipped []string
	header := true
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if header {
			addrPart := line
			if i := strings.Index(line, "{"); i >= 0 {
				addrPart = line[:i]
				header = false
			}
			for _, a := range strings.Split(addrPart, ",") {
				a = strings.TrimSpace(a)
				if strings.HasPrefix(a, "https://") {
					s.HTTPS = true
				}
				d := strings.TrimPrefix(strings.TrimPrefix(a, "http://"), "https://")
				if d == "" {
					continue
				}
				if strings.Contains(d, " ") || !domainRe.MatchString(d) {
					skipped = append(skipped, d)
					continue
				}
				s.Domains = append(s.Domains, d)
			}
			continue
		}
		if mm := flyRootRe.FindStringSubmatch(line); mm != nil {
			s.Root = mm[1]
		}
		if mm := flyPHPRe.FindStringSubmatch(line); mm != nil {
			s.PHP = mm[1] + "." + mm[2]
		}
		if strings.HasPrefix(line, "tls ") {
			s.HTTPS = true
		}
	}
	if len(s.Domains) == 0 {
		return s, skipped, fmt.Errorf("no valid domain")
	}
	if s.Root == "" {
		return s, skipped, fmt.Errorf("no root folder")
	}
	return s, skipped, nil
}

// importFlyEnvFTP copies password lines (with hashes) from FlyEnv, so the
// users keep their passwords.
func (m *Manager) importFlyEnvFTP(passwdPath string) ([]string, error) {
	data, err := os.ReadFile(passwdPath)
	if os.IsNotExist(err) {
		return []string{"no FTP users found in FlyEnv"}, nil
	}
	if err != nil {
		return nil, err
	}
	existing, _ := m.FTPUsers()
	have := map[string]bool{}
	for _, u := range existing {
		have[u.User] = true
	}
	var report, lines []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 6 || parts[0] == "" {
			continue
		}
		if have[parts[0]] {
			report = append(report, fmt.Sprintf("ftp user %s: already exists, skipped", parts[0]))
			continue
		}
		lines = append(lines, line)
		report = append(report, fmt.Sprintf("ftp user %s: imported (folder %s)", parts[0], strings.TrimSuffix(parts[5], "/./")))
	}
	if len(lines) == 0 {
		return report, nil
	}
	f, err := os.OpenFile(m.ftpPasswd(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(strings.Join(lines, "\n") + "\n")
	f.Close()
	if err != nil {
		return nil, err
	}
	return report, m.rebuildFTPDB()
}
