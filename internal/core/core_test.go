package core

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFlyEnvCaddy(t *testing.T) {
	conf := `http://oncology dashboard,
http://oncology.test {
    encode zstd gzip
    root * "/Users/me/oncology/public"

    route {
        import enable-php-select 85
    }
    file_server
}
`
	p := filepath.Join(t.TempDir(), "1.conf")
	os.WriteFile(p, []byte(conf), 0o644)
	s, skipped, err := parseFlyEnvCaddy(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Domains, ",") != "oncology.test" || s.Root != "/Users/me/oncology/public" || s.PHP != "8.5" || s.HTTPS {
		t.Fatalf("bad site: %+v", s)
	}
	if len(skipped) != 1 || skipped[0] != "oncology dashboard" {
		t.Fatalf("bad skipped: %v", skipped)
	}
}

func TestRenderHosts(t *testing.T) {
	main := hostsBlock{begin: hostsBegin, end: hostsEnd, legacy: true}
	other := hostsBlock{begin: "# >>> Benchtop /tmp/x >>>", end: "# <<< Benchtop /tmp/x <<<"}
	base := "127.0.0.1\tlocalhost\n::1 localhost\n"
	once := renderHosts(base, []string{"a.test"}, main)
	if !strings.Contains(once, "127.0.0.1\ta.test") || !strings.HasPrefix(once, base) {
		t.Fatalf("bad hosts:\n%s", once)
	}
	if twice := renderHosts(once, []string{"a.test"}, main); twice != once {
		t.Fatalf("render is not stable:\n%s", twice)
	}
	legacy := base + "\n" + legacyHostsBegin + "\n127.0.0.1\told.test\n" + legacyHostsEnd + "\n"
	if got := renderHosts(legacy, []string{"a.test"}, main); got != once {
		t.Fatalf("legacy block not replaced:\n%s", got)
	}
	// A second home must not touch the main block or the legacy block.
	if got := renderHosts(once, nil, other); got != once {
		t.Fatalf("other home changed the main block:\n%s", got)
	}
	if got := renderHosts(legacy, nil, other); got != legacy {
		t.Fatalf("other home changed the legacy block:\n%s", got)
	}
	if cleared := renderHosts(once, nil, main); cleared != base {
		t.Fatalf("block not removed:\n%q", cleared)
	}
}

func TestNormalizeSite(t *testing.T) {
	s, err := NormalizeSite(Site{Domains: []string{"http://A.test, b.test/ a.test"}, Root: "/x", PHP: "php8.2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Domains, ",") != "a.test,b.test" || s.PHP != "8.2" {
		t.Fatalf("bad: %+v", s)
	}
	if _, err := NormalizeSite(Site{Domains: []string{"a.test"}, Root: "relative"}); err == nil {
		t.Fatal("want error for a relative root")
	}
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		c    string
		v    string
		want bool
	}{
		{"^8.2", "8.2.35", true},
		{"^8.2", "8.5.12", true},
		{"^8.2", "7.4.33", false},
		{"^7.3|^8.0", "7.4.33", true},
		{"^7.3 || ^8.0", "8.3.1", true},
		{"~8.1", "8.5.0", true},
		{"~8.1.0", "8.2.0", false},
		{">=8.1 <8.4", "8.3.36", true},
		{">=8.1,<8.4", "8.5.12", false},
		{"8.2.*", "8.2.35", true},
		{"8.2.*", "8.3.0", false},
		{">=7.2.5", "7.4.33", true},
		{"*", "5.6.40", true},
	}
	for _, c := range cases {
		v, _ := parseVersion(c.v)
		if got := satisfies(c.c, v); got != c.want {
			t.Errorf("satisfies(%q, %s) = %v, want %v", c.c, c.v, got, c.want)
		}
	}
}

