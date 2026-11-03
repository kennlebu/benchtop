package core

import (
	"errors"
	"fmt"
)

func (m *Manager) database(id string) (databaseService, error) {
	s, ok := findExtra(id)
	if !ok {
		return nil, fmt.Errorf("unknown service %q", id)
	}
	db, ok := s.(databaseService)
	if !ok {
		return nil, fmt.Errorf("%s has no databases", s.Name())
	}
	if _, running := m.extraRunning(id); !running {
		return nil, fmt.Errorf("start %s first", s.Name())
	}
	return db, nil
}

// Databases lists the user databases of a database service.
func (m *Manager) Databases(id string) ([]string, error) {
	db, err := m.database(id)
	if err != nil {
		return nil, err
	}
	return db.databases(m)
}

func (m *Manager) CreateDatabase(id, name string) error {
	if !dbNameRe.MatchString(name) {
		return errors.New("a database name can only have letters, numbers and underscores")
	}
	db, err := m.database(id)
	if err != nil {
		return err
	}
	return db.createDatabase(m, name)
}

func (m *Manager) CreateDatabaseUser(id, user, password, database string) error {
	if !dbNameRe.MatchString(user) {
		return errors.New("a user name can only have letters, numbers and underscores")
	}
	if password == "" {
		return errors.New("password is required")
	}
	if database != "" && !dbNameRe.MatchString(database) {
		return errors.New("the database name is not valid")
	}
	db, err := m.database(id)
	if err != nil {
		return err
	}
	return db.createUser(m, user, password, database)
}

// SetRootPassword checks the password against the running server, then
// stores it in the Keychain.
func (m *Manager) SetRootPassword(id, password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	db, err := m.database(id)
	if err != nil {
		return err
	}
	return db.setRootPassword(m, password)
}

// OpenShell opens the database client (or redis-cli) in the terminal app.
func (m *Manager) OpenShell(id string) error {
	s, ok := findExtra(id)
	if !ok {
		return fmt.Errorf("unknown service %q", id)
	}
	sh, ok := s.(interface{ shell(*Manager) error })
	if simple, isSimple := s.(*simpleService); isSimple && !simple.hasShell() {
		ok = false
	}
	if !ok {
		return fmt.Errorf("%s has no shell", s.Name())
	}
	if _, running := m.extraRunning(id); !running {
		return fmt.Errorf("start %s first", s.Name())
	}
	return sh.shell(m)
}

// ServiceEnv returns .env lines for a Laravel app that uses the service.
func (m *Manager) ServiceEnv(id string) (string, error) {
	s, ok := findExtra(id)
	if !ok {
		return "", fmt.Errorf("unknown service %q", id)
	}
	return s.env(m), nil
}

// FlyEnvData returns the FlyEnv data folder that a database service can
// use in place, or "".
func FlyEnvData(id string) string {
	s, ok := findExtra(id)
	if !ok {
		return ""
	}
	if db, ok := s.(databaseService); ok {
		return db.flyenvDataDir()
	}
	return ""
}
