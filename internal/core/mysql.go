package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// databaseService is an extra service that holds databases and users.
type databaseService interface {
	extraService
	databases(m *Manager) ([]string, error)
	createDatabase(m *Manager, name string) error
	createUser(m *Manager, user, password, database string) error
	setRootPassword(m *Manager, password string) error
	shell(m *Manager) error
	// flyenvDataDir is the FlyEnv data folder for this version, or "".
	flyenvDataDir() string
}

var dbNameRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// mysqlService runs MySQL, or MariaDB when maria is true. The two are
// close: they differ in binary names, the data folder setup and one option.
type mysqlService struct {
	key     string // "8.4"; empty for the plain formula (the newest version)
	label   string // the version to show, for example "8.4" or "26.10"
	formula string // "mysql@8.4", "mysql" or "mariadb@11.4"
	maria   bool
}

func (s *mysqlService) ID() string {
	id := "mysql"
	if s.maria {
		id = "mariadb"
	}
	if s.key != "" {
		id += "-" + s.key
	}
	return id
}

func (s *mysqlService) Name() string {
	name := "MySQL"
	if s.maria {
		name = "MariaDB"
	}
	if s.label != "" {
		name += " " + s.label
	}
	return name
}

// server and client are the binary names.
func (s *mysqlService) server() string {
	if s.maria {
		return "mariadbd"
	}
	return "mysqld"
}

func (s *mysqlService) client() string {
	if s.maria {
		return "mariadb"
	}
	return "mysql"
}
func (s *mysqlService) Kind() string    { return "database" }
func (s *mysqlService) Formula() string { return s.formula }
func (s *mysqlService) Installed() bool {
	return exists(filepath.Join(brewPrefix(s.formula), "bin", s.server()))
}
func (s *mysqlService) Version() string  { return brewVersion(s.formula) }
func (s *mysqlService) DefaultPort() int { return 3306 }

// account names the Keychain item for the root user of the current data
// folder, so each data folder keeps its own password.
func (s *mysqlService) account(m *Manager) string { return "root " + s.dataDir(m) }

func (s *mysqlService) bin(name string) string {
	return filepath.Join(brewPrefix(s.formula), "bin", name)
}

// tmpName adds a short hash of the home to names in /tmp, for a Benchtop
// home that is not the default one, so two homes do not share a socket.
func (m *Manager) tmpName(base string) string {
	if m.Home == defaultHomePath() {
		return base
	}
	sum := sha256.Sum256([]byte(m.Home))
	return base + "-" + hex.EncodeToString(sum[:3])
}

func (s *mysqlService) socket(m *Manager) string {
	return "/tmp/" + m.tmpName("benchtop-"+strings.ReplaceAll(s.ID(), ".", "")) + ".sock"
}

func (s *mysqlService) dataDir(m *Manager) string {
	if d := m.serviceSettings(s.ID()).DataDir; d != "" {
		return d
	}
	return filepath.Join(m.extraDir(s), "data")
}

func (s *mysqlService) flyenvDataDir() string {
	if s.maria {
		return ""
	}
	d := filepath.Join(FlyEnvHome(), "mysql", "data-"+majorMinor(s.Version()))
	if exists(filepath.Join(d, "mysql")) {
		return d
	}
	return ""
}

func (s *mysqlService) cnf(m *Manager) string    { return filepath.Join(m.extraDir(s), "my.cnf") }
func (s *mysqlService) custom(m *Manager) string { return filepath.Join(m.extraDir(s), "custom.cnf") }
func (s *mysqlService) marker(m *Manager) string { return filepath.Join(m.extraDir(s), ".new-root") }

func (s *mysqlService) prepare(m *Manager) error {
	data := s.dataDir(m)
	if err := checkDataDirFree(data); err != nil {
		return err
	}
	if !exists(s.custom(m)) {
		if err := os.WriteFile(s.custom(m), []byte("# Your MySQL settings. Restart MySQL after you save.\n[mysqld]\n"), 0o644); err != nil {
			return err
		}
	}
	port := m.servicePortOrDefault(s)
	cnf := fmt.Sprintf(`# Created by Benchtop. Benchtop replaces this file on each start.
# Put your own settings in custom.cnf.
[mysqld]
datadir = %s
port = %d
bind-address = 127.0.0.1
socket = %s
pid-file = %s
%s

[client]
port = %d
socket = %s

!include %s
`, data, port, s.socket(m), filepath.Join(m.extraDir(s), "mysqld.pid"), s.extraOption(), port, s.socket(m), s.custom(m))
	if err := os.WriteFile(s.cnf(m), []byte(cnf), 0o644); err != nil {
		return err
	}
	if exists(filepath.Join(data, "mysql")) {
		return nil
	}
	if m.serviceSettings(s.ID()).Source == "flyenv" {
		return fmt.Errorf("the FlyEnv data folder %s has no MySQL data", data)
	}
	// A new data folder: root has no password until afterStart sets one.
	if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
		return err
	}
	init := exec.Command(s.bin("mysqld"), "--defaults-file="+s.cnf(m), "--initialize-insecure")
	if s.maria {
		// "normal" gives root a password login, like MySQL, instead of a login
		// by macOS user name.
		init = exec.Command(s.bin("mariadb-install-db"), "--defaults-file="+s.cnf(m), "--auth-root-authentication-method=normal")
	}
	out, err := init.CombinedOutput()
	appendLog(m.extraLog(s.ID()), "initialize data folder", out)
	if err != nil {
		return fmt.Errorf("could not make the data folder: %s", lastLines(strings.TrimSpace(string(out)), 8))
	}
	return os.WriteFile(s.marker(m), nil, 0o600)
}