func TestSuggestSite(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(t.TempDir(), "My App")
	os.MkdirAll(filepath.Join(proj, "public"), 0o755)
	os.WriteFile(filepath.Join(proj, "public", "index.php"), []byte("<?php"), 0o644)
	os.WriteFile(filepath.Join(proj, "artisan"), nil, 0o644)
	os.WriteFile(filepath.Join(proj, "composer.json"), []byte(`{"require":{"php":"^8.2"}}`), 0o644)
	s, err := m.SuggestSite(proj)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root != filepath.Join(proj, "public") || s.Domain != "my-app.test" || s.Framework != "Laravel" {
		t.Fatalf("bad suggestion: %+v", s)
	}
	installed := []PHPVersion{{Key: "7.4", Full: "7.4.33"}, {Key: "8.2", Full: "8.2.35"}, {Key: "8.5", Full: "8.5.12"}}
	if php, _ := suggestPHP(proj, s.Root, installed); php != "8.2" {
		t.Fatalf("want PHP 8.2, got %s", php)
	}
	// .php-version comes before composer.json.
	os.WriteFile(filepath.Join(proj, ".php-version"), []byte("php-7.4\n"), 0o644)
	if php, _ := suggestPHP(proj, s.Root, installed); php != "7.4" {
		t.Fatalf("want PHP 7.4 from .php-version, got %s", php)
	}
	hints := m.SiteHealth(Site{Root: proj, PHP: "8.2", Domains: []string{"x.test"}}, map[string]bool{})
	if len(hints) == 0 || hints[len(hints)-1].Fix == nil || hints[len(hints)-1].Fix.Value != filepath.Join(proj, "public") {
		t.Fatalf("want a public/ fix, got %+v", hints)
	}
}

func TestLoginAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	s := Settings{Login: LoginSettings{Enabled: true, Web: true}, HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, FTPPort: cfg.FTP.Port}
	if _, err := m.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(LoginPlistPath())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/plutil", "-lint", LoginPlistPath()).CombinedOutput(); err != nil {
		t.Fatalf("plist is not valid: %s\n%s", out, data)
	}
	if !strings.Contains(string(data), "<string>--login</string>") {
		t.Fatalf("plist does not run start --login:\n%s", data)
	}
	s.Login.Enabled = false
	if _, err := m.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if exists(LoginPlistPath()) {
		t.Fatal("plist was not removed")
	}
}

func TestFamilyFormulas(t *testing.T) {
	catalogMu.Lock()
	old := catalog
	catalog = map[string]catalogEntry{
		"mysql": {Version: "26.10.0"}, "mysql@8.4": {Version: "8.4.13"}, "mysql@5.7": {Version: "5.7.44"},
		"mysql@9.7": {Version: "9.7.1", Disabled: true}, "mysqltuner": {Version: "2.0"},
	}
	catalogMu.Unlock()
	defer func() { catalogMu.Lock(); catalog = old; catalogMu.Unlock() }()
	var fam family
	for _, f := range families() {
		if f.id == "mysql" {
			fam = f
		}
	}
	var got []string
	for _, f := range fam.formulas() {
		if brewPrefix(f.opt) == "" { // skip what this Mac has installed
			got = append(got, f.id+"="+f.label)
		}
	}
	// The newest comes first. 5.7 is too old, 9.7 is disabled, and
	// mysqltuner is another formula.
	if len(got) == 0 || got[0] != "mysql=26.10" {
		t.Fatalf("got %v", got)
	}
	for _, g := range got {
		if strings.HasPrefix(g, "mysql-5.7") || strings.HasPrefix(g, "mysql-9.7") || strings.Contains(g, "tuner") {
			t.Fatalf("got %v", got)
		}
	}
}

func TestSQLString(t *testing.T) {
	if got := sqlString(`it's a \ test`); got != `'it\'s a \\ test'` {
		t.Fatalf("got %s", got)
	}
}

func TestServicePortSkipsUsedPort(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	used := ln.Addr().(*net.TCPAddr).Port
	s := &fakeService{port: used}
	port, err := m.servicePort(s)
	if err != nil {
		t.Fatal(err)
	}
	if port == used || m.serviceSettings("fake").Port != port {
		t.Fatalf("want a free saved port, got %d (used %d)", port, used)
	}
}

