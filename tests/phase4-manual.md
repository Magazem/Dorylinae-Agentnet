# Phase 4 manual tests

Mark each step `[x] PASS` or `[x] FAIL` and add notes. Created by ticket 4.4d. These are the
Unix service-install and keychain checks CI genuinely cannot make: a real interactive login
session (not a headless runner), a reboot, and a locked or absent keychain UI.

## 4.4d Unix hardening — what CI cannot cover

`.github/workflows/ci.yml`'s `unix-service` job builds `agentnetd`/`agentnet` for real and, on
`ubuntu-latest` and `macos-latest`, runs `agentnetd install`, waits for `agentnet status` to
report the daemon running, checks the unit/plist file and the platform's own "is it loaded"
query, runs `agentnetd stop`, then `agentnetd uninstall`, and confirms the unit/plist file and
the loaded job are both gone. `internal/keystore`'s tests cover the Secret-Service-absent file
fallback (exact 0600, table of group/world-readable modes refused) on every OS the test suite
runs on, and `internal/keystore/darwinreal` exercises the real macOS keychain when the runner's
keychain is unlocked. What that leaves for a human:

- [ ] **A real interactive Linux desktop login**, not `loginctl enable-linger` on a headless
      CI runner. `enable-linger` starts the user's systemd manager without a login session at
      all, which proves the unit and commands are correct but not that a normal desktop login
      (GNOME/KDE, a real `XDG_RUNTIME_DIR` created by pam_systemd, a graphical session bus)
      picks up and runs `~/.config/systemd/user/agentnetd.service` the same way. Log into a
      real Linux desktop, run `agentnet setup` (or `agentnetd install`) once, log out and back
      in, and confirm `systemctl --user status agentnetd.service` shows it running without
      re-running install.
- [ ] **A real interactive macOS GUI login**, not the CI runner's automatic session.
      `launchctl bootstrap gui/<uid>` on `macos-latest` proves the plist and commands are
      correct under that runner's always-logged-in session; it does not prove the agent
      survives a real logout/login cycle or appears in System Settings > General > Login
      Items the way a user-facing LaunchAgent should. Install on a real Mac, log out and back
      in (or reboot), and confirm `launchctl print gui/$(id -u)/dev.dorylinae.agentnetd` still
      shows it loaded and that a fresh `agentnetd` process is running (a new start time).
- [ ] **A reboot**, on both OSes: confirm the service comes up after a full restart, not just
      after `systemctl --user enable --now` / `launchctl bootstrap` in the same session. CI
      runners are single-boot and cannot exercise this.
- [ ] **A locked or logged-out macOS keychain.** `internal/keystore/darwinreal`'s
      `TestRealKeychainRoundTrip` skips (does not fail) if `Set`/`Get` return
      `keystore.ErrUnavailable` or a keychain error, which is what a locked login keychain, a
      keychain with no default set, or a `security` policy prompt looks like. GitHub's
      `macos-latest` runner keeps its login keychain unlocked for the whole job, so this path
      is not exercised in CI. On a real Mac, lock the login keychain (`security lock-keychain`)
      or log in without unlocking it, then run `agentnet setup` (or anything that stores the
      identity key) and confirm it falls back to the owner-only file (`identity.key`, mode
      0600) instead of hanging or crashing, and that a later unlock lets a *new* identity use
      the keychain again (existing file-backed identities are not expected to move themselves
      back to the keychain).
- [ ] **A key file's ACL, not just its POSIX mode bits**, on both OSes. `checkOwnerOnly`
      (`internal/keystore/perm_unix.go`) reads only `os.FileMode.Perm()`, which does not see a
      POSIX ACL (Linux `setfacl`) or a macOS ACL (`chmod +a`) that grants another user or group
      access despite a `-rw-------` mode string. Confirm on a real filesystem that supports
      ACLs: `chmod 600 identity.key`, then grant a second account read access via `setfacl -m
      u:otheruser:r identity.key` (Linux) or `chmod +a "otheruser allow read"
      identity.key` (macOS); `agentnet`'s own checks are expected to still say the file is
      600-only (a known gap, not a bug introduced here) — record whether that second account
      can actually read the file, since that is the real-world risk `checkOwnerOnly` does not
      cover.

## Notes on the CI job's scope

`unix-service` only exercises what `agentnetd install`/`uninstall`/`stop` and `agentnet status`
already do; it does not create a config directory, run `agentnet setup`, or exchange any relay
traffic, so keystore selection inside the *real installed service process* runs whatever
`$DORYLINAE_KEYSTORE` the job sets (see the job's `DORYLINAE_KEYSTORE=file` step) rather than
auto-detecting a keychain — this keeps the job from hanging on a Secret-Service timeout, but
means the job does not by itself prove the installed service can find and use a real keychain.
That combination (installed-as-a-service *and* using the keychain) is not covered anywhere and
is a reasonable follow-up if it matters in practice.
