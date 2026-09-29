# 61: Security review of R55-F7 (commit b6a7983)

- **Reviewer:** SEC-F7, model `claude-opus-5-5`, 2026-09-29. This was a read-only review. The only file written is this report.
- **Scope:** `git show b6a7983`: `internal/pathid`, `internal/daemon/grant.go`, `internal/capability/{fs,git}.go`, `internal/device/{runner,scope}.go`, and `grant.md`/`device.md`. Checked against 55/99 R55-006, 027, 028, 159 and 198, the F7 row, §4.1, `verify/C11-04.md`, `verify/T13-01.md` and T4-01.
- **Evidence:**
  - `go test ./internal/pathid ./internal/capability -count=1` passes locally (Windows, go1.27.0).
  - I built a probe from copies of `internal/pathid/*.go` in `%TEMP%\f7probe`, outside the repo.
  - PR #11 CI: `test (macos-latest)` **fails**, `test (ubuntu-latest)` passes, `test (windows-latest)` and `race` were still pending when I wrote this.

## Verdict: **changes needed** (1 High, 1 Medium, 2 Low, 4 Info)

## Findings

### F7-S1 · High · confirmed-test (CI): the macOS data volume root is still grantable
- **Where:** `internal/pathid/pathid_unix.go` `isRoot`, and `grant.go` `checkForbiddenIdentity`.
- **Evidence:** PR #11 run 36603587673, job `test (macos-latest)`:
  `grant_identity_darwin_test.go:44: "/System/Volumes/Data" accepted as "/System/Volumes/Data"`.
  The other six spellings are refused.
- **Why it happens:**
  - `isRoot` returns false for `/System/Volumes/Data`. Its parent (`p + "/.."`) either reports the same `st_dev` or is a different inode on the same device, so across the firmlink "mount point = device changes" does not hold.
  - The containment check compares only the lexical ancestors of the config dir: `/Users/…` and `/` on the system volume. The Data volume root is never one of them.
- **Impact:** R55-006/T13-01 is still open on macOS. After one approval, the peer reads every user's files, including `…/Library/Application Support/dorylinae/dorylinae.db`.
- **Fix direction:**
  - Detect mount points with `statfs` (`f_mntonname == p` on darwin; `/proc/self/mountinfo`, or `statfs` plus a parent `st_dev` check, on Linux).
  - Also walk the config dir's **physical** parents (`open(cfg/..)` repeated until the parent is the directory itself) and compare them with the resource by identity.
  - The existing darwin test already proves it. Keep it as the gate.