func appendLog(path, title string, out []byte) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- %s: %s\n%s", time.Now().Format(time.RFC3339), title, out)
}

// checkDataDirFree stops a start when another database server already uses
// the data folder. Two servers on one data folder can damage the data.
func checkDataDirFree(dir string) error {
	out, _ := exec.Command("/bin/ps", "-axo", "pid=,command=").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "mysqld") && !strings.Contains(line, "postgres") {
			continue
		}
		if strings.Contains(line, dir) {
			return fmt.Errorf("another server already uses %s:\n%s", dir, strings.TrimSpace(line))
		}
	}
	// FlyEnv gives the data folder in its config file, not on the command line.
	if strings.HasPrefix(dir, FlyEnvHome()) {
		for _, pidFile := range []string{filepath.Join(FlyEnvHome(), "mysql", "mysql.pid"), filepath.Join(dir, "postmaster.pid")} {
			if pid := readPID(pidFile); pidAlive(pid) {
				return fmt.Errorf("FlyEnv still runs a server on %s (pid %d). Stop it in FlyEnv first", dir, pid)
			}
		}
	}
	return nil
}

func (s *mysqlService) command(m *Manager) (string, []string) {
	return s.bin(s.server()), []string{"--defaults-file=" + s.cnf(m)}
}

func (s *mysqlService) ready(m *Manager) bool {
	conn, err := net.DialTimeout("unix", s.socket(m), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (s *mysqlService) afterStart(m *Manager) error {
	if !exists(s.marker(m)) {
		return nil
	}
	pw := randomPassword()
	if _, err := s.query(m, "", fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED BY %s;", sqlString(pw))); err != nil {
		return fmt.Errorf("set the root password: %w", err)
	}
	if err := m.savePassword(s.ID(), s.account(m), pw); err != nil {
		return err
	}
	return os.Remove(s.marker(m))
}

// query runs SQL as root. With password "", it uses the Keychain password.
func (s *mysqlService) query(m *Manager, password, sql string) (string, error) {
	if password == "" && !exists(s.marker(m)) {
		pw, ok := m.loadPassword(s.ID(), s.account(m))
		if !ok {
			return "", errors.New("Benchtop does not know the root password. Use \"Set root password\" first")
		}
		password = pw
	}
	creds := m.path("tmp", s.ID()+"-client.cnf")
	content := fmt.Sprintf("[client]\nuser=root\npassword=%q\nsocket=%s\n", password, s.socket(m))
	if err := os.WriteFile(creds, []byte(content), 0o600); err != nil {
		return "", err
	}
	defer os.Remove(creds)
	cmd := exec.Command(s.bin(s.client()), "--defaults-extra-file="+creds, "--batch", "--skip-column-names")
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(text, "Access denied") {
			return "", errors.New("MySQL refused the root password. Use \"Set root password\" with the right one")
		}
		return "", errors.New(lastLines(text, 5))
	}
	return text, nil
}

var mysqlSystemDBs = map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true}

func (s *mysqlService) databases(m *Manager) ([]string, error) {
	out, err := s.query(m, "", "SHOW DATABASES;")
	if err != nil {
		return nil, err
	}
	dbs := []string{}
	for _, line := range strings.Split(out, "\n") {
		if line != "" && !mysqlSystemDBs[line] {
			dbs = append(dbs, line)
		}
	}
	return dbs, nil
}

func (s *mysqlService) createDatabase(m *Manager, name string) error {
	_, err := s.query(m, "", fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", name))
	return err
}

func (s *mysqlService) createUser(m *Manager, user, password, database string) error {
	sql := fmt.Sprintf("CREATE USER IF NOT EXISTS %s@'%%' IDENTIFIED BY %s;\n", sqlString(user), sqlString(password))
	if database != "" {
		sql += fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO %s@'%%';\n", database, sqlString(user))
	}
	_, err := s.query(m, "", sql)
	return err
}

func (s *mysqlService) setRootPassword(m *Manager, password string) error {
	if _, err := s.query(m, password, "SELECT 1;"); err != nil {
		return errors.New("MySQL refused this password")
	}
	return m.savePassword(s.ID(), s.account(m), password)
}

func (s *mysqlService) shell(m *Manager) error {
	pw, _ := m.loadPassword(s.ID(), s.account(m))
	creds := m.path("tmp", s.ID()+"-shell.cnf")
	content := fmt.Sprintf("[client]\nuser=root\npassword=%q\nsocket=%s\n", pw, s.socket(m))
	if err := os.WriteFile(creds, []byte(content), 0o600); err != nil {
		return err
	}
	return m.openInTerminal(s.ID()+"-shell", s.bin(s.client()), "--defaults-extra-file="+creds)
}

func (s *mysqlService) detail(m *Manager) string {
	return fmt.Sprintf("port %d, data %s", m.servicePortOrDefault(s), shortPath(s.dataDir(m)))
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func (s *mysqlService) files(m *Manager) []ConfigFile {
	return []ConfigFile{{Label: "custom.cnf", Path: s.custom(m), Note: "Restart " + s.Name() + " after you save."}}
}

func (s *mysqlService) links(m *Manager) []Link { return nil }

func (s *mysqlService) env(m *Manager) string {
	pw, _ := m.loadPassword(s.ID(), s.account(m))
	return fmt.Sprintf("DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=\nDB_USERNAME=root\nDB_PASSWORD=%s\n", m.servicePortOrDefault(s), pw)
}

// extraOption turns off the MySQL X plugin, which MariaDB does not have.
func (s *mysqlService) extraOption() string {
	if s.maria {
		return ""
	}
	return "mysqlx = OFF"
}
