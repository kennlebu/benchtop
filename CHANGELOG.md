# Changelog

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
  now come from data-only definitions (RFC 0001, section 7.3).
- Wildcard domains: a small DNS server in Benchtop, with `/etc/resolver` files.
- Backup and restore: `benchtop backup|restore FILE` and Settings → Backup.
  FTP users get the user ID of this Mac.

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
