// Package web serves the local dashboard and its JSON API.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kennlebu/benchtop/internal/core"
)

//go:embed static
var static embed.FS

// started changes each time the dashboard starts. An open page reloads when
// it sees a new value, so it never runs old code against a new server.
var started = time.Now().UnixNano()

type server struct {
	m *core.Manager
}

func Handler(m *core.Manager) http.Handler {
	s := &server{m: m}
	mux := http.NewServeMux()
	sub, _ := fs.Sub(static, "static")
	files := http.FileServer(http.FS(sub))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// The page is embedded in the binary. Do not cache it, so an upgrade shows at once.
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { reply(w, s.state(), nil) })
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/services/{id}/{action}", s.service)
	mux.HandleFunc("POST /api/start-all", s.wrap(func(r *http.Request) (any, error) { return nil, s.m.StartAll() }))
	mux.HandleFunc("POST /api/stop-all", s.wrap(func(r *http.Request) (any, error) { return nil, s.m.StopAll() }))
	mux.HandleFunc("POST /api/parked", s.wrap(func(r *http.Request) (any, error) { return s.parked(r, true) }))
	mux.HandleFunc("DELETE /api/parked", s.wrap(func(r *http.Request) (any, error) { return s.parked(r, false) }))
	mux.HandleFunc("POST /api/hosts/sync", s.wrap(func(r *http.Request) (any, error) { return nil, s.m.SyncHosts() }))
	mux.HandleFunc("POST /api/updates/check", s.wrap(func(r *http.Request) (any, error) {
		if err := s.m.RefreshCatalog(0); err != nil {
			return nil, err
		}
		n := 0
		for _, svc := range s.m.Status() {
			if svc.Update != "" {
				n++
			}
		}
		return map[string]int{"updates": n}, nil
	}))
	mux.HandleFunc("POST /api/trust", s.wrap(func(r *http.Request) (any, error) { return nil, s.m.TrustCA() }))
	mux.HandleFunc("POST /api/import-flyenv", s.wrap(func(r *http.Request) (any, error) {
		report, err := s.m.ImportFlyEnv(core.FlyEnvHome())
		return map[string]any{"report": report}, err
	}))
	mux.HandleFunc("GET /api/php/{version}/extensions", s.wrap(func(r *http.Request) (any, error) {
		return s.m.ListPHPExtensions(r.PathValue("version"))
	}))
	mux.HandleFunc("POST /api/php/{version}/extensions/{name}/{action}", s.wrap(func(r *http.Request) (any, error) {
		v, name := r.PathValue("version"), r.PathValue("name")
		switch r.PathValue("action") {
		case "install":
			return nil, s.m.InstallPHPExtension(v, name)
		case "on":
			return nil, s.m.SetPHPExtension(v, name, true)
		case "off":
			return nil, s.m.SetPHPExtension(v, name, false)
		}
		return nil, errors.New("unknown action")
	}))
	mux.HandleFunc("GET /api/backup", func(w http.ResponseWriter, r *http.Request) {
		data, err := s.m.MakeBackup()
		if err != nil {
			reply(w, nil, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="benchtop-backup-%s.json"`, time.Now().Format("2006-01-02")))
		w.Write(data)
	})
	mux.HandleFunc("POST /api/restore", s.wrap(func(r *http.Request) (any, error) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			return nil, err
		}
		report, err := s.m.RestoreBackup(data)
		return map[string]any{"report": report}, err
	}))
	mux.HandleFunc("PUT /api/settings", s.wrap(func(r *http.Request) (any, error) {
		var in core.Settings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		notes, err := s.m.UpdateSettings(in)
		return map[string]any{"warnings": notes}, err
	}))

	mux.HandleFunc("PUT /api/services/{id}/settings", s.wrap(func(r *http.Request) (any, error) {
		var in core.ServiceSettingsUpdate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.UpdateServiceSettings(r.PathValue("id"), in)
	}))
	mux.HandleFunc("GET /api/services/{id}/env", s.wrap(func(r *http.Request) (any, error) {
		env, err := s.m.ServiceEnv(r.PathValue("id"))
		return map[string]any{"env": env}, err
	}))
	mux.HandleFunc("GET /api/db/{id}/databases", s.wrap(func(r *http.Request) (any, error) {
		dbs, err := s.m.Databases(r.PathValue("id"))
		return map[string]any{"databases": dbs}, err
	}))
	mux.HandleFunc("POST /api/db/{id}/databases", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ Name string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.CreateDatabase(r.PathValue("id"), in.Name)
	}))
	mux.HandleFunc("POST /api/db/{id}/users", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ User, Password, Database string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.CreateDatabaseUser(r.PathValue("id"), in.User, in.Password, in.Database)
	}))
	mux.HandleFunc("POST /api/db/{id}/root-password", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ Password string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.SetRootPassword(r.PathValue("id"), in.Password)
	}))
	mux.HandleFunc("POST /api/db/{id}/shell", s.wrap(func(r *http.Request) (any, error) {
		return nil, s.m.OpenShell(r.PathValue("id"))
	}))

	mux.HandleFunc("POST /api/sites", s.wrap(s.saveSite))
	mux.HandleFunc("POST /api/suggest-site", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ Path string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return s.m.SuggestSite(in.Path)
	}))
	mux.HandleFunc("DELETE /api/sites/{id}", s.wrap(func(r *http.Request) (any, error) {
		warnings, err := s.m.DeleteSite(r.PathValue("id"))
		return map[string]any{"warnings": warnings}, err
	}))

	mux.HandleFunc("POST /api/ftp/users", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ User, Password, Dir string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.AddFTPUser(in.User, in.Password, in.Dir)
	}))
	mux.HandleFunc("POST /api/ftp/users/{user}/password", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ Password string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.SetFTPPassword(r.PathValue("user"), in.Password)
	}))
	mux.HandleFunc("DELETE /api/ftp/users/{user}", s.wrap(func(r *http.Request) (any, error) {
		return nil, s.m.DeleteFTPUser(r.PathValue("user"))
	}))

	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		files := s.m.LogFiles()
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		reply(w, map[string]any{"logs": names}, nil)
	})
	mux.HandleFunc("GET /api/logs/{name}", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		if n <= 0 {
			n = 300
		}
		text, err := s.m.TailLog(r.PathValue("name"), n)
		reply(w, map[string]any{"text": text}, err)
	})
	mux.HandleFunc("GET /api/logs/{name}/stream", s.logStream)

	mux.HandleFunc("POST /api/pick-folder", s.wrap(func(r *http.Request) (any, error) {
		out, err := exec.Command("/usr/bin/osascript", "-e",
			`POSIX path of (choose folder with prompt "Choose a folder")`).Output()
		if err != nil {
			return map[string]any{"path": ""}, nil // the user cancelled
		}
		return map[string]any{"path": strings.TrimSuffix(strings.TrimSpace(string(out)), "/")}, nil
	}))
	mux.HandleFunc("POST /api/open", s.wrap(func(r *http.Request) (any, error) {
		var in struct{ What, Path string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, err
		}
		return nil, s.m.Open(in.What, in.Path)
	}))

	return guard(mux)
}

// guard blocks requests from other web sites. Only the dashboard on
// localhost may call the API: we check the Host header (against DNS
// rebinding) and require a custom header on writes (against CSRF).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Benchtop") != "1" {
			http.Error(w, "missing X-Benchtop header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) wrap(fn func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		reply(w, v, err)
	}
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		body := map[string]any{"error": err.Error()}
		var pe *core.PortInUseError
		if errors.As(err, &pe) {
			body["portInUse"] = pe.Port
		}
		if m, ok := v.(map[string]any); ok {
			for k, val := range m {
				body[k] = val
			}
		}
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(body)
		return
	}
	if v == nil {
		v = map[string]any{"ok": true}
	}
	json.NewEncoder(w).Encode(v)
}

func (s *server) state() map[string]any {
	cfg := s.m.Config()
	users, _ := s.m.FTPUsers()
	services := s.m.Status()
	running := map[string]bool{}
	for _, svc := range services {
		running[svc.ID] = svc.Running
	}
	all := s.m.Sites()
	sites := make([]map[string]any, 0, len(all))
	for _, site := range all {
		sites = append(sites, map[string]any{"site": site, "url": core.SiteURL(site), "hints": s.m.SiteHealth(site, running)})
	}
	editors, terminals := core.InstalledApps()
	_, loginErr := os.Stat(core.LoginPlistPath())
	return map[string]any{
		"home":     s.m.Home,
		"services": services,
		"sites":    sites,
		"php":      core.DiscoverPHP(),
		// The PHP versions that the "Add a PHP version" card can install.
		"phpChoices":    s.m.PHPChoices(),
		"phpInstalling": s.m.PHPInstalling(),
		"ftpUsers":      users,
		"settings": core.Settings{
			Login: cfg.Login, Editor: cfg.Editor, Terminal: cfg.Terminal,
			HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, FTPPort: cfg.FTP.Port,
		},
		"loginInstalled": loginErr == nil,
		"apps":           map[string]any{"editors": editors, "terminals": terminals},
		"caddyfile":      s.m.CaddyfilePath(),
		"started":        started,
		"parked":         s.m.Parked(),
	}
}

func sseStart(w http.ResponseWriter) (http.Flusher, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	return f, true
}

func sseSend(w io.Writer, event string, data []byte) {
	fmt.Fprintf(w, "event: %s\n", event)
	for _, line := range bytes.Split(data, []byte("\n")) {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprint(w, "\n")
}

// events sends the full state when it changes. It checks every second.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := sseStart(w)
	if !ok {
		return
	}
	var last []byte
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		data, _ := json.Marshal(s.state())
		if !bytes.Equal(data, last) {
			sseSend(w, "state", data)
			f.Flush()
			last = data
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

// logStream sends the end of a log, then each new line as it is written.
func (s *server) logStream(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, ok := s.m.LogFiles()[name]
	if !ok {
		http.Error(w, "unknown log", http.StatusNotFound)
		return
	}
	f, ok := sseStart(w)
	if !ok {
		return
	}
	text, _ := s.m.TailLog(name, 400)
	data, _ := json.Marshal(text)
	sseSend(w, "reset", data)
	f.Flush()

	offset := int64(0)
	if st, err := os.Stat(path); err == nil {
		offset = st.Size()
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if st.Size() < offset {
			// The log was rotated or cleared.
			offset = 0
			sseSend(w, "reset", []byte(`""`))
		}
		if st.Size() == offset {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		size := st.Size() - offset
		if size > 256*1024 {
			offset = st.Size() - 256*1024
			size = 256 * 1024
		}
		buf := make([]byte, size)
		n, _ := file.ReadAt(buf, offset)
		file.Close()
		offset += int64(n)
		data, _ := json.Marshal(string(buf[:n]))
		sseSend(w, "append", data)
		f.Flush()
	}
}

func (s *server) service(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	force := r.URL.Query().Get("force") == "1"
	var err error
	switch action {
	case "start":
		err = s.m.Start(id, force)
	case "stop":
		err = s.m.Stop(id)
	case "restart":
		err = s.m.Restart(id, force)
	case "install":
		err = s.m.Install(id)
	case "upgrade":
		err = s.m.Upgrade(id)
	default:
		err = errors.New("unknown action " + action)
	}
	reply(w, nil, err)
}

func (s *server) parked(r *http.Request, add bool) (any, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, err
	}
	var warnings []string
	var err error
	if add {
		warnings, err = s.m.Park(in.Path)
	} else {
		warnings, err = s.m.Unpark(in.Path)
	}
	return map[string]any{"warnings": warnings}, err
}

func (s *server) saveSite(r *http.Request) (any, error) {
	var site core.Site
	if err := json.NewDecoder(r.Body).Decode(&site); err != nil {
		return nil, err
	}
	saved, warnings, err := s.m.SaveSite(site)
	return map[string]any{"site": saved, "warnings": warnings}, err
}
