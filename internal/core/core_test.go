package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
