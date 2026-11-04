# Guide

## Dashboard

- **Sites**: each site shows a status dot, its PHP version and badges
  (HTTPS, Subdomains, Parked, Nginx). A hint shows the likely cause of a
  404 or a 502, with a fix button. The "⋯" menu opens the project in your
  editor or terminal, and shows the access log.
- **Services**: Caddy, each PHP version, Pure-FTPd, wildcard domains and
  Nginx. Start, stop and restart each one. The "⋯" menu opens the config
  files and the log. "Add a PHP version" installs another PHP.
- **Databases**: databases, caches, search and storage. Choose a version and
  install it. Create databases and users, copy the `.env` lines, and open a
  shell.
- **Mail**: Mailpit catches the mail that your apps send.
- **FTP**: virtual FTP users, each with a home folder.
- **Logs**: live logs for Caddy, each site, each PHP version and each service.
- **Settings**: start at login, the editor and terminal apps, ports, tools,
  and backup.

Every action in the dashboard also has a command. Run `benchtop help` for
the list.

## Sites

A site has one or more domains, a root folder, a PHP version (or none, for
static files) and options:

- **HTTPS**: Caddy makes a certificate from the Benchtop local CA. Run
  `benchtop trust` one time.
- **Subdomains**: `*.acme.test` goes to `acme.test` too. This needs
  wildcard domains.
- **Web server**: Caddy (the default) or Nginx.
- **Extra**: text that goes into the site block, for Caddy or for Nginx.

### Parked folders

Park a folder (`benchtop park ~/Projects`) and each project folder in it
gets `<name>.test`, with subdomains on. Benchtop checks the folders every 5
seconds. It skips hidden folders, `node_modules` and `vendor`.

A parked site is not saved in the config. If you edit it, Benchtop saves it
as a normal site with your settings. A saved site for the same project or
domain always wins.

A new parked folder never opens a password dialog by itself. If its domain
is not in `/etc/hosts`, the site row shows a **Sync /etc/hosts** button. With
wildcard domains on, no hosts change is necessary.

### Nginx

Caddy keeps ports 80 and 443 and sends the requests for an Nginx site to
Nginx on `127.0.0.1:8081`. For an Nginx site, the Extra field holds Nginx
text, for example `location` blocks.

### Wildcard domains

"Wildcard domains" on the Services page runs a small DNS server in Benchtop.
It answers `127.0.0.1` for every name that ends in `.test`, or in the
endings that you choose. The first start writes `/etc/resolver/test` with
your password. After that, new sites under those endings need no
`/etc/hosts` change. Stop it to remove the resolver files.

## PHP

Each PHP version runs its own PHP-FPM on
`/tmp/benchtop-php<version>.sock`, for example `/tmp/benchtop-php82.sock`.
`benchtop start` starts only the versions that a site uses.

- **Install a version**: "Add a PHP version" on the Services page, or
  `benchtop install php-8.4`. Benchtop uses Homebrew core first, and the
  `shivammathur/php` tap for older versions (7.4, 8.0) and previews.
- **Extensions**: the "Extensions" item in the "⋯" menu of a PHP card, or
  `benchtop ext install 8.2 xdebug`. Benchtop turns an extension on with its
  own file in `conf.d` and never changes `php.ini`. Xdebug starts only when
  a request asks for it, so normal requests stay fast.
- **Config files**: open `php.ini` and `php-fpm.conf` from the "⋯" menu.
  Restart that PHP version after a change.

### Which PHP runs in the terminal

`benchtop php`, `benchtop composer` and `benchtop which` choose in this
order:

1. The site that holds the current folder.
2. `.php-version` in the project, for example `8.2`.
3. `composer.json`: `config.platform.php`, then the oldest installed version
   that matches `require.php`.
4. The newest installed PHP.

## Node

`benchtop node`, `benchtop npm` and `benchtop npx` choose in this order:

