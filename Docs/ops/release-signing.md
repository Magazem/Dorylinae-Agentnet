# Release signing (owner, offline)

D36, OD-P4-19 (a) + (ii): the release signature is a plain Ed25519 signature over the
bytes of `SHA256SUMS`. The key is kept offline and is **never** in GitHub: CI builds and
publishes a **draft** release; the owner signs on their own machine, uploads the
signatures and publishes the draft. The tool is `tools/releasesign` (Go standard library
only; `go run ./tools/releasesign --help`).

> **Status (2026-09-29): "Every release" below is the process of ticket R55-F3**
> (spec `Docs/review/57-r55-f3-spec.md`, approved D45). `fetch`,
> `sign -expect-sha256 -archives` and `verify -archives` are implemented on branch
> `p4/r55-f3`. **Do not cut a real release before F3 is merged and the dry run (spec §7
> A9) has settled V2, V4 and V5.** The earlier process signed whatever `SHA256SUMS` was on
> the draft, and anyone able to edit the draft could have swapped it first (review 55,
> R55-003).

**Why the draft is never trusted.** A draft release can be edited by anyone who can write
to the repository: a leaked token, or someone who took over your GitHub account. That
includes its files, its notes and the commit it names. So you sign only a `SHA256SUMS`
that meets both of these conditions:
1. its SHA-256 is the one printed by **the tag's own CI run**, in a log nobody can edit;
2. that run was built from **the commit you tagged on your own machine**.

Your local clone, not GitHub, is the reference for which commit is being released.

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

1. **Tag on your machine and write down the commit.** Update `CHANGELOG.md` (from ticket
   4.7a) and review what goes out. Then, in your own clone:

   ```sh
   git tag vX.Y.Z <reviewed commit>
   git rev-parse 'vX.Y.Z^{commit}'     # note this 40-character commit; it is "COMMIT" below
   git push origin vX.Y.Z              # tags need the owner's OK
   ```

   `release.yml` builds, writes `SHA256SUMS` and prints its digest
   (`release-sums-sha256: <hex>`) in the `sums` job's log and in the run summary. It then
   tests `install.sh` (with a throwaway test key) and the Homebrew formula. Its `draft`
   job waits for your approval in the `release` environment. Approve it: it creates a
   **draft** release `vX.Y.Z` with six archives and `SHA256SUMS`.

   **If `git push` rejects the tag** (for example "already exists"), stop: someone else
   created it on GitHub. See "If a check fails" below.

   Do every later step in **this same clone**, and do not run `git fetch --tags --force`
   in it. The tag you made is the reference; a plain `git fetch` will not move it if
   someone moves it on GitHub.
2. **Fetch and check the run** once the run has finished (all jobs green, draft created):

   ```sh
   go run ./tools/releasesign fetch -tag vX.Y.Z -commit "$(git rev-parse 'vX.Y.Z^{commit}')" -out rel-X.Y.Z
   ```

   (The same line works in PowerShell and in sh.)

   `fetch` uses `gh` (logged in as you). It stops, and you must not sign, unless all of the
   following hold:
   - exactly one run of `release.yml`, from a tag **push**, was built from COMMIT;
   - no other push run exists for this tag (if one does, the tag was moved on GitHub:
     stop, see below);
   - every job succeeded on a GitHub-hosted runner;
   - the `sums` log carries exactly one `release-sums-sha256:` value;
   - exactly one release is named vX.Y.Z, it is a draft, and it holds only the six
     archives and `SHA256SUMS`;
   - the draft's `SHA256SUMS` has that digest;
   - every draft archive matches its line;
   - the tag on GitHub still points to COMMIT.

   It downloads the draft into `rel-X.Y.Z/` and prints the run's URL and
   `expected sha256: <hex>`.

   **Check it yourself too:** open the printed run URL in the browser. The commit shown
   there (7 characters) must be the start of COMMIT, and the run summary must show the
   same `release-sums-sha256` value as `fetch`'s `expected sha256:` line. Never take a
   digest, a commit or a command from the draft's notes; the notes can be edited.
