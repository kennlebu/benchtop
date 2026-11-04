# Benchtop

A local development environment for PHP on macOS. Benchtop runs your web
server, PHP versions, databases and mail catcher from Homebrew, as normal
processes. There is no Docker and no virtual machine.

It is one Go binary with a command line and a web dashboard on
`http://127.0.0.1:7788`.

## Features

- **Sites** on Caddy (or Nginx behind Caddy), with HTTPS from a local CA.
  "Add site" finds `public/`, the domain and the PHP version.
- **Parked folders**: each project in a folder gets `<name>.test`.
- **Many PHP versions** at the same time (7.4 to 8.5), one per site.
  `benchtop php` and `benchtop composer` use the version of the site.
- **PHP extensions** (Xdebug, Redis, APCu, ...) with one click.
- **Node per project** from `.nvmrc`: `benchtop node`, `npm`, `npx`.
- **Databases and services**, many versions side by side: MySQL, MariaDB,
  PostgreSQL, MongoDB, Redis, Valkey, Memcached, Meilisearch, Typesense,
  SeaweedFS (S3) and Mailpit. Each card copies the Laravel `.env` lines.
- **Wildcard domains** and subdomains, from a small DNS server in Benchtop.
- **Pure-FTPd** with virtual users.
- **Updates**: a card shows when Homebrew has a newer version.
- **Start at login**, off by default.

## Install

Benchtop needs macOS and [Homebrew](https://brew.sh). After the first
release, it installs with `brew install kennlebu/tap/benchtop`. Until then,
build it from source:

```bash
brew install caddy php          # the minimum: a web server and one PHP
git clone https://github.com/kennlebu/benchtop
cd benchtop
make install                    # puts benchtop in ~/.local/bin (needs Go 1.23)
```

## Quick start

```bash
benchtop ui                     # open the dashboard
benchtop park ~/Projects        # every project in it gets <name>.test
benchtop start                  # start PHP for each site, then Caddy
```

Open `http://<project>.test`. The [getting started guide](docs/getting-started.md)
has the full steps.

## Docs

- [Getting started](docs/getting-started.md): install, first site, HTTPS, databases
- [Guide](docs/guide.md): every feature, and how Benchtop works
- [Moving from FlyEnv](docs/flyenv.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Contributing](CONTRIBUTING.md): build, test, and add a service
- [Changelog](CHANGELOG.md)

## License

MIT. See [LICENSE](LICENSE).
