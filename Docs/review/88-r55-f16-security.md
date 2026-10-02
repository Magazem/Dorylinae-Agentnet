# Review 88 — R55-F16 security review (relay operations robustness)

Ticket commit `44366c3` on `eb283bf`, branch `p4/r55-f16`. Reviewer: R55-F16sec-Opus.
Scope: `Server.serveWG` shutdown, `/metrics`, relay migrations, `relay backup` / `relay restore`,
`fileDSN`, v1 `pair_new` limiter, `deploy/early/setup.sh`.

Checks run: `go test ./internal/relay -count=3` ok (232 s); `go test ./cmd/relay` ok;
`bash -n deploy/early/setup.sh` ok. A copy of the `setup.sh` key filter was run against sample
`authorized_keys` lines in a temp dir; results are under F1.

## Verdict

**Approve after fixing F1 and F2.** Both are small. Nothing here is High. Most of the
ticket is sound:

- the serve wait is one WaitGroup, so N slow peers cost 4 s in total, not 4 s each;
- `beginServe` puts the `closing` check and the `Add` under the same `mu`;
- a migration runs under `BEGIN IMMEDIATE` and re-reads the version inside the lock;
- restore copies to a side file and renames it, so a bad backup never replaces the database;
- `O_CREATE|O_EXCL` refuses an existing `--out`, including a symlink planted there in advance;
- metrics expose no paths or keys and cost one `stat` plus one `statfs` per scrape;
- v1 `pair_new` is charged before any other check.

## Findings

### F1 — Medium — `setup.sh` misses `command=` written in another case, so the owner can be locked out
`deploy/early/setup.sh:31-33`. sshd matches `authorized_keys` option names without regard to
case (`auth-options.c`, `opt_match` uses `strncasecmp`). The filter
`grep -vE '(^|[[:space:],])command='` is case-sensitive. These lines were accepted as an
"unrestricted key":

```
Command="/bin/false" ssh-ed25519 AAAA…            ACCEPT
environment="A=1",COMMAND="/bin/false" ssh-ed25519 ACCEPT
```

**Scenario.** root's only key has a forced command in upper or mixed case. `setup.sh`
continues and writes `PasswordAuthentication no`, and root now has no shell over SSH. The
provider's web console is the only way back, and that is exactly the case R55-196 meant to
prevent. The same filter also accepts:

