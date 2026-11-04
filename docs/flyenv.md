# Moving from FlyEnv

Benchtop can import your Caddy sites and FTP users from FlyEnv. It reads
`~/Library/PhpWebStudy` and does not change it.

## Sites and FTP users

1. Stop FlyEnv, so the two apps do not fight for ports 80, 443 and 21.
2. In Benchtop, open Settings and click **Import from FlyEnv**, or run:

   ```bash
   benchtop import-flyenv
   ```

3. Read the report. Benchtop skips sites and users that already exist, and
   domains that are not valid. FTP users get the user ID of your macOS user.
   The report warns when an FTP folder does not exist.
4. Click **Start all**, or run `benchtop start`.

If FlyEnv still holds a port, start with `--force`:

```bash
benchtop start caddy --force
```

## Databases

Benchtop uses its own data folder for each database by default. To keep
your FlyEnv data, open the "⋯" menu of the MySQL or PostgreSQL card and
choose **Use FlyEnv data**. Benchtop then runs the database on the FlyEnv
data folder where it is. It does not copy it.

- Do not run the same database in FlyEnv at the same time.
- After the switch, use **Set root password** with the root password that
  you used in FlyEnv. Benchtop keeps it in your Keychain.
- **Use Benchtop data folder** switches back. The FlyEnv data stays where it
  is.
