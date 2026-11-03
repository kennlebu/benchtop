package core

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Backup holds what another Mac needs to rebuild the setup: the config and
// the FTP users (with password hashes). It has no database data and no
// Keychain passwords.
type Backup struct {
	Format    int       `json:"benchtopBackup"`
	Created   time.Time `json:"created"`
	Config    Config    `json:"config"`
	FTPPasswd string    `json:"ftpPasswd,omitempty"`
}

func (m *Manager) MakeBackup() ([]byte, error) {
	passwd, _ := os.ReadFile(m.ftpPasswd())
	b := Backup{Format: 1, Created: time.Now(), Config: m.Config(), FTPPasswd: string(passwd)}
	return json.MarshalIndent(b, "", "  ")
}

// RestoreBackup adds the sites and FTP users from a backup. It skips sites
// with a domain that is already in use, and users that already exist. It
// does not change the settings of this Mac.
func (m *Manager) RestoreBackup(data []byte) ([]string, error) {
	var b Backup
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("this is not a Benchtop backup: %w", err)
	}
	if b.Format != 1 {
		return nil, fmt.Errorf("this is not a Benchtop backup, or it is from a newer version")
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	var report []string
	added := 0
	for _, s := range b.Config.Sites {
		if m.siteWithDomain(s.Domains[0]) {
			report = append(report, fmt.Sprintf("site %s: already exists, skipped", s.Domains[0]))
			continue
		}
		s.ID = "" // a new ID that is free on this Mac
		saved, warnings, err := m.saveSite(s)
		if err != nil {
			report = append(report, fmt.Sprintf("site %s: %v", s.Domains[0], err))
			continue
		}
		added++
		report = append(report, fmt.Sprintf("site %s: added", saved.Domains[0]))
		for _, w := range warnings {
			report = append(report, "  warning: "+w)
		}
	}
	if added > 0 {
		for _, w := range m.applySites() {
			report = append(report, "warning: "+w)
		}
	}
	if strings.TrimSpace(b.FTPPasswd) != "" {
		tmp := m.path("tmp", "restore.passwd")
		if err := os.WriteFile(tmp, []byte(b.FTPPasswd), 0o600); err != nil {
			return report, err
		}
		defer os.Remove(tmp)
		ftp, err := m.importFlyEnvFTP(tmp)
		if err != nil {
			report = append(report, "ftp: "+err.Error())
		}
		report = append(report, ftp...)
	}
	if len(report) == 0 {
		report = append(report, "The backup has no sites and no FTP users.")
	}
	return report, nil
}