3. **Sign**, with the key available. For `-expect-sha256`, copy the value **from the run
   summary in the browser** (the one you just compared), not from `fetch`'s output:

   ```sh
   go run ./tools/releasesign sign -key agentnet-release.key \
     -expect-sha256 <hex from the run> -archives rel-X.Y.Z rel-X.Y.Z/SHA256SUMS
   go run ./tools/releasesign verify -install-sh scripts/install.sh rel-X.Y.Z/SHA256SUMS
   gh release upload vX.Y.Z rel-X.Y.Z/SHA256SUMS.sig rel-X.Y.Z/SHA256SUMS.minisig
   ```

   `sign` refuses unless:
   - `SHA256SUMS` is exactly the six archives of one version;
   - its SHA-256 equals `-expect-sha256`;
   - every archive in `rel-X.Y.Z` matches its line.

   `sign` does not use the network; it can run on a separate machine if you copy
   `rel-X.Y.Z` there. `verify` also checks that the two keys in `install.sh` are the same key.
4. **Publish.** First replace the draft's notes with your own text (the `CHANGELOG.md`
   entry): whoever can edit the draft can also edit its notes. Publish the draft on
   GitHub, then check what is actually published:

   ```sh
   gh release download vX.Y.Z -D pub-X.Y.Z
   go run ./tools/releasesign verify -install-sh scripts/install.sh -archives pub-X.Y.Z pub-X.Y.Z/SHA256SUMS
   git ls-remote origin 'refs/tags/vX.Y.Z*'   # must show COMMIT
   ```

   If either check fails, delete the release and investigate (see below); release a new
   patch version once it is fixed. With immutable releases on, a published release cannot
   go back to draft or have its files changed.
5. **Homebrew:** render the formula from the `SHA256SUMS` you signed and commit it to the tap
   repository (`github.com/Magazem/homebrew-tap`, `Formula/agentnet.rb`):

   ```sh
   sh packaging/homebrew/render.sh X.Y.Z rel-X.Y.Z/SHA256SUMS > agentnet.rb
   ```

**If a check fails, do not sign.** In particular, stop if:
- the run was built from another commit;
- there are two push runs for the tag, or two releases named vX.Y.Z;
- your tag push was rejected, or the tag on GitHub points elsewhere;
- the draft holds a file other than the six archives and `SHA256SUMS`;
- the digests differ;
- a job ran on a runner that is not GitHub-hosted.

Any of these means someone other than you changed the repository, the tag or the draft.
Delete the draft and change your GitHub password, tokens and sessions. Then check
**Settings → Actions → Runners**, rulesets and the `release` environment, and find what
changed before you tag again. A new tag number is simplest.

**What this protects against, honestly.** A swap of the draft's files, or a look-alike CI
run, by a leaked token or by someone who took over your GitHub account: they cannot make
the tag's CI run, at your commit, log a different digest. It does **not** catch a CI run
that was compromised while it ran (a poisoned action or runner): that run's own log
would carry the bad digest. Closing that needs a local rebuild (OD-R55F3-2, planned
before the public launch). It also does not catch a bad commit that you tagged yourself:
review `main` before tagging.

Without OpenSSL 3 you can cross-check by hand: `openssl pkeyutl -sign -inkey
agentnet-release.key -rawin -in SHA256SUMS -out check.sig` produces the same bytes as
`SHA256SUMS.sig` (Ed25519 is deterministic).

## Dry run

`Actions → Release → Run workflow` with `dry_run` ticked and a `version` (default
`0.0.1`; it must be `0.0.Z`) creates only a draft **pre-release** named `dry-run-<run id>`
with the six archives and `SHA256SUMS`, plus the run's workflow artifacts (kept 7 days). A
draft does not create a git tag. Delete the draft afterwards (`gh release delete dry-run-<run id>`).
`dry_run` unticked is refused.

Sign a dry run only with a **test** key (`keygen -out test.key`, `embed` into a copy of
`install.sh`), never with the release key. A dry run can be started from any branch, so its
binaries are unreviewed; a signature is valid for ever, for whatever bytes it covers. That is
why dry-run versions are `0.0.Z`: below `AGENTNET_MIN_VERSION`, so `install.sh` refuses them
even if one were signed with the real key by mistake (review 53 M1).

To rehearse the steps above on a dry run, use
`fetch -dry-run -tag dry-run-<run id> -commit <the branch commit you dispatched>`. It
accepts a `workflow_dispatch` run instead of a tag push. Then run `sign -expect-sha256 …`
with the test key.
