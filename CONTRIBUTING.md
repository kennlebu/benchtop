# Contributing

Thank you for helping. Benchtop is small on purpose: one Go binary, the
standard library only, and one HTML file for the dashboard.

## Build and test

```bash
make build       # bin/benchtop
make test        # go vet and go test
make install     # ~/.local/bin/benchtop
```

CI runs `gofmt`, `go vet`, `go test` and a build on macOS for each push
and pull request.

### Test with a scratch home

Use `BENCHTOP_HOME` so a test does not touch your real setup:

```bash
BENCHTOP_HOME=/tmp/bt-test go run ./cmd/benchtop ui --port 7799
```

A scratch home gets its own socket names and its own `/etc/hosts` block,
so it cannot stop your real PHP or remove your real hosts entries. It still
uses the real ports 80, 443 and 21, so do not start Caddy or FTP in two
homes at the same time.

### Unit tests

Keep unit tests few. Test the logic that can break a real setup or lose
data: the config format, the hosts block, version and formula choice, port
choice, and parked sites. Check UI changes and simple helpers by hand.

## Code layout

| Path | Content |
| --- | --- |
| `cmd/benchtop/main.go` | The command line. |
| `internal/core/` | Everything that the command line and the dashboard share. |
| `internal/web/server.go` | The dashboard API and server-sent events. |
| `internal/web/static/index.html` | The dashboard: one file with HTML, CSS and JavaScript. |

In `internal/core`:

| File | Content |
| --- | --- |
| `manager.go`, `config.go` | The manager, the config file and its format version. |
| `sites.go`, `park.go`, `health.go`, `suggest.go` | Sites, parked folders, site hints and "Add site" suggestions. |
| `caddy.go`, `nginx.go`, `php.go`, `ftp.go`, `dns.go` | The core services. |
| `extra.go` | The interface for other services, and start and stop. |
| `mysql.go`, `services_more.go` | MySQL, MariaDB and PostgreSQL (with databases and users). |
| `simple.go` | Services that are only data. Start here to add a service. |
| `catalog.go`, `brew.go`, `upgrade.go`, `phpinstall.go` | Homebrew versions, installs and upgrades. |

## Add a service

Most services need no new code: add a `simpleService` in `simple.go`. For
example, Meilisearch:

```go
&simpleService{
	id: "meilisearch", name: "Meilisearch", kind: "database",
	formula: "meilisearch", binary: "bin/meilisearch", port: 7700,
	args: func(dir string, port int) []string {
		return []string{"--db-path", filepath.Join(dir, "data.ms"),
			"--http-addr", fmt.Sprintf("127.0.0.1:%d", port), "--env", "development"}
	},
	envText: func(port int) string {
		return fmt.Sprintf("SCOUT_DRIVER=meilisearch\nMEILISEARCH_HOST=http://127.0.0.1:%d\n", port)
	},
},
```

The fields:

| Field | Use |
| --- | --- |
| `id`, `name`, `kind` | The ID in commands, the card name, and the page (`database` or `mail`). |
| `formula`, `prefixName` | The Homebrew formula, and its folder in `opt` when that is another name (for tap formulas). |
| `binary`, `port` | The program inside the formula folder, and its usual port. |
| `args` | The command line. `dir` is the service folder in `~/.benchtop/services/<id>`. |
| `config`, `custom`, `customText` | A config file that Benchtop writes on each start, and a file for the user that it writes once. |
| `readyPort`, `readyURL` | How Benchtop knows the service is ready. The default is the port. |
| `started` | A step after the start, for example to make a bucket. |
| `envText`, `linkList`, `shellArgv`, `detailText` | The `.env` lines, card links, the shell command and the card text. |

Rules for a service:

- Listen on `127.0.0.1` only.
- Keep all data in `dir`.
- Run in the foreground. Benchtop runs it as a child process and writes its
  output to `logs/<id>.log`.

A service with many versions in Homebrew (like MySQL or Redis) goes in
`families()` in `catalog.go` instead. A service with databases and users
implements `databaseService` (see `mysql.go`).

## UI and text style

- Keep the dashboard simple: one card per service, advanced options in
  closed sections, and a fix button for each error.
- Colours must pass WCAG 2.1 AA: 4.5:1 for text, 3:1 for field borders and
  status dots. All colours are CSS variables in `:root`.
- Write short, plain sentences. Do not use "·" as a separator.
- Every dashboard action also needs a command.

## Pull requests

- Run `make test` and `gofmt` before you push.
- Add a line to `CHANGELOG.md` under the unreleased version.
- Describe how you tested the change.

Maintainers: [RELEASING.md](RELEASING.md) has the release steps.
