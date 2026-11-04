# Getting started

## 1. Install

Benchtop needs macOS and [Homebrew](https://brew.sh). Install a web server
and one PHP version:

```bash
brew install caddy php
```

Then build Benchtop (it needs Go 1.23 or later):

```bash
git clone https://github.com/kennlebu/benchtop
cd benchtop
make install
```

`make install` puts `benchtop` in `~/.local/bin`. Make sure that folder is in
your `PATH`.

## 2. Open the dashboard

```bash
benchtop ui
```

The dashboard opens on `http://127.0.0.1:7788`. To keep it up after you
close the terminal, use `benchtop ui --background`. To stop it, use
`benchtop ui --stop`. Your services keep running when the dashboard stops.

## 3. Add your first site

You can add one site or a folder of projects.

**One site.** On the Sites page, click **Add site** and choose the project
folder. Benchtop suggests:

- the root: `public/` if the project has it
- the domain: `<folder>.test`
- the PHP version: from `.php-version`, then `composer.json`

Click **Save**. Benchtop asks for your password one time, to add the domain
to `/etc/hosts`.

**A folder of projects.** Click **Park a folder** and choose the folder, for
example `~/Projects`. Each project folder in it gets `<name>.test`. New
folders get a site by themselves.

From the command line:

```bash
benchtop site add myapp.test ~/Projects/myapp/public --php 8.3
benchtop park ~/Projects
```

## 4. Start the services

Click **Start all** in the dashboard, or run:

```bash
benchtop start
```

This starts the PHP versions that your sites use, then Caddy. Open
`http://myapp.test`. If a site shows a problem, its row has a hint with a
fix button.

## 5. HTTPS (optional)

Turn on **Use HTTPS** in the site form (under Advanced). Then trust the
Benchtop certificate one time:

```bash
benchtop trust
```

Restart your browser after that.

## 6. A database (optional)

On the Databases page, choose a version and click **Install**. Homebrew
installs it. Then click **Start** and **New database**. **Copy .env** gives
the lines for your Laravel `.env` file, with the password from your
Keychain.

```bash
benchtop install mysql-8.4
benchtop start mysql-8.4
benchtop db create mysql-8.4 myapp
benchtop db env mysql-8.4
```

## 7. Use the right PHP and Node in the terminal

```bash
benchtop php artisan migrate    # the PHP version of the site in this folder
benchtop composer install
benchtop npm run dev            # the Node version from .nvmrc
benchtop which                  # shows the choice and the reason
```

You can add aliases to your shell:

```bash
alias php="benchtop php"
alias composer="benchtop composer"
```

## Next

- [Guide](guide.md): all features
- [Troubleshooting](troubleshooting.md)
