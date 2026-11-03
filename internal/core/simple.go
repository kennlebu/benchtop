package core

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// simpleService describes a service with data only, so a new service of
// this kind needs no new code. Benchtop runs the binary in the foreground,
// as a child process.
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
	// started runs once the service answers, for example to make a bucket.
	started func(port int) error
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

func (s *simpleService) afterStart(m *Manager) error {
	if s.started == nil {
		return nil
	}
	return s.started(m.servicePortOrDefault(s))
}

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

// simpleServices are the services that have one version. Redis and MongoDB
// have many versions, so families() lists them.
func simpleServices() []extraService {
	return []extraService{
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
			id: "valkey", name: "Valkey", kind: "database", formula: "valkey", binary: "bin/valkey-server", port: 6379,
			// Valkey is the open-source Redis. Laravel's Redis settings work unchanged.
			args: func(dir string, port int) []string {
				return []string{"--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--dir", dir, "--daemonize", "no"}
			},
			envText: func(port int) string {
				return fmt.Sprintf("REDIS_CLIENT=phpredis\nREDIS_HOST=127.0.0.1\nREDIS_PORT=%d\nREDIS_PASSWORD=null\n", port)
			},
			shellArgv: func(prefix string, port int) []string {
				return []string{filepath.Join(prefix, "bin", "valkey-cli"), "-p", fmt.Sprint(port)}
			},
		},
		&simpleService{
			id: "meilisearch", name: "Meilisearch", kind: "database", formula: "meilisearch", binary: "bin/meilisearch", port: 7700,
			args: func(dir string, port int) []string {
				return []string{"--db-path", filepath.Join(dir, "data.ms"), "--dump-dir", filepath.Join(dir, "dumps"),
					"--http-addr", fmt.Sprintf("127.0.0.1:%d", port), "--env", "development", "--no-analytics"}
			},
			linkList: func(port int) []Link {
				return []Link{{Label: "Open", URL: fmt.Sprintf("http://127.0.0.1:%d", port)}}
			},
			envText: func(port int) string {
				return fmt.Sprintf("SCOUT_DRIVER=meilisearch\nMEILISEARCH_HOST=http://127.0.0.1:%d\nMEILISEARCH_KEY=\n", port)
			},
		},
		&simpleService{
			id: "typesense", name: "Typesense", kind: "database", formula: "typesense/tap/typesense-server",
			prefixName: "typesense-server", binary: "bin/typesense-server", port: 8108,
			// The API key only guards a server on 127.0.0.1, so a fixed key is fine.
			args: func(dir string, port int) []string {
				data := filepath.Join(dir, "data")
				os.MkdirAll(data, 0o755)
				return []string{"--data-dir", data, "--api-key", "benchtop", "--api-address", "127.0.0.1",
					"--api-port", fmt.Sprint(port), "--enable-cors"}
			},
			envText: func(port int) string {
				return fmt.Sprintf("SCOUT_DRIVER=typesense\nTYPESENSE_API_KEY=benchtop\nTYPESENSE_HOST=127.0.0.1\nTYPESENSE_PORT=%d\nTYPESENSE_PROTOCOL=http\n", port)
			},
		},
		&simpleService{
			id: "seaweedfs", name: "SeaweedFS (S3)", kind: "database", formula: "seaweedfs", binary: "bin/weed", port: 8333,
			// One process runs the master, a volume, the filer and the S3 API.
			// The other parts use the ports after the S3 port.
			args: func(dir string, port int) []string {
				data := filepath.Join(dir, "data")
				os.MkdirAll(data, 0o755)
				return []string{"server", "-dir", data, "-ip", "127.0.0.1", "-ip.bind", "127.0.0.1",
					"-master.port", fmt.Sprint(port + 1), "-volume.port", fmt.Sprint(port + 2),
					"-filer", "-filer.port", fmt.Sprint(port + 3), "-s3", "-s3.port", fmt.Sprint(port)}
			},
			detailText: func(port int) string {
				return fmt.Sprintf("S3 port %d, bucket \"local\"", port)
			},
			// With no S3 users set, SeaweedFS accepts any key. Make the bucket that
			// the .env lines name.
			started: func(port int) error {
				req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/local", port), nil)
				resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
				if err != nil {
					return err
				}
				resp.Body.Close()
				return nil
			},
			envText: func(port int) string {
				return fmt.Sprintf("FILESYSTEM_DISK=s3\nAWS_ACCESS_KEY_ID=benchtop\nAWS_SECRET_ACCESS_KEY=benchtop\nAWS_DEFAULT_REGION=us-east-1\nAWS_BUCKET=local\nAWS_ENDPOINT=http://127.0.0.1:%d\nAWS_USE_PATH_STYLE_ENDPOINT=true\n", port)
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
				return fmt.Sprintf("SMTP port %d, inbox port %d", port, port+7000)
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

func redisService() *simpleService {
	return &simpleService{
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
	}
}

func mongoService() *simpleService {
	return &simpleService{
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
	}
}

// simpleVersion makes the service for one version of a family.
func simpleVersion(base *simpleService, f familyFormula) *simpleService {
	s := *base
	s.id, s.formula, s.prefixName = f.id, f.formula, ""
	if f.opt != f.formula {
		s.prefixName = f.opt
	}
	if f.label != "" {
		s.name = base.name + " " + f.label
	}
	return &s
}