func TestServicePortSkipsOtherVersions(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	// Another version saved this port, but it is stopped now.
	if err := m.setServiceSettings("other", func(st *ServiceSettings) { st.Port = free }); err != nil {
		t.Fatal(err)
	}
	port, err := m.servicePort(&fakeService{port: free})
	if err != nil {
		t.Fatal(err)
	}
	if port == free {
		t.Fatalf("got the port of the other version: %d", port)
	}
}

func TestUpdates(t *testing.T) {
	if got := withoutRevision("8.2.35_1"); got != "8.2.35" {
		t.Fatalf("withoutRevision: %s", got)
	}
	if got := shortFormula("shivammathur/php/php@7.4"); got != "php@7.4" {
		t.Fatalf("shortFormula: %s", got)
	}
	if got := formulaOfBin("/opt/homebrew/opt/no-such-formula/sbin/x"); got != "no-such-formula" {
		t.Fatalf("formulaOfBin: %s", got)
	}
	if got := formulaOfBin("/opt/homebrew/Cellar/caddy/2.8.4/bin/caddy"); got != "caddy" {
		t.Fatalf("formulaOfBin: %s", got)
	}
	setOutdated(map[string]outdatedEntry{"no-such-formula": {Version: "2.0_1"}})
	defer setOutdated(map[string]outdatedEntry{})
	// brewVersion is "" for a formula that is not installed, so 2.0 is new.
	if got := updateFor("no-such-formula"); got != "2.0" {
		t.Fatalf("updateFor: %q", got)
	}
	if got := updateFor("other"); got != "" {
		t.Fatalf("updateFor other: %q", got)
	}
}

type fakeService struct {
	extraService
	port int
}

func (f *fakeService) ID() string       { return "fake" }
func (f *fakeService) DefaultPort() int { return f.port }

func TestTestHomeHasOwnSockets(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m.phpSocket("8.2") == "/tmp/benchtop-php82.sock" {
		t.Fatal("a second home must not use the main PHP socket")
	}
	if !strings.HasPrefix(m.phpSocket("8.2"), "/tmp/benchtop-php82-") {
		t.Fatalf("unexpected socket %s", m.phpSocket("8.2"))
	}
}

func TestDNS(t *testing.T) {
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.LocalAddr().(*net.UDPAddr).Port
	ln.Close()
	go ServeDNS(port, []string{"test", "backend"})
	var ip net.IP
	for i := 0; i < 20; i++ {
		if ip, err = dnsQuery(port, "my-app.test"); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("my-app.test: %v %v", ip, err)
	}
	if ip, err := dnsQuery(port, "api.gifms.backend"); err != nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("api.gifms.backend: %v %v", ip, err)
	}
	if _, err := dnsQuery(port, "example.com"); err == nil {
		t.Fatal("example.com must get no answer")
	}
}

func TestNginxSite(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Config()
	cfg.Sites = []Site{
		{ID: "a", Domains: []string{"a.test", "www.a.test"}, Root: "/srv/a/public", PHP: "8.2", Server: "nginx", Extra: "location /x { return 204; }"},
		{ID: "b", Domains: []string{"b.test"}, Root: "/srv/b", PHP: "8.3"},
	}
	ng := m.renderNginx(cfg)
	for _, want := range []string{"server_name a.test www.a.test;", `root "/srv/a/public";`, "fastcgi_pass unix:" + m.phpSocket("8.2"), "location /x { return 204; }"} {
		if !strings.Contains(ng, want) {
			t.Fatalf("nginx.conf lacks %q:\n%s", want, ng)
		}
	}
	if strings.Contains(ng, "b.test") {
		t.Fatal("a Caddy site must not be in nginx.conf")
	}
	cf := m.renderCaddyfile(cfg)
	if !strings.Contains(cf, "reverse_proxy 127.0.0.1:8081") || strings.Contains(cf, "return 204") {
		t.Fatalf("bad Caddyfile for an Nginx site:\n%s", cf)
	}
	if _, err := NormalizeSite(Site{Domains: []string{"c.test"}, Root: "/c", Server: "apache"}); err == nil {
		t.Fatal("want an error for an unknown web server")
	}
}