1. `.nvmrc` or `.node-version`. Values such as `20`, `v20.11.1`, `lts/*` and
   `lts/iron` work.
2. `engines.node` in `package.json`: the newest installed version that
   matches.
3. The `node` on your `PATH`.

Benchtop finds Node from nvm, fnm and Homebrew. It puts the chosen version
first in `PATH`, so the scripts that npm starts use the same Node.

## Services

| Service | Usual port | Notes |
| --- | --- | --- |
| MySQL, MariaDB | 3306 | Many versions side by side. A random root password goes in your Keychain. |
| PostgreSQL | 5432 | Many versions side by side. |
| MongoDB | 27017 | From the `mongodb/brew` tap. |
| Redis, Valkey | 6379 | Valkey is the open-source Redis. |
| Memcached | 11211 | |
| Meilisearch | 7700 | Development mode, no key. |
| Typesense | 8108 | From the `typesense/tap` tap. The API key is `benchtop`. |
| SeaweedFS | 8333 | S3 storage with a `local` bucket. Any key works. |
| Mailpit | 1025 | The inbox is on the SMTP port plus 7000 (8025). |

If the usual port is in use, for example by Docker, a service gets the
next free port the first time it starts, and keeps it. Two versions of a
service never get the same port. **Change port** in the "⋯" menu sets
another one.

"Start all" and start at login start the services that were running when
you last stopped them.

### Versions and updates

A service that is not installed has a version menu. An installed card has
**Install another version** in its "⋯" menu. Benchtop asks Homebrew for the
versions once a day.

When Homebrew has a newer version, the card shows **Update available** and
an **Upgrade** button. Benchtop upgrades with Homebrew and restarts the
service if it was running. A new build of the same version does not count.
Settings has **Check for updates**.

## Start at login

Start at login is off by default. Turn it on in Settings and choose what
starts. Benchtop then adds `~/Library/LaunchAgents/dev.benchtop.login.plist`,
which runs `benchtop start --login` one time when you log in. Turn it off
to remove the file.

## Backup

`benchtop backup FILE` (or Settings, Backup) saves the sites, the settings
and the FTP users. `benchtop restore FILE` adds them on another Mac. The file
has no database data and no Keychain passwords.

## How it works

All data is in `~/.benchtop`. Set `BENCHTOP_HOME` to use another folder.

| Path | Content |
| --- | --- |
| `config.json` | Sites and settings. This is the source of truth. |
| `caddy/Caddyfile` | Made from `config.json`. Do not edit it. Use the Extra field of a site. |
| `caddy/data` | Caddy certificates and the local CA. |
| `php/<version>/php-fpm.conf` | Made once. You can edit it. |
| `ftp/pureftpd.passwd` | FTP virtual users, managed with `pure-pw`. |
| `services/<id>/` | Config and data for each database and service. |
| `logs/` | Caddy, site, FTP, service, Homebrew, PECL, dashboard and login logs. |

`config.json` has a format `version`. A newer Benchtop moves an older config
to its format and keeps the old file as `config.json.v<N>.bak`.

- **Caddy** runs as your user on ports 80 and 443 (macOS lets normal users
  use these ports). Its admin API is on `localhost:2099`. Site changes
  reload Caddy with no downtime.
- **PHP-FPM, databases and services** run as your user.
- **Pure-FTPd** runs as root, because it uses port 21 and chroot. FTP users
  are virtual users. Their files belong to your macOS user.
- **/etc/hosts**: Benchtop keeps a marked block with the site domains.
  `*.localhost` domains need no entry.

Only these tasks need your password, through the macOS password dialog:
changes to `/etc/hosts`, starting and stopping FTP, trusting the HTTPS CA,
and the `/etc/resolver` files for wildcard domains.

The dashboard listens on `127.0.0.1` only. It refuses requests with another
`Host` header, and write requests need an `X-Benchtop` header, so other web
sites cannot control it.
