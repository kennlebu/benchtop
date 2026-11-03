package core

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func dialTCP(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ---------- PostgreSQL ----------

type postgresService struct {
	key     string // "17"
	formula string // "postgresql@17"
}

func postgresServices() []extraService {
	var out []extraService
	for _, k := range []string{"17", "16", "15", "14"} {
		out = append(out, &postgresService{key: k, formula: "postgresql@" + k})
	}
	return out
}

func (s *postgresService) ID() string       { return "postgres-" + s.key }
func (s *postgresService) Name() string     { return "PostgreSQL " + s.key }
func (s *postgresService) Kind() string     { return "database" }
func (s *postgresService) Formula() string  { return s.formula }
func (s *postgresService) Installed() bool  { return exists(s.bin("postgres")) }
func (s *postgresService) Version() string  { return brewVersion(s.formula) }
func (s *postgresService) DefaultPort() int { return 5432 }

func (s *postgresService) account(m *Manager) string { return "postgres " + s.dataDir(m) }

func (s *postgresService) bin(name string) string {
	return filepath.Join(brewPrefix(s.formula), "bin", name)
}

func (s *postgresService) dataDir(m *Manager) string {
	if d := m.serviceSettings(s.ID()).DataDir; d != "" {
		return d
	}
	return filepath.Join(m.extraDir(s), "data")
}

func (s *postgresService) flyenvDataDir() string {
	d := filepath.Join(FlyEnvHome(), "postgresql", "postgresql"+s.key)
	if exists(filepath.Join(d, "PG_VERSION")) {
		return d
	}
	return ""
}

// socketDir holds the PostgreSQL socket. The name has a fixed short path,
// because socket paths must be short.
func (s *postgresService) socketDir(m *Manager) string {
	return "/tmp/" + m.tmpName("benchtop-pg"+s.key)
}

func (s *postgresService) prepare(m *Manager) error {
	data := s.dataDir(m)
	if err := checkDataDirFree(data); err != nil {
		return err
	}
	if err := os.MkdirAll(s.socketDir(m), 0o700); err != nil {
		return err
	}
	if v, err := os.ReadFile(filepath.Join(data, "PG_VERSION")); err == nil {
		if got := strings.TrimSpace(string(v)); got != s.key {
			return fmt.Errorf("the data folder is for PostgreSQL %s, not %s", got, s.key)
		}
		return nil
	}
	if m.serviceSettings(s.ID()).Source == "flyenv" {
		return fmt.Errorf("the FlyEnv data folder %s has no PostgreSQL data", data)
	}
	pw := randomPassword()
	pwFile := m.path("tmp", s.ID()+"-initdb")
	if err := os.WriteFile(pwFile, []byte(pw+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(pwFile)
	// Local socket connections need no password. TCP connections (from
	// PHP apps) use the password, which goes in the Keychain.
	out, err := exec.Command(s.bin("initdb"), "-D", data, "-U", "postgres", "-E", "UTF8",
		"--auth-local=trust", "--auth-host=scram-sha-256", "--pwfile="+pwFile).CombinedOutput()
	appendLog(m.extraLog(s.ID()), "initdb", out)
	if err != nil {
		return fmt.Errorf("could not make the data folder: %s", lastLines(strings.TrimSpace(string(out)), 8))
	}
	return m.savePassword(s.ID(), s.account(m), pw)
}

func (s *postgresService) command(m *Manager) (string, []string) {
	return s.bin("postgres"), []string{"-D", s.dataDir(m), "-p", fmt.Sprint(m.servicePortOrDefault(s)),
		"-k", s.socketDir(m), "-c", "listen_addresses=127.0.0.1"}
}

func (s *postgresService) ready(m *Manager) bool {
	return exists(filepath.Join(s.socketDir(m), fmt.Sprintf(".s.PGSQL.%d", m.servicePortOrDefault(s)))) && dialTCP(m.servicePortOrDefault(s))
}

func (s *postgresService) afterStart(m *Manager) error { return nil }

// superuser is "postgres" for data that Benchtop made. Data from other
// tools may use the macOS user name instead.
func (s *postgresService) query(m *Manager, sql string) (string, error) {
	var lastErr error
	for _, user := range []string{"postgres", os.Getenv("USER")} {
		cmd := exec.Command(s.bin("psql"), "-h", s.socketDir(m), "-p", fmt.Sprint(m.servicePortOrDefault(s)),
			"-U", user, "-d", "postgres", "-At", "-v", "ON_ERROR_STOP=1")
		cmd.Stdin = strings.NewReader(sql)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		lastErr = errors.New(lastLines(strings.TrimSpace(string(out)), 5))
		if !strings.Contains(string(out), "does not exist") {
			break
		}
	}
	return "", lastErr
}

func (s *postgresService) databases(m *Manager) ([]string, error) {
	out, err := s.query(m, "SELECT datname FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY 1;")
	if err != nil {
		return nil, err
	}
	dbs := []string{}
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			dbs = append(dbs, line)
		}
	}
	return dbs, nil
}

func (s *postgresService) createDatabase(m *Manager, name string) error {
	dbs, err := s.databases(m)
	if err != nil {
		return err
	}
	if contains(dbs, name) {
		return nil
	}
	_, err = s.query(m, fmt.Sprintf(`CREATE DATABASE "%s" ENCODING 'UTF8';`, name))
	return err
}

func (s *postgresService) createUser(m *Manager, user, password, database string) error {
	pw := strings.ReplaceAll(password, "'", "''")
	sql := fmt.Sprintf(`DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE "%[1]s" LOGIN PASSWORD '%[2]s';
  ELSE ALTER ROLE "%[1]s" LOGIN PASSWORD '%[2]s'; END IF;
END $$;`, user, pw)
	if database != "" {
		// Since PostgreSQL 15, a user needs to own the database to create tables in it.
		sql += fmt.Sprintf("\nALTER DATABASE \"%s\" OWNER TO \"%s\";", database, user)
	}
	_, err := s.query(m, sql)
	return err
}

func (s *postgresService) setRootPassword(m *Manager, password string) error {
	pw := strings.ReplaceAll(password, "'", "''")
	if _, err := s.query(m, fmt.Sprintf("ALTER ROLE postgres PASSWORD '%s';", pw)); err != nil {
		return err
	}
	return m.savePassword(s.ID(), s.account(m), password)
}

func (s *postgresService) shell(m *Manager) error {
	return m.openInTerminal(s.ID()+"-shell", s.bin("psql"), "-h", s.socketDir(m), "-p", fmt.Sprint(m.servicePortOrDefault(s)), "-U", "postgres", "-d", "postgres")
}

func (s *postgresService) detail(m *Manager) string {
	return fmt.Sprintf("port %d, data %s", m.servicePortOrDefault(s), shortPath(s.dataDir(m)))
}

func (s *postgresService) files(m *Manager) []ConfigFile {
	return []ConfigFile{{Label: "postgresql.conf", Path: filepath.Join(s.dataDir(m), "postgresql.conf"), Note: "Restart " + s.Name() + " after you save."}}
}

func (s *postgresService) links(m *Manager) []Link { return nil }

func (s *postgresService) env(m *Manager) string {
	pw, _ := m.loadPassword(s.ID(), s.account(m))
	return fmt.Sprintf("DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=\nDB_USERNAME=postgres\nDB_PASSWORD=%s\n", m.servicePortOrDefault(s), pw)
}
