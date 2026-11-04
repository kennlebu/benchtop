# Changelog

## v1.0.0 (unreleased)

### Added

- `config.json` has a format `version`. Benchtop moves an older config to
  the current format and keeps the old file as `config.json.v<N>.bak`. It
  refuses a config from a newer Benchtop, so a save cannot drop fields.
- Docs: a short README, plus `docs/getting-started.md`, `docs/guide.md`,
  `docs/flyenv.md`, `docs/troubleshooting.md` and `CONTRIBUTING.md` (with
  how to add a service).
- `benchtop version`, and the version on the Settings page.
- Release files: `make dist` builds Benchtop for Apple Silicon and Intel
  Macs. A version tag starts a GitHub workflow that makes the release. A
  Homebrew formula that builds from source is in `packaging/homebrew`.

## v0.5.0 (unreleased)

### Added

- Version picker: a card for a service that is not installed has a version
  menu. An installed card has "Install another version" in its menu.
  Versions run side by side, each with its own port and data folder.
- The versions come from Homebrew. The dashboard asks it once a day.
  Benchtop hides versions that Homebrew has turned off, and very old ones.
- Updates: a card shows "Update available" when Homebrew has a newer
  version, with an Upgrade button. Benchtop restarts the service after the
  upgrade if it ran. Settings has "Check for updates", and the command
  line has `benchtop upgrade SERVICE`. A new build of the same version
  (a Homebrew revision) does not count as an update.
- Install PHP from the dashboard: an "Add a PHP version" card on the
  Services page, and `benchtop install php-8.4`. Benchtop uses Homebrew
  core when it has the version, and the `shivammathur/php` tap for older
  versions (7.4, 8.0) and previews. A site that needs a missing PHP
  version gets an Install button on its PHP card.
- Parked folders: "Park a folder" on the Sites page, and
  `benchtop park|unpark FOLDER`. Each project folder in it gets
  `<name>.test`, with `public/` and the PHP version found by Benchtop. The
  dashboard finds new folders every 5 seconds and reloads Caddy. A parked
  site that you edit becomes a saved site with your settings.
- Subdomains: a site option (Advanced, or `--subdomains`) that sends
  `*.domain` to the site too, in Caddy and in Nginx. Parked sites have it
  on. Subdomains need the wildcard DNS; a site hint offers to turn it on.
- `.php-version`: Benchtop reads it before `composer.json`, for new
  sites, parked sites, and `benchtop php`, `composer` and `which`. A site
  that uses another version gets a hint with a "Use PHP X" button.
- Node for each project: `benchtop node`, `npm` and `npx` use the version
  from `.nvmrc` or `.node-version` (also `lts/*` and names such as
  `lts/iron`), then `engines.node` in `package.json`, then the `node` on
  your PATH. Benchtop finds Node from nvm, fnm and Homebrew. `benchtop
  which` shows the choice.
- More services: Valkey, Meilisearch, Typesense and SeaweedFS (S3
  storage, with a `local` bucket). Each card has "Copy .env" with the
  Laravel settings. Typesense comes from the `typesense/tap` tap, which
  Homebrew must trust first (`brew trust typesense/tap`).
- A failed install shows the reason on the card and in the command line,
  for example the `brew trust` command for a tap that is not trusted.
- A site hint when a domain is not in `/etc/hosts`, with a "Sync
  /etc/hosts" button. A new parked folder never opens a password dialog
  by itself.
- An open dashboard page reloads by itself when the dashboard restarts.
- More versions: MySQL 9.7 and the newest MySQL, MariaDB 10.11 to 13.0,
  PostgreSQL 18, Redis 8.2, MongoDB 7.0 to 8.3.

### Changed

- The newest MySQL, MariaDB, Redis and MongoDB (the formula without "@")
  have the service ID `mysql`, `mariadb`, `redis` and `mongodb`, so an
  upgrade keeps the same data folder.
- A new service version does not take a port that another version saved.
- `benchtop install` and `benchtop ext install` wait for the end of the
  install. Before, the install stopped when the command ended.

## v0.4.0 (unreleased)

### Added

- PHP extensions: an "Extensions" dialog on each PHP card, and
  `benchtop ext list|install|on|off`. PECL installs in the background.
  Benchtop turns extensions on with its own file in `conf.d` and does not
  change `php.ini`. Xdebug starts only on a trigger, so normal requests
  stay fast.
- Per-site PHP: `benchtop php`, `benchtop composer` and `benchtop which`.
  `benchtop versions` now lists the PHP versions (it was `benchtop php`).
- Nginx as the web server of a site, behind Caddy. `benchtop site add ... --server nginx`.
- MariaDB, Memcached and MongoDB. Redis, Memcached, MongoDB and Mailpit
  now come from data-only definitions.
- Wildcard domains: a small DNS server in Benchtop, with `/etc/resolver` files.
- Backup and restore: `benchtop backup|restore FILE` and Settings → Backup.
  FTP users get the user ID of this Mac.

### Changed

- New dashboard font (Avenir Next) and monospace font (SF Mono).
- Colours now meet WCAG 2.1 AA in light and dark mode. Field borders are
  darker, and the dark mode buttons have dark text.
- Status text uses commas in place of "·".

## v0.3.0 (unreleased)

### Added

- Databases page: MySQL (8.4, 8.0, newest), PostgreSQL (14 to 17) and Redis.
  Create databases and users, copy `.env` lines, open a shell, edit the
  config, change the port.
- Mail page: Mailpit, with the inbox link and the `MAIL_*` lines.
- "Install with Homebrew" for services that are not installed, with live
  output in the "Homebrew installs" log.
- "Use FlyEnv data" for MySQL and PostgreSQL: uses the FlyEnv data folder
  where it is.
- Root passwords for new databases go in the macOS Keychain, one for each
  data folder.
- `benchtop install` and `benchtop db list|create|user|root-password|env|shell`.
- "Start all" and start at login start the databases and Mailpit that were
  on last time.

### Fixed

- A second Benchtop home (for example a test copy with `BENCHTOP_HOME`)
  now uses its own PHP and database sockets and its own `/etc/hosts` block.
  Before, it could remove the sockets or the hosts entries of the main setup.
- PHP-FPM does not start when another PHP-FPM already listens on its socket.

## v0.2.0 (unreleased)

### Changed

- The project is now called **Benchtop**. The command is `benchtop`.
- The data folder moves from `~/.devcenter` to `~/.benchtop` on the first
  run. A link stays at the old path, so running services keep working.
  `BENCHTOP_HOME` replaces `DEVCENTER_HOME`.
- PHP sockets are now `/tmp/benchtop-php<version>.sock`.
- New dashboard layout: a left navigation with Sites, Services, FTP, Logs
  and Settings.

### Added

- Optional start at login (off by default), with a choice of what starts.
- `benchtop ui --background` and `benchtop ui --stop`.
- "Add site" suggests the root (`public/`), the domain and the PHP version
  (from `composer.json`).
- Site health hints with one-click fixes, for example "Use public/" or
  "Start PHP 8.3".
- Quick actions: open in the browser, Finder, editor or terminal.
- Live status and live logs, with a short format for Caddy JSON logs.
- Open `php.ini`, `php-fpm.conf` and other config files from a service card.
- Settings page: start at login, editor, terminal, ports, data folder, tools.
- MIT license and a CI workflow.

## v0.1.0 (2026-11-02)

- First version: Caddy sites, PHP-FPM 7.4 to 8.5, Pure-FTPd users,
  `/etc/hosts` block, FlyEnv import, command line and web dashboard.