- `ssh-dss` keys, which Debian 12's OpenSSH refuses by default;
- keys limited by `expiry-time=` or by `from=` (a source address other than the owner's).

Those are lower risk because the operator chose them.

**Fix.** Use `grep -viE` for the `command=` filter. Drop `ssh-dss` from the accepted types.
Optionally treat `from=` and `expiry-time=` the same way. A filter that is too strict only
refuses to run, which is safe.

### F2 — Medium — `relay restore` run as root leaves a root-owned 0600 database, and the relay cannot open it
`internal/relay/backup.go:84-109` (`copyFile` creates `relay.db.restoring` 0600 as the current
user; `os.Rename` puts it over `--db`). The unit runs as `User=agentnet-relay`
(`deploy/early/agentnet-relay.service:22`). The runbook shows `runuser` for backup
(`Docs/ops/early-relay-deploy.md:280`) but gives no restore command.

**Scenario.** The operator runs `relay restore --force …` as root. It reports success. On
`systemctl start`, the relay fails with "unable to open database file" and the relay is down
until someone runs `chown`.

The old code (remove the file, then recreate it) behaved the same, so this is not a
regression. But the rename now makes the new ownership unconditional. Before, a `--force`
over a database that still existed kept that file's owner.

**Fix.** On Unix, `Chown` the side file to the uid/gid of the existing `--db`, or of its
directory, before the rename. Also document
`runuser -u agentnet-relay -- relay restore …` next to the backup line.

### F3 — Low — frames routed to a peer whose `drainClose` already finished are still lost
`internal/relay/relay.go:531-540` with `conn.go:108-118, 456-472`. `Close` starts
`drainClose` for every connection at once. Read loops keep routing for up to 4 s.

**Scenario.**
1. Peer B's buffer empties, B's `drainClose` sends its close frame, and B's `writeLoop` ends.
2. B is still in `s.conns` until its `serve` returns.
3. Peer A's `pair.confirm` or `session.*` to B gets `dst.direct` → `directSent` and goes into
   B's dead buffer. A receives no `queued`.

This is the in-flight direct frame that R55-036 targets. Mail survives because the outbox
resends it, but these direct frames have no resend. So
`relay-hosted.md` §3 ("routed or queued, not lost") and `early-relay-deploy.md:270-273`
("not a lost frame") claim too much. Found by reading the code; not reproduced with a test.

**Fix.** In `Close`, before the `drainClose` loop, set `c.draining = true` on every
connection. Do it under `c.mu`, after collecting the connections, not while holding `s.mu`.
`direct` then returns `directDraining`, the frame is queued and acknowledged with `queued`,
and it is delivered after the restart. `startDrain` already refuses once `closing` is set.
Alternatively, change the doc wording.

### F4 — Low — the serve wait (4 s) is shorter than `drainCloseTimeout` (5 s), and the stop budget has 1 s to spare
`relay.go:47` and `conn.go:18`. A peer still flushing a full outbound buffer keeps its read
loop alive past the 4 s, so the queue closes under it. Its later `enqueue` and `ack` calls
fail with "database is closed" (the sender gets `internal`, and acked rows are sent again,
which dedupe absorbs). So nothing silent is lost.

Worst case, the stop takes 5 s (HTTP) + 10 s (drain) + 4 s (serve) = 19 s against
`TimeoutStopSec=20`. On SIGKILL, WAL with `synchronous(FULL)` keeps the database consistent.
**Fix (optional):** log how many read loops were still running when the wait gave up, and
think about raising `TimeoutStopSec` to 30.

### F5 — Low — restore does not detect a running relay; on Linux it "succeeds" and the running relay's writes are lost
`backup.go:86-108`. On Linux, unlink and rename succeed under a running relay. The relay
keeps writing to the unlinked inode and its unlinked `-wal`/`-shm`. Every envelope, ack,
account change and unbind after the restore is lost silently at the next restart. Windows
fails safe: removing `-wal` hits a sharing violation.

The doc says to stop the relay first, so this is operator error. **Cheap detection:** the
relay holds an advisory lock (`flock` / `LockFileEx`) on `<db>.lock` while it runs, and
restore (and `admin`, if wanted) refuses when it cannot take that lock without blocking.
A runbook check with `systemctl is-active agentnet-relay` is the stopgap.

### F6 — Info — smaller points
- **Rename durability.** `backup.go:105`: the directory is not fsynced after the rename. After a
  power loss the old database can come back. That is safe, just surprising.
- **`--out` swap race.** `backup.go:39-50`: between closing the O_EXCL file and `VACUUM INTO`
  opening it by path, an attacker who can write to the directory could swap it for a
  symlink. That needs a non-sticky directory writable by others (the StateDirectory is 0700),
  so it is not exploitable as deployed.
- **0600 on Windows.** It is advisory there; the file takes its ACL from the directory. The
  relay is Linux-hosted.
- **`relay_db_bytes`.** It counts only the main file, not `-wal` (up to `journal_size_limit`).
  The disk alert relies on `relay_disk_free_bytes`, so this does no harm. Document it or add the WAL size.
- **Metrics content.** No path, key or error text appears in the output or in the 500 body.
- **Migrations.** Every open takes one short write lock per migration, three today. That is
  fine for both the relay and `relay admin`. A database migrated by a newer binary is skipped
  inside the lock and then refused by `checkRelaySchemaVersion`, which is correct.
- **v1 `pair_new` limiter.** No bypass through other frames: redeem and lookup stay under
  `lim`, and `pair_cancel` does not refund the charge. A new key per connection still runs
  into the per-prefix limiter.

## Files created
- `Docs/review/88-r55-f16-security.md` (this file)
- temp: `/tmp/tmp.K42uZIg2Sm/ak` (sample authorized_keys probe; outside the repo)
