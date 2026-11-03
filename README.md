# Benchtop

A small local development environment manager for macOS. It runs
**Caddy**, **PHP-FPM** (many versions at the same time), **Pure-FTPd**,
**Nginx**, **MySQL**, **MariaDB**, **PostgreSQL**, **Redis**, **Valkey**,
**Memcached**, **MongoDB**, **Meilisearch**, **Typesense**, **SeaweedFS** (S3)
and **Mailpit** from Homebrew.

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
benchtop install redis     # install a service with Homebrew
benchtop install mysql-8.0 # install another version, next to the others
benchtop upgrade caddy     # upgrade with Homebrew, then restart it
benchtop install php-8.4   # add a PHP version
benchtop park ~/Projects   # each project in it gets <name>.test
benchtop npm run dev       # npm with the Node version from .nvmrc
benchtop site add acme.test ~/Projects/acme/public --subdomains  # *.acme.test too
benchtop start mysql-8.4
benchtop db create mysql-8.4 myapp
benchtop db env mysql-8.4  # .env lines for Laravel
benchtop php artisan migrate   # the PHP version of the site in this folder
benchtop composer install
benchtop ext install 8.2 xdebug
benchtop backup ~/benchtop-backup.json
benchtop help              # all commands
```

When you stop the dashboard or the terminal, the services keep running.

### Dashboard

- **Sites**: each site shows a status dot and its PHP version. A hint shows
  the likely cause of a 404 or a 502, with a fix button. "Add site"
  suggests the root (`public/`), the domain and the PHP version from
  `composer.json`.
- **Services**: start, stop and restart each service. Open `php.ini`,
  `php-fpm.conf` and other config files from the "⋯" menu. "Extensions"
  on a PHP card installs PECL extensions (Xdebug, Redis, APCu, ...) and
  turns them on or off.
- **Databases**: MySQL, PostgreSQL and Redis. Create databases and users,
  copy the `.env` lines, open a shell. A service that is not installed has
  an "Install with Homebrew" button.
- **Mail**: Mailpit catches the mail that your apps send. Open the inbox
  and copy the `MAIL_*` lines.
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
| `services/<id>/` | Config and data for databases, Redis and Mailpit. |
| `logs/` | Caddy, site, FTP, service, Homebrew, dashboard and login logs. |

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
- **Databases, Redis, Mailpit** run as your user. Each one gets the first
  free port from its usual port (3306, 5432, 6379, 1025), so a Docker
  database on the same port does not block it. "Start all" starts the ones
  that were on last time.
  - A new MySQL or PostgreSQL gets a random root password, which goes in
    your login Keychain. "Copy .env" includes it.
  - "Use FlyEnv data" runs MySQL or PostgreSQL on the FlyEnv data folder
    where it is. Benchtop does not copy it. Stop it in FlyEnv first.
- **/etc/hosts**: Benchtop keeps a marked block with the site domains.
  `*.localhost` domains need no entry.

Root tasks (hosts file, FTP start and stop, trusting the HTTPS CA) show the
macOS password dialog. Nothing else runs as root.

The dashboard listens on 127.0.0.1 only. It refuses requests with another
`Host` header, and write requests need an `X-Benchtop` header, so other
web sites cannot control it.

## Per-site PHP on the command line

`benchtop php` and `benchtop composer` use the PHP version of the site
that holds the current folder. Then `composer`, `artisan` and the web server
use the same version. With no site, Benchtop reads `composer.json`, and then
uses the newest PHP. `benchtop which` shows the choice. You can add aliases:

```bash
alias php="benchtop php"
alias composer="benchtop composer"
```

## Nginx

A site can use Nginx instead of Caddy (site form → Advanced → Web server).
Caddy keeps ports 80 and 443 and sends the requests for that site to Nginx on
`127.0.0.1:8081`. For an Nginx site, the "Extra" field holds Nginx text, for
example `location` blocks.

## Wildcard domains

"Wildcard domains" on the Services page runs a small DNS server in Benchtop.
It answers `127.0.0.1` for every name that ends in `.test` (or the endings
that you choose). The first start writes `/etc/resolver/test` with your
password. After that, new sites under those endings need no `/etc/hosts`
change and no password. Stop it to remove the resolver files.

## Backup

`benchtop backup FILE` (or Settings → Backup) saves the sites, the settings
and the FTP users. `benchtop restore FILE` adds them on another Mac. The file
has no database data and no Keychain passwords.

## HTTPS

Turn on HTTPS for a site, start Caddy, then run `benchtop trust` one time
to add the Benchtop local CA to the system keychain.

## Roadmap

1. v0.2: optional start at login, smart "Add site", live logs, new layout (done)
2. v0.3: MySQL, Redis, PostgreSQL, Mailpit (done)
3. v0.4: PHP extensions, Nginx, MariaDB, MongoDB, wildcard domains (done)
4. v0.5: version picker, PHP installs, parked folders, Node per project
5. v1.0: Homebrew tap, open-source release
6. Later: native app

## License

MIT. See [LICENSE](LICENSE).
