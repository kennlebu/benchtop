# Releasing

1. Move the "unreleased" section in `CHANGELOG.md` to the new version, for
   example `## v1.0.0 (2026-11-10)`. Commit it.
2. Tag and push:

   ```bash
   git tag v1.0.0
   git push origin main v1.0.0
   ```

   The Release workflow runs the tests, builds the files for Apple Silicon
   and Intel Macs, and makes a GitHub release with the changelog section.

3. Update the Homebrew formula in the tap repository
   (`kennlebu/homebrew-tap`, file `Formula/benchtop.rb`). Start from
   `packaging/homebrew/benchtop.rb`, set the `url` to the new tag, and set
   the `sha256` of the source tarball:

   ```bash
   curl -sL https://github.com/kennlebu/benchtop/archive/refs/tags/v1.0.0.tar.gz | shasum -a 256
   ```

4. Check the formula, then push the tap:

   ```bash
   brew install --build-from-source kennlebu/tap/benchtop
   brew test kennlebu/tap/benchtop
   ```

Users then install with `brew install kennlebu/tap/benchtop`.

## Version numbers

`make build` takes the version from the newest git tag (`git describe`).
`benchtop version` and the Settings page show it. A build with no tag shows
the commit ID.
