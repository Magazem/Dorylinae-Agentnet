# 61b: Security re-review of the R55-F7 fix round (commit feba128)

- **Reviewer:** SEC-F7b, model `claude-opus-5-5`, 2026-09-29. This was a read-only review. The only file written is this report.
- **Scope:** `git show feba128`, read against review 61 (`7fda934`) and the original fix `b6a7983`: `internal/pathid/*`, `internal/daemon/grant.go` `checkForbiddenIdentity`, and `internal/device/scope.go` (comments only).
- **Evidence:**
  - `go test ./internal/pathid ./internal/capability -count=1 -v` passes locally (Windows 11).
    - `TestRefusalMakesNoCall`, `TestResolveThroughJunction`, `TestResolveThroughVolumeGUIDLink` and `TestIsVolumeGUIDPath` all pass.
    - `TestResolveRefusesLinkToUNC` is skipped locally because there is no symlink privilege.
  - PR #11 CI, head `feba128`: run 36612810811 is **all green**: `test (macos-latest)` pass (5m24s; `internal/daemon` and `internal/pathid` ok), `test (ubuntu-latest)` pass, `test (windows-latest)` pass, `race` pass, and lint, build and cross pass. CI runs `go test` without `-v`, so skips are not visible in the log. The same macOS runner **failed** `TestValidateResourceRefusesFirmlinkSpellings` at `b6a7983` (run 36603587673), so its preconditions hold there and this pass is a real pass.

## Verdict: **approve** (0 Critical/High/Medium, 2 Low, 5 Info). The two Lows are follow-ups, not merge blockers.

## Review 61 findings

