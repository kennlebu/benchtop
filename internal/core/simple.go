package core

import (
	"fmt"
	"os"
	"path/filepath"
)

// simpleService describes a service with data only, so a new service of
// this kind needs no new code (RFC 0001, section 7.3). Benchtop runs the
// binary in the foreground, as a child process.
type simpleService struct {
	id, name, kind string
	formula        string
	// prefixName is the folder in Homebrew's opt, when it is not the formula
	// name (for a formula from a tap such as mongodb/brew/mongodb-community).
	prefixName string
	binary     string // relative to the Homebrew prefix
	port       int
	// config makes a config file in dir. It returns "" when there is none.
	config func(dir string, port int) (name, content string)
	// custom is a settings file that Benchtop makes once and never changes.
	custom     string
	customText string
	args       func(dir string, port int) []string
	readyPort  func(port int) int
	envText    func(port int) string
	linkList   func(port int) []Link
	shellArgv  func(prefix string, port int) []string
	detailText func(port int) string
}

func (s *simpleService) prefix() string {
	if s.prefixName != "" {
		return brewPrefix(s.prefixName)
	}
	return brewPrefix(s.formula)
}

func (s *simpleService) ID() string       { return s.id }
func (s *simpleService) Name() string     { return s.name }
func (s *simpleService) Kind() string     { return s.kind }
func (s *simpleService) Formula() string  { return s.formula }
func (s *simpleService) DefaultPort() int { return s.port }
func (s *simpleService) Installed() bool {
	return s.prefix() != "" && exists(filepath.Join(s.prefix(), s.binary))
}
func (s *simpleService) Version() string {
	if s.prefixName != "" {
		return brewVersion(s.prefixName)
	}
	return brewVersion(s.formula)
}

func (s *simpleService) prepare(m *Manager) error {
	dir := m.extraDir(s)
	if s.custom != "" && !exists(filepath.Join(dir, s.custom)) {
		if err := os.WriteFile(filepath.Join(dir, s.custom), []byte(s.customText), 0o644); err != nil {
			return err
		}
	}
	if s.config == nil {
		return nil
	}
	name, content := s.config(dir, m.servicePortOrDefault(s))
	if name == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func (s *simpleService) command(m *Manager) (string, []string) {
	return filepath.Join(s.prefix(), s.binary), s.args(m.extraDir(s), m.servicePortOrDefault(s))
}

func (s *simpleService) ready(m *Manager) bool {
	port := m.servicePortOrDefault(s)
	if s.readyPort != nil {
		port = s.readyPort(port)
	}
	return dialTCP(port)
}

func (s *simpleService) afterStart(m *Manager) error { return nil }

func (s *simpleService) detail(m *Manager) string {
	if s.detailText != nil {
		return s.detailText(m.servicePortOrDefault(s))
	}
	return fmt.Sprintf("port %d", m.servicePortOrDefault(s))
}

func (s *simpleService) files(m *Manager) []ConfigFile {
	if s.custom == "" {
		return nil
	}
	return []ConfigFile{{Label: s.custom, Path: filepath.Join(m.extraDir(s), s.custom), Note: "Restart " + s.name + " after you save."}}
}

func (s *simpleService) links(m *Manager) []Link {
	if s.linkList == nil {
		return nil
	}
	return s.linkList(m.servicePortOrDefault(s))
}

func (s *simpleService) env(m *Manager) string {
	if s.envText == nil {
		return ""
	}
	return s.envText(m.servicePortOrDefault(s))
}

func (s *simpleService) hasShell() bool { return s.shellArgv != nil }

func (s *simpleService) shell(m *Manager) error {
	if s.shellArgv == nil {
		return fmt.Errorf("%s has no shell", s.name)
	}
	return m.openInTerminal(s.id+"-shell", s.shellArgv(s.prefix(), m.servicePortOrDefault(s))...)
}

// simpleServices are the services that the data below describes.
func simpleServices() []extraService {
	return []extraService{
		&simpleService{
			id: "redis", name: "Redis", kind: "database", formula: "redis", binary: "bin/redis-server", port: 6379,
			custom: "custom.conf", customText: "# Your Redis settings. Restart Redis after you save.\n",
			config: func(dir string, port int) (string, string) {
				return "redis.conf", fmt.Sprintf(`# Created by Benchtop. Benchtop replaces this file on each start.
# Put your own settings in custom.conf.
port %d
bind 127.0.0.1 -::1
protected-mode yes
daemonize no
dir %s
logfile ""
include %s
`, port, dir, filepath.Join(dir, "custom.conf"))
			},
			args: func(dir string, port int) []string { return []string{filepath.Join(dir, "redis.conf")} },
			envText: func(port int) string {
				return fmt.Sprintf("REDIS_HOST=127.0.0.1\nREDIS_PORT=%d\nREDIS_PASSWORD=null\n", port)
			},
			shellArgv: func(prefix string, port int) []string {
				return []string{filepath.Join(prefix, "bin", "redis-cli"), "-p", fmt.Sprint(port)}
			},
		},
		&simpleService{
			id: "memcached", name: "Memcached", kind: "database", formula: "memcached", binary: "bin/memcached", port: 11211,
			args: func(dir string, port int) []string {
				return []string{"-l", "127.0.0.1", "-p", fmt.Sprint(port), "-m", "128"}
			},
			envText: func(port int) string {
				return fmt.Sprintf("CACHE_STORE=memcached\nMEMCACHED_HOST=127.0.0.1\nMEMCACHED_PORT=%d\n", port)
			},
		},
		&simpleService{
			id: "mongodb", name: "MongoDB", kind: "database", formula: "mongodb/brew/mongodb-community",
			prefixName: "mongodb-community", binary: "bin/mongod", port: 27017,
			args: func(dir string, port int) []string {
				data := filepath.Join(dir, "data")
				os.MkdirAll(data, 0o755)
				return []string{"--dbpath", data, "--port", fmt.Sprint(port), "--bind_ip", "127.0.0.1"}
			},
			envText: func(port int) string {
				return fmt.Sprintf("MONGODB_URI=mongodb://127.0.0.1:%d\n", port)
			},
			shellArgv: func(prefix string, port int) []string {
				return []string{"mongosh", "--port", fmt.Sprint(port)}
			},
		},
		&simpleService{
			id: "mailpit", name: "Mailpit", kind: "mail", formula: "mailpit", binary: "bin/mailpit", port: 1025,
			// The web inbox port is 8025 for the usual SMTP port 1025.
			args: func(dir string, port int) []string {
				return []string{"--smtp", fmt.Sprintf("127.0.0.1:%d", port), "--listen", fmt.Sprintf("127.0.0.1:%d", port+7000),
					"--database", filepath.Join(dir, "mailpit.db")}
			},
			readyPort: func(port int) int { return port + 7000 },
			detailText: func(port int) string {
				return fmt.Sprintf("SMTP port %d · inbox port %d", port, port+7000)
			},
			linkList: func(port int) []Link {
				return []Link{{Label: "Open inbox", URL: fmt.Sprintf("http://127.0.0.1:%d", port+7000)}}
			},
			envText: func(port int) string {
				return fmt.Sprintf("MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nMAIL_PORT=%d\nMAIL_USERNAME=null\nMAIL_PASSWORD=null\nMAIL_ENCRYPTION=null\n", port)
			},
		},
	}
}
