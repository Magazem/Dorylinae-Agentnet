# Release signing (owner, offline)

D36, OD-P4-19 (a) + (ii): the release signature is a plain Ed25519 signature over the
bytes of `SHA256SUMS`. The key is kept offline and is **never** in GitHub: CI builds and
publishes a **draft** release; the owner signs on their own machine, uploads the
signatures and publishes the draft. The tool is `tools/releasesign` (Go standard library
only; `go run ./tools/releasesign --help`).

## Once: make the key

```sh
go run ./tools/releasesign keygen -out agentnet-release.key
```

- Keep `agentnet-release.key` offline (e.g. an encrypted USB stick), plus a sealed backup
  copy somewhere else. The file is an unencrypted PKCS#8 PEM, so the medium must be
  encrypted. Anyone holding it can sign releases that `install.sh` accepts.
- Put the public half into the installer and commit it:

  ```sh
  go run ./tools/releasesign embed -key agentnet-release.key -in scripts/install.sh -out scripts/install.sh
  ```

  Until this is done, `install.sh` refuses to install anything (it holds a loud
  placeholder). Only public values are written.
- Review `AGENTNET_MIN_VERSION` in `scripts/install.sh` (initially `0.1.0`); raise it
  whenever a release must never be installed again.
- Publish the updated `install.sh` at `https://dorylinae.net/install.sh`.

If the key is lost or leaks: make a new one, embed it, publish the new `install.sh`, and
say so in the release notes. Installers already downloaded keep the old key.

## Every release

1. Update `CHANGELOG.md` (from ticket 4.7a), then push the tag `vX.Y.Z`
   (tags need the owner's OK). `release.yml` builds, tests `install.sh` (with a throwaway
   test key) and the Homebrew formula, and creates a **draft** release `vX.Y.Z` with six
   archives and `SHA256SUMS`.
2. On your machine, with the key available:

   ```sh
   gh release download vX.Y.Z -p SHA256SUMS
   go run ./tools/releasesign sign -key agentnet-release.key SHA256SUMS
   go run ./tools/releasesign verify -install-sh scripts/install.sh SHA256SUMS
   gh release upload vX.Y.Z SHA256SUMS.sig SHA256SUMS.minisig
   ```

   `sign` refuses a `SHA256SUMS` that is not exactly the six archives of one version.
   `verify` also checks that the two keys in `install.sh` are the same key.
   Before signing, look at the draft: the commit it was built from and the version.
3. Publish the draft on GitHub.
4. Homebrew: render the formula from the SHA256SUMS you just signed and commit it to the tap
   repository (`github.com/Magazem/homebrew-tap`, `Formula/agentnet.rb`):

   ```sh
   sh packaging/homebrew/render.sh X.Y.Z SHA256SUMS > agentnet.rb
   ```

Without OpenSSL 3 you can cross-check by hand: `openssl pkeyutl -sign -inkey
agentnet-release.key -rawin -in SHA256SUMS -out check.sig` produces the same bytes as
`SHA256SUMS.sig` (Ed25519 is deterministic).

## Dry run

`Actions → Release → Run workflow` with `dry_run` ticked and a `version` (default
`0.1.0`) creates only a draft **pre-release** named `dry-run-<run id>` with the six
archives and `SHA256SUMS`, plus the run's workflow artifacts (kept 7 days). A draft does
not create a git tag. Delete the draft afterwards (`gh release delete dry-run-<run id>`).
`dry_run` unticked is refused.