### F7-S2 · Medium · confirmed-test (probe): the NT `\??\` prefix bypasses the pre-dial UNC refusal (R55-027 incomplete)
- **Where:** `internal/pathid/pathid_windows.go` `checkLocal`; reached through `validateResource`, `resolveProgram` and `CheckTarget`.
- **Evidence:** `filepath.IsAbs(`\??\UNC\host\share\x`)` is true and `VolumeName` is `\??\UNC\host\share`. `checkLocal` only refuses a leading `\\` or a 2-character network drive, so the path passes.
  - `Resolve` then opens it. `\??\UNC\10.255.255.1\share\x` blocked for **21 s** with "The network path was not found", which is an SMB dial.
  - `\??\UNC\localhost\C$\Users` was refused only by `finalPath`, **after** the session was opened.
  - `\??\X:\` on a mapped drive skips `GetDriveType` the same way.
- **Impact:** R55-027's scenario is unchanged. A local agent calls `grant_policy_add` or `device_scope_set` with a `\??\UNC\attacker\s` path, and Net-NTLMv2 leaves before any approval.
- **Fix:** allow only a volume name that is exactly `X:` (drive letter and colon), and refuse every other non-empty `VolumeName`, including `\??\`. Add `\??\UNC\…`, `\??\C:\…` and `\??\GLOBALROOT\…` to `TestCheckLocalRefusesUNCAndDevicePaths`.

### F7-S3 · Low · confirmed-test (probe): paths under a folder-mounted volume are refused as "network"
- **Evidence:** for a junction or mount point to `\\?\Volume{GUID}\`, Go ≥ 1.23 `os.Readlink` returns `\\?\Volume{…}\`. `guardLinks` hands that to `checkLocal`, which refuses it. `C:\mnt\data\proj` is then refused with "UNC, device and network paths are refused".
- **Impact:** usability only, and it fails closed. Users who mount data disks into folders cannot grant anything on them.
- **Fix:** in `guardLinks`, let a `\\?\Volume{GUID}\` target through. It is always local: mount points cannot target remote volumes. `finalPath` already canonicalises it.

### F7-S4 · Low · confirmed-test (probe), pre-existing: every path *through* a junction fails to resolve
- **Evidence:** on go1.27, `filepath.EvalSymlinks(`<junction>\sub`)` returns "The system cannot find the path specified." With `GODEBUG=winsymlink=0` it resolves. `Resolve` calls `EvalSymlinks` before `finalPath`, so a project reached through a junction is refused as "resource does not exist". The same applies to device repos.
- **Context:** the junction itself as the final component works, and that is what the tests cover (`TestResolveJunction`). Nothing covers a path under one.
- **Fix:** on Windows, skip `EvalSymlinks` after `guardLinks` and rely on `finalPath` (`CreateFile` follows both symlinks and junctions). Add a test for `<junction>\sub`.

### Info
- **I1 (usability decisions, accepted):**
  - Only the mount point **itself** is refused. `IsRoot` runs on `resolved` alone, never on its ancestors, so paths under `/Users`, `/usr/local`, `/mnt/x` or a data drive stay allowed. CI's darwin test confirms this: `/System/Volumes/Data/<proj>` is allowed. There is no regression here.
  - Refusing `\\?\C:\` and volume-GUID spellings is acceptable, since users type drive letters.
  - Junction- or subst-spelled policies and scopes stop matching. That fails closed; note it in the release notes.
  - Refusing a `.git` component for every kind, and a bare-repo shape for `fs.read`, is acceptable.
- **I2 (fs.read, R55-028 edge):** an `fs.read` grant on a directory that *contains* a bare repo (`repos/foo.git`) still serves `foo.git/config` and objects. Serving hides only names spelled `.git`. That is outside this ticket; it fits a spec note in grant.md §Resource kinds.
- **I3 (TOCTOU residual):** "a local writer swaps a root after approval" is **acceptable**. So is the similar `guardLinks`→`EvalSymlinks` window, where a link is swapped to UNC between the guard and the follow. Both need a local writer, which is out of scope in the 55/01 threat model. Serving refuses links and junctions (`componentErr`), and `rewalk` closes R55-159.
- **I4 (tests and docs):**
  - No test covers Linux bind mounts or `/proc/self/root`. By reading, both are handled: identity catches a same-filesystem bind, `st_dev` catches another filesystem, and `EvalSymlinks` maps `/proc/self/root` to `/`.
  - No test covers a mapped network drive. I checked by probe: `net use X: \\localhost\C$` and `subst Y: \\localhost\C$` are both refused by `GetDriveType` before any filesystem access.
  - The §4.1 `zz_review55_*` acceptance files are not in this tree. The fixer's own tests stand in for them.
  - `device/scope.go:139` still says "EvalSymlinks".
  - `resolveProgram` checks `CheckLocal(p)` only after `lookPath`, so a UNC entry in the daemon's own `PATH` is still statted. That PATH is the user's own configuration, not a host the caller chose.

## Checklist answers
1. **Spelling bypasses:**
   - Windows `\\?\`, `\\.\`, UNC, GLOBALROOT, subst, mapped drives, volume GUID, junction, 8.3, case, trailing dot and space, and `::$INDEX_ALLOCATION` are closed. Tests cover these, except mapped drives, which I checked by probe.
   - **Not closed:** `\??\` (F7-S2).
   - macOS firmlink: **not closed** for the Data volume root (F7-S1).
   - Linux: closed by reading, with no tests.
2. **UNC before approval:** closed for `\\` spellings and for links (`guardLinks` reads link targets first). **Open** for `\??\UNC` (F7-S2).
3. **Fail closed:** yes. `grant.go:148` is fixed: config dir, home, ancestor `Stat` and `IsRoot` errors all refuse. The stated residual is acceptable (I3).
4. **Trade-offs:** see I1. The mount-point rule is correct. F7-S3 and F7-S4 are the real usability gaps.
5. **Device scope and argv[0]:** consistent. Scope repos use `validateResource`, and `CheckTarget` uses `pathid.Resolve`. F7-S2 applies to all three.
6. **Tests:** the Windows spellings are proven. The darwin test runs in CI and **fails**, so it proves F7-S1. Linux mounts and `\??\` are untested.