func TestPHPFormulas(t *testing.T) {
	catalogMu.Lock()
	old := catalog
	catalog = map[string]catalogEntry{
		"php": {Version: "8.5.12"}, "php@8.4": {Version: "8.4.27"}, "php@8.1": {Version: "8.1.34", Deprecated: true},
		"shivammathur/php/php@8.4": {Version: "8.4.27"}, "shivammathur/php/php@7.4": {Version: "7.4.33"},
		"shivammathur/php/php@8.6": {Version: "8.6.0"}, "shivammathur/php/php@7.0": {Version: "7.0.33", Disabled: true},
	}
	catalogMu.Unlock()
	defer func() { catalogMu.Lock(); catalog = old; catalogMu.Unlock() }()
	got := phpFormulas()
	want := map[string]string{"8.5": "php", "8.4": "php@8.4", "8.1": "php@8.1", "7.4": "shivammathur/php/php@7.4", "8.6": "shivammathur/php/php@8.6"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, f := range want {
		if got[k].Formula != f {
			t.Fatalf("PHP %s: got %s, want %s", k, got[k].Formula, f)
		}
	}
	if !got["8.6"].Preview || got["8.5"].Preview {
		t.Fatalf("preview: %+v", got)
	}
	if f := phpFormulaFor("8.0"); f != "shivammathur/php/php@8.0" {
		t.Fatalf("phpFormulaFor 8.0: %s", f)
	}
}

func TestParkedSites(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, p := range []string{"shop/public", "Blog App", ".hidden", "node_modules", "taken"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "shop", "public", "index.php"), []byte("<?php"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	saved := Site{ID: "mine", Domains: []string{"mine.test"}, Root: filepath.Join(dir, "taken")}
	if err := m.updateConfig(func(c *Config) error {
		c.Parked = []string{dir}
		c.Sites = []Site{saved}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !m.scanParked() {
		t.Fatal("the first scan must report a change")
	}
	if m.scanParked() {
		t.Fatal("a second scan with no change must not report one")
	}
	got := map[string]Site{}
	for _, s := range m.Sites() {
		got[s.Domains[0]] = s
	}
	// "taken" has a saved site, so it gets no parked site.
	if len(got) != 3 || got["mine.test"].Parked != "" || got["taken.test"].ID != "" {
		t.Fatalf("got %+v", got)
	}
	if s := got["shop.test"]; s.Root != filepath.Join(dir, "shop", "public") || s.Parked != dir || s.ID != "parked-shop" {
		t.Fatalf("shop: %+v", s)
	}
	if _, ok := got["blog-app.test"]; !ok {
		t.Fatalf("blog-app.test is missing: %+v", got)
	}
	os.MkdirAll(filepath.Join(dir, "new"), 0o755)
	if !m.scanParked() {
		t.Fatal("a new folder must report a change")
	}
}

func TestSubdomains(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Site{ID: "acme", Domains: []string{"acme.test", "*.old.test"}, Root: "/tmp", Subdomains: true}
	if got := strings.Join(s.Hosts(), ","); got != "acme.test,*.old.test,*.acme.test" {
		t.Fatalf("Hosts: %s", got)
	}
	cfg := m.Config()
	cfg.Sites = []Site{s}
	if cf := m.renderCaddyfile(cfg); !strings.Contains(cf, "http://acme.test, http://*.old.test, http://*.acme.test {") {
		t.Fatalf("Caddyfile:\n%s", cf)
	}
	s.Server = "nginx"
	cfg.Sites = []Site{s}
	if ng := m.renderNginx(cfg); !strings.Contains(ng, "server_name acme.test *.old.test *.acme.test;") {
		t.Fatalf("nginx:\n%s", ng)
	}
}
