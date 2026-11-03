package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// NodeVersion is one installed Node.js, from nvm, fnm or Homebrew.
type NodeVersion struct {
	Version string `json:"version"` // "20.14.0"
	Bin     string `json:"bin"`     // the folder with node, npm and npx
	From    string `json:"from"`    // "nvm", "fnm" or "Homebrew"
}

// ltsNames are the code names that .nvmrc can use, for example "lts/iron".
var ltsNames = map[string]int{
	"argon": 4, "boron": 6, "carbon": 8, "dubnium": 10, "erbium": 12, "fermium": 14,
	"gallium": 16, "hydrogen": 18, "iron": 20, "jod": 22, "krypton": 24,
}

// DiscoverNode lists the installed Node versions, the oldest first.
func DiscoverNode() []NodeVersion {
	home, _ := os.UserHomeDir()
	nvm := os.Getenv("NVM_DIR")
	if nvm == "" {
		nvm = filepath.Join(home, ".nvm")
	}
	seen := map[string]bool{}
	var out []NodeVersion
	add := func(version, bin, from string) {
		version = strings.TrimPrefix(version, "v")
		if _, ok := parseVersion(version); !ok || seen[version] || !exists(filepath.Join(bin, "node")) {
			return
		}
		seen[version] = true
		out = append(out, NodeVersion{Version: version, Bin: bin, From: from})
	}
	dirs, _ := filepath.Glob(filepath.Join(nvm, "versions", "node", "v*"))
	for _, d := range dirs {
		add(filepath.Base(d), filepath.Join(d, "bin"), "nvm")
	}
	for _, root := range []string{filepath.Join(home, "Library", "Application Support", "fnm"), filepath.Join(home, ".local", "share", "fnm")} {
		dirs, _ := filepath.Glob(filepath.Join(root, "node-versions", "v*"))
		for _, d := range dirs {
			add(filepath.Base(d), filepath.Join(d, "installation", "bin"), "fnm")
		}
	}
	for _, p := range []string{"/opt/homebrew/opt", "/usr/local/opt"} {
		dirs, _ := filepath.Glob(filepath.Join(p, "node*"))
		for _, d := range dirs {
			if b := filepath.Base(d); b == "node" || strings.HasPrefix(b, "node@") {
				add(brewVersion(b), filepath.Join(d, "bin"), "Homebrew")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return semverLess(out[i].Version, out[j].Version) })
	return out
}

func semverLess(a, b string) bool {
	va, _ := parseVersion(a)
	vb, _ := parseVersion(b)
	for i := range va {
		if va[i] != vb[i] {
			return va[i] < vb[i]
		}
	}
	return false
}

// matchNode picks the newest installed version for a version file value,
// for example "20", "v20.11.1", "lts/iron", "lts/*" or "node".
func matchNode(spec string, installed []NodeVersion) (NodeVersion, bool) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	match := func(ok func(v semver) bool) (NodeVersion, bool) {
		for i := len(installed) - 1; i >= 0; i-- {
			if v, _ := parseVersion(installed[i].Version); ok(v) {
				return installed[i], true
			}
		}
		return NodeVersion{}, false
	}
	switch {
	case spec == "node" || spec == "latest" || spec == "current" || spec == "stable":
		return match(func(semver) bool { return true })
	case spec == "lts/*" || spec == "lts":
		// LTS releases have an even major version.
		return match(func(v semver) bool { return v[0]%2 == 0 })
	case strings.HasPrefix(spec, "lts/"):
		major, ok := ltsNames[strings.TrimPrefix(spec, "lts/")]
		return match(func(v semver) bool { return ok && v[0] == major })
	}
	parts := strings.Split(strings.TrimPrefix(spec, "v"), ".")
	return match(func(v semver) bool {
		for i, p := range parts {
			if i > 2 || fmt.Sprint(v[i]) != p {
				return false
			}
		}
		return true
	})
}

// NodeForDir picks the Node version for a folder: .nvmrc or .node-version,
// then "engines.node" in package.json, then the node on the PATH. It
// returns the version and the reason, for "benchtop which".
func NodeForDir(dir string) (NodeVersion, string, error) {
	installed := DiscoverNode()
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		for _, name := range []string{".nvmrc", ".node-version"} {
			data, err := os.ReadFile(filepath.Join(d, name))
			if err != nil {
				continue
			}
			spec := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
			if v, ok := matchNode(spec, installed); ok {
				return v, fmt.Sprintf("from %s (%s)", name, spec), nil
			}
			return NodeVersion{}, "", fmt.Errorf("%s asks for Node %s, which is not installed. Run: nvm install %s", filepath.Join(d, name), spec, spec)
		}
		if data, err := os.ReadFile(filepath.Join(d, "package.json")); err == nil {
			var pkg struct {
				Engines struct {
					Node string `json:"node"`
				} `json:"engines"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Engines.Node != "" {
				for i := len(installed) - 1; i >= 0; i-- {
					if v, _ := parseVersion(installed[i].Version); satisfies(pkg.Engines.Node, v) {
						return installed[i], fmt.Sprintf("newest that matches \"node\": %q in package.json", pkg.Engines.Node), nil
					}
				}
				return NodeVersion{}, "", fmt.Errorf("no installed Node matches %q in package.json", pkg.Engines.Node)
			}
			break // the project folder, with no Node version in it
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	if path, err := exec.LookPath("node"); err == nil {
		bin := filepath.Dir(path)
		for _, v := range installed {
			if v.Bin == bin {
				return v, "no .nvmrc here, so the node on your PATH", nil
			}
		}
		return NodeVersion{Bin: bin, From: "PATH"}, "no .nvmrc here, so the node on your PATH", nil
	}
	if len(installed) > 0 {
		return installed[len(installed)-1], "no .nvmrc here, so the newest Node", nil
	}
	return NodeVersion{}, "", fmt.Errorf("Node.js is not installed. Run: brew install node")
}

// NodeCommand builds the command for "benchtop node", "npm" or "npx". The
// version's folder comes first in PATH, so scripts that npm starts use the
// same Node.
func NodeCommand(dir, tool string, args []string) (*exec.Cmd, error) {
	v, _, err := NodeForDir(dir)
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(v.Bin, tool)
	if !exists(bin) {
		return nil, fmt.Errorf("%s is not in %s", tool, v.Bin)
	}
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), "PATH="+v.Bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return c, nil
}
