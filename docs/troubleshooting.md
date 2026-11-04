# Troubleshooting

Start with the site hints on the Sites page and with the Logs page. Most
problems show a cause and a fix button there. From the command line:

```bash
benchtop status
benchtop logs caddy        # or php-8.2, ftp, mysql-8.4, brew, pecl, ...
```

## A site gives 502 Bad Gateway

The PHP version of the site is stopped. The site row has a
**Start PHP x.y** button. For an Nginx site, Nginx may be stopped.

## A site gives 404 Not Found

The root folder has no index file. For Laravel and similar apps, the root
must be the `public/` folder. The site row has a **Use public/** button.

## The browser cannot find the domain

The domain is not in `/etc/hosts`. Click **Sync /etc/hosts** on the site row,
or run `benchtop hosts`. To stop the problem for good, start
**Wildcard domains** on the Services page.

Subdomains (`api.acme.test`) always need wildcard domains, because
`/etc/hosts` cannot hold wildcard names.

## The browser warns about the HTTPS certificate

Run `benchtop trust` one time, then restart the browser. Firefox uses its
own certificate store: turn on `security.enterprise_roots.enabled` in
`about:config`.

## A port is in use

- **Ports 80, 443 or 21**: another app holds the port, for example FlyEnv,
  MAMP or another web server. The error has a **Stop it and start** button,
  or run `benchtop start caddy --force`.
- **A database port**: a service takes the next free port the first time it
  starts, for example 3307 when Docker or OrbStack holds 3306. The card
  shows the port, and **Copy .env** has it.

## An install fails

The card shows the reason, and the full output is in the `brew` log.

- **"Homebrew does not trust the tap"**: Homebrew asks you to approve
  formulas from other taps. If you trust the tap, run the `brew trust`
  command from the message, then install again.
- **A PECL extension**: the output is in the `pecl` log. Some extensions
  need a library first, for example `brew install imagemagick` for Imagick.

## MySQL says "Access denied"

The root password in your Keychain does not match the data folder. This
happens with **Use FlyEnv data**. Use **Set root password** in the "⋯" menu
and type the real root password.

## A service starts, then stops

Open its log on the Logs page. Two common causes:

- The data folder belongs to another version, for example PostgreSQL 15
  data with PostgreSQL 16. Use the version that made the data.
- Another copy of the service already uses the data folder, for example
  from `brew services`. Stop that copy first.

## "config.json is from a newer Benchtop"

A newer Benchtop wrote the config. This build refuses it, so a save cannot
drop the newer settings. Update Benchtop. To go back to an older config,
use the `config.json.v<N>.bak` file in `~/.benchtop`.

## The dashboard does not open

- Check that it runs: `benchtop ui --background` starts it, and
  `curl -s http://127.0.0.1:7788` must answer.
- `benchtop logs dashboard` shows its errors (the file is
  `~/.benchtop/logs/ui.log`).
- Port 7788 can be in use: `benchtop ui --port 7790`.

## The FTP password dialog shows often

Pure-FTPd must run as root, so each start and stop of FTP asks for your
password. Other services never ask.
