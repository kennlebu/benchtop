# Changelog

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