| # | Status | Evidence |
|---|---|---|
| F7-S1 (High) macOS data-volume root | **Closed** (CI macOS) | See the S1 notes below. |
| F7-S2 (Medium) `\??\` bypass | **Closed** | See the S2 notes below. |
| F7-S3 (Low) volume-GUID targets refused | **Closed** | See the S3 notes below. |
| F7-S4 (Low) paths through a junction | **Closed** | See the S4 notes below. |

**S1: macOS data-volume root**
- `isRoot` now asks `isMountPoint` first.
  - On darwin that means `statfs` `f_mntonname == p`. The comparison is exact and byte-wise, on the path `EvalSymlinks` returned.
  - On Linux it means the mountinfo field 5, with the escapes decoded.
- `checkForbiddenIdentity` also compares the resource by identity with `PhysicalAncestors(cfg)`, the kernel `..` chain. That catches spellings that the exact `statfs` string match misses. For example, `/system/volumes/data` on case-insensitive APFS is refused because it is the same inode as a physical parent of the config dir.
- On Windows, `PhysicalAncestors` behaves the same as `Ancestors`, because Win32 folds `..` lexically. That is harmless.

**S2: `\??\` bypass**
- `checkLocal` refuses a leading `\??` or `/??` before calling `VolumeName`. It then allows only an empty volume name or `[A-Za-z]:`.
- `TestRefusalMakesNoCall` stubs out `Lstat`, `GetDriveType` and `CreateFile`. It proves that none of them is reached for `\??\UNC`, `\??\C:`, `\??\GLOBALROOT`, `\\?\UNC` and `\\.\`.
- `validateResource` still rejects `\foo` first, via `IsAbs`.

**S3: volume-GUID targets refused**
- `guardLinks` lets through only an exact `\\?\Volume{8-4-4-4-12 hex}` target, alone or followed by `\`.
- Links after it are still checked.
- `finalPath` still refuses a `\\?\UNC\` result and any result without a drive letter.

**S4: paths through a junction**
- On Windows, `evalLinks` is a no-op, and `finalPath`'s `CreateFile` resolves the path.
- `TestResolveThroughJunction` covers `<junction>\sub`.

## New findings

### F7b-1 · Low · by reading: without `EvalSymlinks`, a gap in `guardLinks` now leads to a dial instead of an error (Windows)
- **Where:** `internal/pathid/pathid.go` `guardLinks`: `if err != nil { return nil }` after `lstat`, and the rooted-target case. `pathid_windows.go` `evalLinks`.
- **Why it happens:**
  - `guardLinks` ends its walk and returns **nil** as soon as a component cannot be `Lstat`ed.
  - Before feba128, `filepath.EvalSymlinks` ran next. It walks the same way with Go's own link model, so a divergence from the kernel failed closed ("resource does not exist") and nothing was opened.
  - Now `CreateFile` in `finalPath` is the first thing to follow the links, so every place where `guardLinks`'s model differs from the kernel's becomes a pre-approval open.
- **Examples:**
  - (a) A symlink whose reparse `SubstituteName` is a raw NT path such as `\Device\Mup\host\share`.
    - This is possible through `FSCTL_SET_REPARSE_POINT`.
    - `os.Readlink` returns it as-is (`normaliseLinkPath` passes through anything without the `\??\` prefix), and `checkLocal` sees an empty volume name.
    - `guardLinks` rewrites it to `C:\Device\Mup\...`, `lstat` fails, and the function returns nil.
    - The kernel then reparses to MUP and dials SMB.
  - (b) A link that sits below a component that `Lstat` cannot read (an ACL), while traversal to it is still allowed.
- **Reachability:** both need a local writer to plant the link. Under the 55/01 threat model that is out of scope, which is why this is rated Low and not Medium. It is still a regression in defence in depth for R55-027: the "no dial before approval" guarantee now rests on `guardLinks` alone.
- **Fix:**
  - In `guardLinks`, return the `lstat` error instead of nil. `Resolve` fails for a missing path anyway, and callers already map a non-`ErrRemote` error to "resource does not exist".
  - Optionally, refuse a rooted target (`\…` with no volume name) that does not exist on the current drive.
  - Add a test that stubs `lstat` to fail on a middle component and asserts that `createFile` is never called. That fits the pattern of `TestRefusalMakesNoCall`.

### F7b-2 · Low · by reading: the Linux mountinfo check refuses bind-mounted project directories (usability, fails closed)
- **Where:** `pathid_linux.go` `isMountPoint`, called from `isRoot`.
- **Behaviour:** a directory that is itself a bind mount of the same filesystem is now refused as a "filesystem root or mount point". Two common cases:
  - devcontainers' default `workspaceMount` of `/workspaces/<repo>`;
  - `docker run -v $PWD:/src` on native Linux.
- Before this round, the device check allowed it. Cross-filesystem binds were already refused.
- **Impact:** usability only, and it fails closed. The workaround is to grant a subdirectory.
- **Fix:** either accept it and state it in grant.md §Issuance and the release notes next to I1, or treat a mount point as a root only when its mount root (mountinfo field 4) is `/`. A bind of a subdirectory has a field 4 other than `/`, so this refuses whole-filesystem binds and allows subdirectory binds. Owner's call.

### Info
- **I1 (`isVolumeGUIDPath` exemption):**
  - The exemption applies to every link type, not only mount points, and `\\?\Volume{…}` resolves through the caller's per-session DosDevices first.
  - Code running as the user could `DefineDosDevice` a fake `Volume{…}` that points at `\Device\Mup\host\share`, then link to it. The dial would happen before `finalPath` refuses the `\\?\UNC\` result.
  - That needs a local writer and is out of scope. A real volume GUID is always a local volume, removable or VHD included, and granting under one is a user choice. The pattern itself is strict: exact length, hex digits, and a `\` or the end after `}`.
  - Consider limiting the exemption to `IO_REPARSE_TAG_MOUNT_POINT` if a cheap way to tell link types apart becomes available.
- **I2 (mountinfo parsing):**
  - The fields cannot be spoofed: the kernel escapes space, tab, newline and backslash in mount paths, and the parser decodes exactly `\ooo`.
  - A malformed line or a line over 1 MiB makes `isRoot` return an error, which refuses the resource: fail closed.
  - Only the caller's mount namespace is listed, which is the right view.
  - Entries of this shape are only ever added by mounting, which needs privilege or a user namespace. An extra entry can only cause a refusal.
- **I3 (`statfs` exactness):**
  - The comparison is exact on bytes. It misses case or Unicode-normalisation variants of a mount path, because darwin `EvalSymlinks` does not canonicalise case.
  - For the data volume, the `PhysicalAncestors` identity check covers this.
  - For other volumes, the `st_dev` fallback decides. Separate volumes such as `/Volumes/X` have their own device. Only the data volume shares its device with its parent, as review 61 observed.
  - Residual: if the config dir is **not** on the data volume, a case-variant spelling of `/System/Volumes/Data` is not refused. That is non-default and needs `--config-dir` elsewhere.
- **I4 (`PhysicalAncestors` length):** each level appends `/..`, so a config dir deeper than about 300 levels hits `PATH_MAX` on darwin, and every resource is then refused. That fails closed and does not happen in practice.
- **I5 (tests):**
  - The Linux tests are good: `/proc` is a root, `/proc/self/root` resolves to a root, and a `mountPoints` fixture covers bind mounts and `\040`.
  - No test covers the Linux bind-mount usability case (F7b-2) or the `guardLinks` lstat-failure path (F7b-1).
  - `TestResolveRefusesLinkToUNC` needs the symlink privilege, so it only runs where CI grants it.

## Checklist answers
1. **Review 61 findings closed:** S2, S3 and S4 are closed and tested on Windows. S1: closed. The darwin gate (`TestValidateResourceRefusesFirmlinkSpellings`, T13-01), which failed at `b6a7983`, now passes on macos-latest, together with `TestIsRootDataVolume`.
2. **New issues:**
   - The mountinfo parser cannot be spoofed (I2).
   - The `statfs` comparison is exact, with a covered residual (I3).
   - Skipping `EvalSymlinks` removed a fail-closed backstop (F7b-1). Relative and rooted targets are handled lexically, the same way Windows handles them. The window for swapping a symlink to UNC after the check is unchanged (review 61 I3).
   - `isVolumeGUIDPath` is strict, and the DosDevices shadow residual is out of scope (I1).
   - Usability: bind-mounted project roots on Linux (F7b-2). Junction and folder-mount projects on Windows now work.
3. **Tests prove the fixes:** yes for S2, S3 and S4 on Windows (run locally). For S1, the darwin gate is CI (above).
