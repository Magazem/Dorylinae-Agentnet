# Review 31: CI `-race` failures after 2.2d

CI's race job (`go test -race`, Linux) failed on main after 2.2d merged. It
reported three races across three tests. For each race we read both stacks.
In every case `store.go` (lines 187, 350, 384, 749, 754, 782) is only a
caller frame: `Create` starts `startWatch`, and the watch goroutine calls
`onWindowAnswer`, then `confirm`, then the action's `Perform` or
`AfterCommit`. The memory that races is a local variable in a test closure.
No `Store` field and no window bookkeeping appears in any stack.

**Classification: all three are test-only bugs.** No production change was
needed. The review-26/30 guarantees stay as they were: hooks never run under
`Store.mu`, the window opens outside `Store.mu`, and `AfterCommit` runs
after commit, outside the mutex.

## Races

1. **`TestWindowApproveKillsAndPerforms`** (`internal/approval/window_test.go`).
   - The race: `Perform` writes the plain `bool` `performed` on the
     window-watch goroutine. The test goroutine reads it in a
     `pollUntilStore` loop.
   - Fix: `performed` is now an `atomic.Bool`, and the poll uses
     `performed.Load`.
2. **`TestAfterCommitRunsOnceAfterCommitOutsideMu_Window`** (same file).
   - The race: the `AfterCommit` hook runs on the watch goroutine. It wrote
     `stateAtHook` (through `Row.Scan`) and `listErr` outside the test's
     `mu`. Only `calls` was guarded. The test then read all three under
     `mu`. The hook's unlock came before those writes, so nothing ordered
     them with the test's reads.
   - Fix: the hook computes both values into locals. It then publishes
     `calls`, `stateAtHook` and `listErr` together in one `mu` critical
     section. The test's assertions are unchanged.
3. **`TestApprovalIPCWindowApproveRunsAction`** (`internal/daemon/approval_test.go`).
   - The race: the same `performed bool` pattern as race 1.
   - Fix: `atomic.Bool`, as in race 1.

## Same pattern in other fakes (latent, not in the CI log)

- **`fakeApprovalNotifier`** (`internal/daemon/approval_test.go`): it had no
  lock. `Show` is also called from the watch goroutine (`notifyOutcome`),
  while `lastCode` reads on the test goroutine. `lastTitle` and `lastBody`
  are now guarded by a mutex.
- **Checked, no change needed:**
  - `fakeWinHandle` and `fakeWinRunner`
    (`internal/approval/window_test.go`) already use a mutex. The `ready`
    field is set before the handle is handed off.
  - `gatedRunner` (`internal/approval/window_review_test.go`).
  - `fakeNotifier` and `fakeAudit` (`internal/approval/store_test.go`).
  - `fakeWindowRunner` and `fakeWindowHandle`
    (`internal/daemon/fake_window_test.go`): `readyOK` is written before
    `close(ready)`, and `killed` is set under a lock and never read.
  - `win.notReady` is set before any `Start`.
  - The terminal-path and `review_test.go` `performed` flags are
    synchronous or ordered by `wg.Wait`.

## Files

- `internal/approval/window_test.go`
- `internal/daemon/approval_test.go`
- `Docs/review/31-race-fixes.md` (this file)
