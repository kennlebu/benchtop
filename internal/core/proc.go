package core

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PortInUseError tells the caller that another program holds a port.
type PortInUseError struct {
	Port  int
	Owner string
}

func (e *PortInUseError) Error() string {
	msg := fmt.Sprintf("port %d is already in use", e.Port)
	if e.Owner != "" {
		msg += " by " + e.Owner
	} else {
		msg += " by another program (FlyEnv, or a process owned by root)"
	}
	return msg + ". Stop it first, or use force start."
}

func readPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// pidAlive also returns true for processes owned by root (EPERM).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !pidAlive(pid)
}

func portInUse(port int) bool {
	for _, host := range []string{"127.0.0.1", "[::1]"} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

// portOwner names the listener on a port. It only sees processes of the current user.
func portOwner(port int) string {
	out, err := exec.Command("/usr/sbin/lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fpc").Output()
	if err != nil {
		return ""
	}
	var pid, cmd string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") && pid == "" {
			pid = line[1:]
		} else if strings.HasPrefix(line, "c") && cmd == "" {
			cmd = line[1:]
		}
	}
	if pid == "" {
		return ""
	}
	return fmt.Sprintf("%s (pid %s)", cmd, pid)
}

// run executes a short command and returns its combined output.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("%s: %s", err, lastLines(out, 15))
		}
		return out, err
	}
	return out, nil
}

// runDetached starts a command that leaves a background process behind.
// Output goes to a log file, not a pipe, because a child that keeps the
// pipe open would block us. The new process group keeps the service alive
// when the user presses Ctrl-C in the terminal.
func runDetached(logPath string, name string, args ...string) error {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	start, _ := f.Seek(0, 2)
	fmt.Fprintf(f, "\n--- %s: %s %s\n", time.Now().Format(time.RFC3339), name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Run(); err != nil {
		data, _ := os.ReadFile(logPath)
		if int64(len(data)) > start {
			data = data[start:]
		}
		return fmt.Errorf("%s: %s", err, lastLines(strings.TrimSpace(string(data)), 15))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runAdmin runs shell commands as root. It shows the macOS password dialog,
// so it works from the terminal and from the web dashboard.
func runAdmin(prompt string, cmds ...string) error {
	if len(cmds) == 0 {
		return nil
	}
	script := strings.Join(cmds, " && ")
	if os.Geteuid() == 0 {
		_, err := run("/bin/sh", "-c", script)
		return err
	}
	esc := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`)
	}
	as := fmt.Sprintf(`do shell script "%s" with prompt "%s" with administrator privileges`, esc(script), esc(prompt))
	out, err := exec.Command("/usr/bin/osascript", "-e", as).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") {
			return errors.New("the admin password dialog was cancelled")
		}
		return fmt.Errorf("admin command failed: %s", msg)
	}
	return nil
}

func findBinary(override string, candidates ...string) string {
	if override != "" {
		return override
	}
	for _, c := range candidates {
		if strings.Contains(c, "/") {
			if _, err := os.Stat(c); err == nil {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}
