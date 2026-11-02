package core

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type FTPUser struct {
	User string `json:"user"`
	Dir  string `json:"dir"`
}

func (m *Manager) pureFTPdBin() string {
	return findBinary(m.Config().Bin.PureFTPd, "/opt/homebrew/sbin/pure-ftpd", "/usr/local/sbin/pure-ftpd", "pure-ftpd")
}

func (m *Manager) purePWBin() string {
	return findBinary(m.Config().Bin.PurePW, "/opt/homebrew/bin/pure-pw", "/usr/local/bin/pure-pw", "pure-pw")
}

func (m *Manager) ftpConf() string    { return m.path("ftp", "pure-ftpd.conf") }
func (m *Manager) ftpPasswd() string  { return m.path("ftp", "pureftpd.passwd") }
func (m *Manager) ftpPDB() string     { return m.path("ftp", "pureftpd.pdb") }
func (m *Manager) ftpPIDFile() string { return m.path("ftp", "pure-ftpd.pid") }

// FTPConfigPath is shown to the user. Benchtop writes it on each start.
func (m *Manager) FTPConfigPath() string { return m.ftpConf() }

func (m *Manager) ftpRunning() (int, bool) {
	pid := readPID(m.ftpPIDFile())
	return pid, pidAlive(pid)
}

func (m *Manager) writeFTPConf() error {
	cfg := m.Config().FTP
	content := fmt.Sprintf(`# Created by Benchtop. Benchtop replaces this file on each FTP start.
ChrootEveryone              yes
BrokenClientsCompatibility  no
MaxClientsNumber            50
Daemonize                   yes
MaxClientsPerIP             10
VerboseLog                  no
DisplayDotFiles             yes
AnonymousOnly               no
NoAnonymous                 yes
SyslogFacility              ftp
DontResolve                 yes
MaxIdleTime                 15
LimitRecursion              20000 8
AnonymousCanCreateDirs      no
MaxLoad                     4
AntiWarez                   yes
Umask                       133:022
MinUID                      100
AllowUserFXP                no
AllowAnonymousFXP           no
ProhibitDotFilesWrite       no
ProhibitDotFilesRead        no
AutoRename                  no
AnonymousCantUpload         yes
MaxDiskUsage                99
CustomerProof               yes
UnixAuthentication          no
PassivePortRange            %s
CreateHomeDir               no
PureDB                      %s
PIDFile                     %s
AltLog                      clf:%s
Bind                        0.0.0.0,%d
`, cfg.PassivePortRange, m.ftpPDB(), m.ftpPIDFile(), m.path("logs", "ftp-transfer.log"), cfg.Port)
	return os.WriteFile(m.ftpConf(), []byte(content), 0o644)
}

// ftpStartCmd returns the root shell command that starts pure-ftpd.
func (m *Manager) ftpStartCmd(force bool) (string, error) {
	bin := m.pureFTPdBin()
	if bin == "" {
		return "", errors.New("pure-ftpd is not installed. Run: brew install pure-ftpd")
	}
	if err := m.writeFTPConf(); err != nil {
		return "", err
	}
	if _, err := os.Stat(m.ftpPDB()); err != nil {
		if err := m.rebuildFTPDB(); err != nil {
			return "", err
		}
	}
	cmd := shQuote(bin) + " " + shQuote(m.ftpConf())
	port := m.Config().FTP.Port
	if portInUse(port) {
		if !force {
			return "", &PortInUseError{Port: port, Owner: portOwner(port)}
		}
		cmd = fmt.Sprintf("(/usr/sbin/lsof -t -nP -iTCP:%d -sTCP:LISTEN | /usr/bin/xargs kill; /bin/sleep 1) && %s", port, cmd)
	}
	return cmd, nil
}

func (m *Manager) startFTP(force bool) error {
	if _, running := m.ftpRunning(); running {
		return nil
	}
	cmd, err := m.ftpStartCmd(force)
	if err != nil {
		return err
	}
	if err := runAdmin("Benchtop needs your password to start the FTP server on a system port.", cmd); err != nil {
		return err
	}
	return m.waitFTP()
}

func (m *Manager) waitFTP() error {
	for i := 0; i < 30; i++ {
		if _, running := m.ftpRunning(); running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("pure-ftpd did not start. Check: log show --last 5m --predicate 'process == \"pure-ftpd\"'")
}

func (m *Manager) stopFTP() error {
	pid, running := m.ftpRunning()
	if !running {
		return nil
	}
	if err := runAdmin("Benchtop needs your password to stop the FTP server.", "/bin/kill "+strconv.Itoa(pid)); err != nil {
		return err
	}
	waitDead(pid, 5*time.Second)
	return nil
}

func (m *Manager) FTPUsers() ([]FTPUser, error) {
	f, err := os.Open(m.ftpPasswd())
	if errors.Is(err, os.ErrNotExist) {
		return []FTPUser{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	users := []FTPUser{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), ":")
		if len(parts) < 6 || parts[0] == "" {
			continue
		}
		users = append(users, FTPUser{User: parts[0], Dir: strings.TrimSuffix(parts[5], "/./")})
	}
	return users, sc.Err()
}

var ftpUserRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

func (m *Manager) purePW(stdin string, args ...string) error {
	bin := m.purePWBin()
	if bin == "" {
		return errors.New("pure-pw is not installed. Run: brew install pure-ftpd")
	}
	args = append(args, "-f", m.ftpPasswd(), "-m", "-F", m.ftpPDB())
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pure-pw: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) AddFTPUser(user, password, dir string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if !ftpUserRe.MatchString(user) {
		return errors.New("user name can only have letters, numbers, dot, dash and underscore")
	}
	if password == "" || strings.ContainsAny(password, "\n\r") {
		return errors.New("password is required")
	}
	if !filepath.IsAbs(dir) {
		return errors.New("folder must be an absolute path")
	}
	users, _ := m.FTPUsers()
	for _, u := range users {
		if u.User == user {
			return fmt.Errorf("user %s already exists", user)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return m.purePW(password+"\n"+password+"\n", "useradd", user,
		"-u", strconv.Itoa(os.Getuid()), "-g", strconv.Itoa(os.Getgid()), "-d", dir)
}

func (m *Manager) SetFTPPassword(user, password string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if password == "" || strings.ContainsAny(password, "\n\r") {
		return errors.New("password is required")
	}
	return m.purePW(password+"\n"+password+"\n", "passwd", user)
}

func (m *Manager) DeleteFTPUser(user string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.purePW("", "userdel", user)
}

func (m *Manager) rebuildFTPDB() error {
	if _, err := os.Stat(m.ftpPasswd()); err != nil {
		// pure-ftpd needs a database file even with no users.
		if err := os.WriteFile(m.ftpPasswd(), nil, 0o600); err != nil {
			return err
		}
	}
	bin := m.purePWBin()
	if bin == "" {
		return errors.New("pure-pw is not installed. Run: brew install pure-ftpd")
	}
	_, err := run(bin, "mkdb", m.ftpPDB(), "-f", m.ftpPasswd())
	return err
}
