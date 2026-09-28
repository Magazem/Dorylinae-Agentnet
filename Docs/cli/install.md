# Installing AgentNet

Releases are built by `.github/workflows/release.yml` for macOS, Linux and Windows on
amd64 and arm64. Each release has six archives and a `SHA256SUMS` file, signed offline by
the maintainer (`SHA256SUMS.sig`, `SHA256SUMS.minisig`; see
[../ops/release-signing.md](../ops/release-signing.md)).

| Archive | Contents |
|---------|----------|
| `agentnet_X.Y.Z_darwin_{amd64,arm64}.tar.gz`, `agentnet_X.Y.Z_linux_{amd64,arm64}.tar.gz` | `agentnet`, `agentnetd`, `relay`, `LICENSE`, `README.md` |
| `agentnet_X.Y.Z_windows_{amd64,arm64}.zip` | the same, as `.exe` |

Release builds never include the `testhooks` build tag (the workflow checks every binary).

## macOS and Linux: install.sh

```sh
curl -fsSL https://dorylinae.net/install.sh | sh
curl -fsSL https://dorylinae.net/install.sh | sh -s -- --version 1.2.3   # a given release
```

It installs `agentnet` and `agentnetd` into `~/.local/bin` (or `--dir DIR`,
`$AGENTNET_INSTALL_DIR`). It never uses sudo. Then run `agentnet setup`. If
`~/.local/bin` is not on your `PATH`, the script says how to add it.

Before installing anything the script:

1. checks the Ed25519 signature on `SHA256SUMS` against the public key embedded in the
   script, with `openssl pkeyutl -verify -rawin` when OpenSSL 3 or newer is installed,
   else with `minisign -V` (the `.minisig` file). Stock macOS has LibreSSL, which cannot
   check Ed25519: install minisign (`brew install minisign`) or use Homebrew below. With
   neither, the script **stops** and prints the Homebrew command and the manual steps. It
   never installs an unsigned release;
2. checks that `SHA256SUMS` names exactly one version, that it is the version asked for
   (if any), and that it is not older than the minimum embedded in the script (so whoever
   serves the files cannot roll you back to a known-bad release);
3. checks the archive's SHA-256 against its line in `SHA256SUMS`.

Any failure exits 1 with "nothing was installed". Other environment variables:
`AGENTNET_VERSION` (like `--version`), `AGENTNET_DOWNLOAD_URL` (a mirror directory holding
the release files; the signature is still checked), `AGENTNET_ALLOW_INSECURE_URL=1` (allow a
non-https mirror; for tests).

**What this protects against, honestly.** The script is served from `dorylinae.net`, so
whoever controls that domain or its host controls the embedded key and every
`curl | sh` install. The signature protects against a swap of the release files on
GitHub (a leaked token, a compromised CI step), because the signing key is kept offline and
is never in GitHub.

## macOS and Linux: Homebrew

```sh
brew install magazem/tap/agentnet
```

Homebrew checks the SHA-256 values in the tap's formula, not the release signature: you
trust the tap repository (`github.com/Magazem/homebrew-tap`). The maintainer renders the
formula from a signature-verified `SHA256SUMS`.

## Manual install (any OS)

1. Download `SHA256SUMS`, `SHA256SUMS.minisig` (or `.sig`) and your archive from the
   release page.
2. Check the signature: `minisign -Vm SHA256SUMS -x SHA256SUMS.minisig -P <key>` with the
   key from `scripts/install.sh` (`AGENTNET_MINISIGN_PUBKEY`), or with OpenSSL 3:
   `openssl pkeyutl -verify -pubin -inkey release.pub -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig`
   with the PEM key (`AGENTNET_PUBKEY_PEM`) saved as `release.pub`.
3. Check the archive: `sha256sum -c --ignore-missing SHA256SUMS` (macOS:
   `shasum -a 256 -c --ignore-missing SHA256SUMS`; Windows PowerShell:
   `Get-FileHash <archive>` and compare).
4. Unpack and put `agentnet` and `agentnetd` on your `PATH`.

## Windows

Download the `windows` zip for your CPU and follow the manual steps. A per-user MSI (no
elevation) is ticket 4.4b. Windows builds are not code-signed in wave 1 (D36): SmartScreen
may ask you to confirm the first run ("More info", then "Run anyway").

## Version checks after install

A relay may announce the oldest daemon release it supports (`ready.min_client`,
[../protocol/envelope.md](../protocol/envelope.md#ready-relay---daemon)). An older daemon
keeps working, but logs a warning, `agentnet status` prints an `upgrade:` line and
`agentnet doctor` fails its `binary` check. Upgrade by running the installer again: it
replaces the binaries in place. Restart `agentnetd` afterwards.
