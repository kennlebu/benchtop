// Command benchtop manages a local Caddy + PHP-FPM + Pure-FTPd setup on macOS.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kennlebu/benchtop/internal/core"
	"github.com/kennlebu/benchtop/internal/web"
)

const usage = `Benchtop: local Caddy, PHP-FPM and Pure-FTPd manager.

Usage:
  benchtop ui [--port N] [--no-open]   Open the web dashboard
  benchtop ui --background             Run the dashboard after the terminal closes
  benchtop ui --stop                   Stop the background dashboard
  benchtop status                      Show services
  benchtop start [SERVICE] [--force]   Start all, or caddy | php | php-8.2 | ftp
  benchtop start --login               Start what the "start at login" setting names
  benchtop stop [SERVICE]              Stop all, or one service
  benchtop restart SERVICE [--force]   Restart one service
  benchtop sites                       List sites
  benchtop site add DOMAINS ROOT [--php 8.2] [--https] [--subdomains] [--server nginx]
  benchtop site rm ID|DOMAIN           Delete a site
  benchtop park FOLDER                 Give each project in FOLDER a <name>.test site
  benchtop unpark FOLDER               Remove a parked folder (the files stay)
  benchtop parked                      List parked folders
  benchtop versions                    List installed PHP versions
  benchtop php [ARGS]                  Run the PHP of the site in this folder
  benchtop composer [ARGS]             Run Composer with that PHP
  benchtop node|npm|npx [ARGS]         Run Node with the version from .nvmrc or package.json
  benchtop which                       Show which PHP and Node these commands use here
  benchtop ext list VERSION            List PECL extensions (for example: ext list 8.2)
  benchtop ext install VERSION NAME    Install an extension with PECL (xdebug, redis, ...)
  benchtop ext on|off VERSION NAME     Turn an installed extension on or off
  benchtop ftp users                   List FTP users
  benchtop ftp add USER DIR            Add an FTP user (asks for a password)
  benchtop ftp passwd USER             Change an FTP password
  benchtop ftp rm USER                 Delete an FTP user
  benchtop upgrade SERVICE             Upgrade a service with Homebrew, then restart it
  benchtop install SERVICE             Install a service version with Homebrew (mysql-8.0, postgres-18, redis, ...)
  benchtop db list SERVICE             List databases (mysql-8.4, postgres-17)
  benchtop db create SERVICE NAME      Create a database
  benchtop db user SERVICE USER [DB]   Create a user (asks for a password)
  benchtop db root-password SERVICE    Tell Benchtop the root password
  benchtop db env SERVICE              Print .env lines for Laravel
  benchtop db shell SERVICE            Open the database client in the terminal
  benchtop hosts                       Write site domains to /etc/hosts
  benchtop trust                       Trust the local HTTPS certificate
  benchtop logs NAME [-n 100]          Show a log (run "benchtop logs" for names)
  benchtop import-flyenv               Import sites and FTP users from FlyEnv
  benchtop backup FILE                 Save sites, settings and FTP users to a file
  benchtop restore FILE                Add the sites and FTP users from a backup

--force stops another program that holds the port (for example another local server app).
Data folder: ~/.benchtop (set BENCHTOP_HOME to change it).
Turn on "start at login" on the dashboard settings page.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	m, err := core.New(core.DefaultHome())
	if err != nil {
		return err
	}
	cmd, rest := args[0], args[1:]
	if cmd == "dns-serve" {
		// Internal: the wildcard DNS server that the "dns" service runs.
		fs := flag.NewFlagSet("dns-serve", flag.ExitOnError)
		port := fs.Int("port", 53535, "UDP port")
		tld := fs.String("tld", "test", "comma-separated domain endings")
		fs.Parse(rest)
		return core.ServeDNS(*port, strings.Split(*tld, ","))
	}
	switch cmd {
	case "php", "composer", "node", "npm", "npx":
		runTool(m, cmd, rest) // does not return
	}
	switch cmd {
	case "ui":
		return cmdUI(m, rest)
	case "status":
		printStatus(m)
		return nil
	case "start", "stop", "restart":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		force := fs.Bool("force", false, "stop another program that holds the port")
		login := fs.Bool("login", false, "start what the login settings name")
		fs.Parse(reorder(rest))
		svc := fs.Arg(0)
		switch {
		case cmd == "start" && *login:
			fmt.Println(time.Now().Format(time.RFC3339), "start at login")
			err = m.StartAtLogin()
		case cmd == "start" && (svc == "" || svc == "all"):
			err = m.StartAll()
		case cmd == "stop" && (svc == "" || svc == "all"):
			err = m.StopAll()
		case svc == "":
			return errors.New("name a service: caddy, php, php-8.2, ftp")
		case cmd == "start":
			err = m.Start(svc, *force)
		case cmd == "stop":
			err = m.Stop(svc)
		default:
			err = m.Restart(svc, *force)
		}
		printStatus(m)
		var pe *core.PortInUseError
		if errors.As(err, &pe) {
			err = fmt.Errorf("%w\nRun again with --force to stop it", err)
		}
		return err
	case "sites":
		printSites(m)
		return nil
	case "park", "unpark":
		if len(rest) != 1 {
			return fmt.Errorf("use: benchtop %s FOLDER", cmd)
		}
		dir, err := filepath.Abs(rest[0])
		if err != nil {
			return err
		}
		var warnings []string
		if cmd == "park" {
			warnings, err = m.Park(dir)
		} else {
			warnings, err = m.Unpark(dir)
		}
		for _, w := range warnings {
			fmt.Println("warning:", w)
		}
		if err == nil && cmd == "park" {
			printSites(m)
		}
		return err
	case "parked":
		for _, d := range m.Parked() {
			fmt.Println(d)
		}
		return nil
	case "site":
		return cmdSite(m, rest)
	case "versions":
		for _, v := range core.DiscoverPHP() {
			fmt.Printf("%-5s %-9s %s\n", v.Key, v.Full, v.FPM)
		}
		return nil
	case "which":
		wd, _ := os.Getwd()
		v, reason, err := m.PHPForDir(wd)
		if err != nil {
			return err
		}
		fmt.Printf("PHP %s (%s): %s\n", v.Full, reason, filepath.Join(filepath.Dir(filepath.Dir(v.FPM)), "bin", "php"))
		if n, reason, err := core.NodeForDir(wd); err != nil {
			fmt.Println("Node:", err)
		} else if n.Version == "" {
			fmt.Printf("Node (%s): %s\n", reason, filepath.Join(n.Bin, "node"))
		} else {
			fmt.Printf("Node %s (%s, %s): %s\n", n.Version, n.From, reason, filepath.Join(n.Bin, "node"))
		}
		return nil
	case "ext":
		return cmdExt(m, rest)
	case "ftp":
		return cmdFTP(m, rest)
	case "install":
		if len(rest) != 1 {
			return errors.New("use: benchtop install SERVICE")
		}
		if err := m.Install(rest[0]); err != nil {
			return err
		}
		fmt.Println("Installing. The output is in: benchtop logs brew")
		m.WaitInstalls()
		for _, s := range m.Status() {
			if s.ID == rest[0] && s.InstallError != "" {
				return errors.New(s.InstallError)
			}
		}
		return nil
	case "upgrade":
		if len(rest) != 1 {
			return errors.New("use: benchtop upgrade SERVICE")
		}
		if err := m.RefreshCatalog(0); err != nil {
			return err
		}
		if err := m.Upgrade(rest[0]); err != nil {
			return err
		}
		fmt.Println("Upgrading. The output is in: benchtop logs brew")
		m.WaitInstalls()
		return nil
	case "db":
		return cmdDB(m, rest)
	case "hosts":
		return m.SyncHosts()
	case "trust":
		return m.TrustCA()
	case "logs":
		return cmdLogs(m, rest)
	case "backup":
		if len(rest) != 1 {
			return errors.New("use: benchtop backup FILE")
		}
		data, err := m.MakeBackup()
		if err != nil {
			return err
		}
		if err := os.WriteFile(rest[0], data, 0o600); err != nil {
			return err
		}
		fmt.Println("Saved", rest[0], "(no database data and no Keychain passwords)")
		return nil
	case "restore":
		if len(rest) != 1 {
			return errors.New("use: benchtop restore FILE")
		}
		data, err := os.ReadFile(rest[0])
		if err != nil {
			return err
		}
		report, err := m.RestoreBackup(data)
		for _, line := range report {
			fmt.Println(line)
		}
		return err
	case "import-flyenv":
		report, err := m.ImportFlyEnv(core.FlyEnvHome())
		for _, line := range report {
			fmt.Println(line)
		}
		return err
	}
	return fmt.Errorf("unknown command %q. Run benchtop help", cmd)
}

// reorder puts flags before positional arguments, so "start ftp --force" works.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && valueFlag(a) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func valueFlag(f string) bool {
	switch strings.TrimLeft(f, "-") {
	case "php", "port", "n", "extra", "server":
		return true
	}
	return false
}

func printStatus(m *core.Manager) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tVERSION\tSTATE\tDETAIL")
	for _, s := range m.Status() {
		state := "stopped"
		if s.Running {
			state = fmt.Sprintf("running (pid %d)", s.PID)
		} else if !s.Installed {
			state = "not installed"
		}
		detail := s.Detail
		if s.Update != "" {
			state += ", update " + s.Update
		}
		if len(s.UsedBy) > 0 {
			detail += "; used by " + strings.Join(s.UsedBy, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Version, state, detail)
	}
	w.Flush()
}

func printSites(m *core.Manager) {
	sites := m.Sites()
	if len(sites) == 0 {
		fmt.Println("No sites. Add one with: benchtop site add myapp.test ~/Projects/myapp/public --php 8.3")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tURL\tPHP\tROOT")
	for _, s := range sites {
		php := s.PHP
		if php == "" {
			php = "static"
		}
		url := core.SiteURL(s)
		if len(s.Domains) > 1 {
			url += fmt.Sprintf(" (+%d)", len(s.Domains)-1)
		}
		root := s.Root
		if s.Parked != "" {
			root += " (parked)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, url, php, root)
	}
	w.Flush()
}

func cmdSite(m *core.Manager, args []string) error {
	if len(args) == 0 {
		return errors.New("use: benchtop site add|rm")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("site add", flag.ExitOnError)
		php := fs.String("php", "", "PHP version, for example 8.2 (empty for static)")
		https := fs.Bool("https", false, "serve with HTTPS")
		subdomains := fs.Bool("subdomains", false, "send *.DOMAIN to the site too")
		server := fs.String("server", "", "web server: caddy (default) or nginx")
		fs.Parse(reorder(args[1:]))
		if fs.NArg() != 2 {
			return errors.New("use: benchtop site add DOMAINS ROOT [--php 8.2] [--https]")
		}
		root := fs.Arg(1)
		if !strings.HasPrefix(root, "/") && !strings.HasPrefix(root, "~/") {
			wd, _ := os.Getwd()
			root = wd + "/" + root
		}
		s, warnings, err := m.SaveSite(core.Site{Domains: []string{fs.Arg(0)}, Root: root, PHP: *php, HTTPS: *https, Subdomains: *subdomains, Server: *server})
		if err != nil {
			return err
		}
		for _, w := range warnings {
			fmt.Println("warning:", w)
		}
		fmt.Printf("Added %s -> %s\n", core.SiteURL(s), s.Root)
		return nil
	case "rm", "delete":
		if len(args) != 2 {
			return errors.New("use: benchtop site rm ID|DOMAIN")
		}
		warnings, err := m.DeleteSite(args[1])
		for _, w := range warnings {
			fmt.Println("warning:", w)
		}
		return err
	}
	return fmt.Errorf("unknown site command %q", args[0])
}

func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	// Hide the typed text when stdin is a terminal.
	stty := func(arg string) error {
		c := exec.Command("/bin/stty", arg)
		c.Stdin = os.Stdin
		return c.Run()
	}
	if stty("-echo") == nil {
		defer func() { stty("echo"); fmt.Println() }()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func cmdFTP(m *core.Manager, args []string) error {
	if len(args) == 0 || args[0] == "users" {
		users, err := m.FTPUsers()
		if err != nil {
			return err
		}
		for _, u := range users {
			fmt.Printf("%-16s %s\n", u.User, u.Dir)
		}
		return nil
	}
	switch args[0] {
	case "add":
		if len(args) != 3 {
			return errors.New("use: benchtop ftp add USER DIR")
		}
		pw, err := readPassword("Password: ")
		if err != nil {
			return err
		}
		return m.AddFTPUser(args[1], pw, args[2])
	case "passwd":
		if len(args) != 2 {
			return errors.New("use: benchtop ftp passwd USER")
		}
		pw, err := readPassword("New password: ")
		if err != nil {
			return err
		}
		return m.SetFTPPassword(args[1], pw)
	case "rm", "delete":
		if len(args) != 2 {
			return errors.New("use: benchtop ftp rm USER")
		}
		return m.DeleteFTPUser(args[1])
	}
	return fmt.Errorf("unknown ftp command %q", args[0])
}

func cmdLogs(m *core.Manager, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	n := fs.Int("n", 100, "number of lines")
	fs.Parse(reorder(args))
	if fs.NArg() == 0 {
		files := m.LogFiles()
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("%-24s %s\n", name, files[name])
		}
		return nil
	}
	text, err := m.TailLog(fs.Arg(0), *n)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}

func cmdUI(m *core.Manager, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	port := fs.Int("port", m.Config().UIPort, "port for the dashboard")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	background := fs.Bool("background", false, "run the dashboard in the background")
	stop := fs.Bool("stop", false, "stop the background dashboard")
	fs.Parse(args)

	if *stop {
		return m.StopUIBackground()
	}
	if *background {
		url, err := m.StartUIBackground()
		if err != nil {
			return err
		}
		fmt.Println("Benchtop dashboard:", url)
		if !*noOpen {
			exec.Command("/usr/bin/open", url).Run()
		}
		return nil
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	url := "http://" + addr
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// The dashboard may already run, for example from a login item.
		if !*noOpen {
			exec.Command("/usr/bin/open", url).Run()
		}
		return fmt.Errorf("cannot listen on %s (is the dashboard already running?): %w", addr, err)
	}
	srv := &http.Server{Handler: web.Handler(m), ReadHeaderTimeout: 10 * time.Second}
	// Find new projects in parked folders.
	go func() {
		for range time.Tick(5 * time.Second) {
			m.CheckParked()
		}
	}()
	// Learn which service versions Homebrew has, once a day.
	go func() {
		for {
			if err := m.RefreshCatalog(24 * time.Hour); err != nil {
				log.Printf("version list: %v", err)
			}
			time.Sleep(time.Hour)
		}
	}()
	fmt.Printf("Benchtop dashboard: %s\nPress Ctrl-C to quit. Services keep running.\n", url)
	if !*noOpen {
		go exec.Command("/usr/bin/open", url).Run()
	}
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func cmdDB(m *core.Manager, args []string) error {
	if len(args) < 2 {
		return errors.New("use: benchtop db list|create|user|root-password|env|shell SERVICE")
	}
	sub, id := args[0], args[1]
	switch sub {
	case "list":
		dbs, err := m.Databases(id)
		for _, d := range dbs {
			fmt.Println(d)
		}
		return err
	case "create":
		if len(args) != 3 {
			return errors.New("use: benchtop db create SERVICE NAME")
		}
		return m.CreateDatabase(id, args[2])
	case "user":
		if len(args) < 3 || len(args) > 4 {
			return errors.New("use: benchtop db user SERVICE USER [DATABASE]")
		}
		pw, err := readPassword("Password for the new user: ")
		if err != nil {
			return err
		}
		db := ""
		if len(args) == 4 {
			db = args[3]
		}
		return m.CreateDatabaseUser(id, args[2], pw, db)
	case "root-password":
		pw, err := readPassword("Root password: ")
		if err != nil {
			return err
		}
		return m.SetRootPassword(id, pw)
	case "env":
		env, err := m.ServiceEnv(id)
		fmt.Print(env)
		return err
	case "shell":
		return m.OpenShell(id)
	}
	return fmt.Errorf("unknown db command %q", sub)
}

// runTool runs PHP, Composer or a Node tool with the version of the
// current folder, and exits with its exit code. Arguments pass through as
// they are.
func runTool(m *core.Manager, tool string, args []string) {
	wd, _ := os.Getwd()
	var c *exec.Cmd
	var err error
	if tool == "php" || tool == "composer" {
		c, err = m.PHPCommand(wd, tool, args)
	} else {
		c, err = core.NodeCommand(wd, tool, args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	signal.Ignore(os.Interrupt) // the child gets Ctrl-C itself
	if err := c.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func cmdExt(m *core.Manager, args []string) error {
	if len(args) < 2 {
		return errors.New("use: benchtop ext list|install|on|off VERSION [NAME]")
	}
	sub, key := args[0], args[1]
	if sub == "list" {
		list, err := m.ListPHPExtensions(key)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "EXTENSION\tINSTALLED\tON\tNOTE")
		yes := func(b bool) string {
			if b {
				return "yes"
			}
			return "-"
		}
		for _, e := range list.Extensions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, yes(e.Installed), yes(e.Loaded), e.Note)
		}
		w.Flush()
		fmt.Println("Loaded:", strings.Join(list.Loaded, " "))
		return nil
	}
	if len(args) != 3 {
		return fmt.Errorf("use: benchtop ext %s VERSION NAME", sub)
	}
	switch sub {
	case "install":
		if err := m.InstallPHPExtension(key, args[2]); err != nil {
			return err
		}
		fmt.Println("Installing. The output is in: benchtop logs pecl")
		m.WaitInstalls()
		return nil
	case "on":
		return m.SetPHPExtension(key, args[2], true)
	case "off":
		return m.SetPHPExtension(key, args[2], false)
	}
	return fmt.Errorf("unknown ext command %q", sub)
}
