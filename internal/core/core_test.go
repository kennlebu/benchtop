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

func TestGroupVersions(t *testing.T) {
	in := []ServiceStatus{
		{ID: "mysql", Name: "MySQL 26.10"}, {ID: "mysql-8.4", Name: "MySQL 8.4"},
		{ID: "postgres-17"}, {ID: "postgres-16", Installed: true}, {ID: "postgres-15"},
		{ID: "redis"},
	}
	var got []string
	out := groupVersions(in)
	for _, s := range out {
		got = append(got, s.ID)
	}
	if strings.Join(got, ",") != "mysql,postgres-16,redis" {
		t.Fatalf("got %v", got)
	}
	if len(out[0].Choices) != 2 || out[0].Choices[1].ID != "mysql-8.4" {
		t.Fatalf("mysql choices: %+v", out[0].Choices)
	}
	if len(out[1].Choices) != 2 || out[1].Family != "postgres" {
		t.Fatalf("postgres card: %+v", out[1])
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

func TestExtensionIni(t *testing.T) {
	if got := extensionIni("redis"); !strings.Contains(got, "\nextension=redis.so\n") {
		t.Fatalf("redis ini: %s", got)
	}
	x := extensionIni("xdebug")
	if !strings.Contains(x, "zend_extension=xdebug.so") || !strings.Contains(x, "xdebug.start_with_request=trigger") {
		t.Fatalf("xdebug ini: %s", x)
	}
	if peclPackage("xdebug", "7.4") != "xdebug-3.1.6" || peclPackage("xdebug", "8.2") != "xdebug" {
		t.Fatal("wrong xdebug release for the PHP version")
	}
}
