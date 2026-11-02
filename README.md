# Benchtop

A small local development environment manager for macOS. It runs
**Caddy**, **PHP-FPM** (many versions at the same time) and **Pure-FTPd**
from Homebrew. It is a lightweight alternative to FlyEnv, for the services
I use every day.

It is one Go binary with no dependencies. It has a command line and a
local web dashboard.

## Requirements

- macOS (Apple Silicon or Intel)
- Homebrew packages: `caddy`, `pure-ftpd`, and one or more of `php`, `php@8.3`, `php@8.2`, `php@7.4`, ...
- Go 1.23 or later, to build

## Install

```bash
make install        # puts benchtop in ~/.local/bin
```

## Use

```bash
benchtop import-flyenv     # one time: copy sites and FTP users from FlyEnv
benchtop start             # start PHP for each site, Caddy, and FTP
benchtop ui                # open the dashboard on http://127.0.0.1:7788
benchtop ui --background   # keep the dashboard up after the terminal closes
benchtop status
benchtop site add myapp.test ~/Projects/myapp/public --php 8.3
benchtop ftp add myuser ~/Projects/myapp/ftp
benchtop help              # all commands
```

When you stop the dashboard or the terminal, the services keep running.

### Dashboard

- **Sites**: each site shows a status dot and its PHP version. A hint shows
  the likely cause of a 404 or a 502, with a fix button. "Add site"
  suggests the root (`public/`), the domain and the PHP version from
  `composer.json`.
- **Services**: start, stop and restart each service. Open `php.ini`,
  `php-fpm.conf` and other config files from the "⋯" menu.
- **Logs**: live logs for Caddy, each site, each PHP version and FTP.
- **Settings**: start at login, the editor and terminal apps, ports, and
  tools (FlyEnv import, hosts sync, HTTPS trust).

### Start at login

Start at login is off by default. Turn it on in Settings, and choose what
starts. Benchtop then adds `~/Library/LaunchAgents/dev.benchtop.login.plist`,
which runs `benchtop start --login` one time when you log in. Turn it off
to remove the file.

## How it works

All data is in `~/.benchtop` (set `BENCHTOP_HOME` to change it):

| Path | Content |
| --- | --- |
| `config.json` | Sites and settings. This is the source of truth. |
| `caddy/Caddyfile` | Made from `config.json`. Do not edit. Use the site "extra" field. |
| `caddy/data` | Caddy certificates and the local CA. |
| `php/<version>/php-fpm.conf` | Made once. You can edit it. |
| `ftp/pureftpd.passwd` | FTP virtual users, managed with `pure-pw`. |
| `logs/` | Caddy, site, FTP, dashboard and login logs. |

Before v0.2 the project was called DevCenter. On the first run, Benchtop
moves `~/.devcenter` to `~/.benchtop` and leaves a link at the old path.

- **Caddy** runs as your user. It listens on ports 80 and 443 (macOS lets
  normal users use these ports). Its admin API is on `localhost:2099`.
  Site changes reload Caddy with no downtime.
- **PHP-FPM**: each version runs as your user and listens on
  `/tmp/benchtop-php<version>.sock`, for example `/tmp/benchtop-php82.sock`.
  Only versions that a site uses start with `benchtop start`.
- **Pure-FTPd** must run as root, because it uses port 21 and chroot. FTP
  users are virtual users. Their files belong to your macOS user.
- **/etc/hosts**: Benchtop keeps a marked block with the site domains.
  `*.localhost` domains need no entry.

Root tasks (hosts file, FTP start and stop, trusting the HTTPS CA) show the
macOS password dialog. Nothing else runs as root.

The dashboard listens on 127.0.0.1 only. It refuses requests with another
`Host` header, and write requests need an `X-Benchtop` header, so other
web sites cannot control it.

## HTTPS

Turn on HTTPS for a site, start Caddy, then run `benchtop trust` one time
to add the Benchtop local CA to the system keychain.

## Roadmap

See [RFC 0001: Benchtop roadmap](docs/rfcs/0001-roadmap.md). In short:

1. v0.2: optional start at login, smart "Add site", live logs, new layout
2. v0.3: MySQL, Redis, PostgreSQL, Mailpit
3. v0.4: PHP extensions, Nginx, more FlyEnv services
4. v1.0: Homebrew tap, open-source release
5. Later: native app

## License

MIT. See [LICENSE](LICENSE).
