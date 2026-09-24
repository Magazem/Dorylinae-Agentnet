# 39: CI red since 2.3a: symlink stat and the in-flight limit under -race

CI on `main` had failed on every OS since 2.3a (fetch server) merged:

- `TestFetchSymlinkEscapes`: `stat out-file: error = "", want "symlink"` (ubuntu, macos,
  windows, race).
- `TestFetchRatePerSecond`: `op 16: "rate_limited"` (race job only).

The local merge gate missed both. This Windows machine cannot create file symlinks (no
privilege), so the file-link half of the test was silently dropped with `t.Logf`. The
limiter failure only shows when goroutines run slowly, as they do under `-race`, which the
gate machine cannot run.

## 1. `stat` of a final-component symlink

**Root cause.** `walk(root, segs, finalLink)` returned the final component's `Lstat` as
is when `finalLink` was true (stat only). A stat of `out-file` therefore answered
`{"type":"symlink"}` instead of failing. The test contradicted itself: its loop expected
`symlink` for stat of `out-file`, and its tail expected a successful stat of type
`symlink`. The tail was never reached on CI, and the whole block never ran locally.

**Spec.** `grant.md` §Serving fs said "a symlink anywhere in the path → `symlink`", and the
resource table said symlinks are "listed as `symlink`, never followed". The 2.3a note
"stat on a symlink returns type symlink" is not in the spec, and review 34 did not change
this rule. The text is at best ambiguous about stat, so the stricter reading applies:
**every op on a link (stat, list, read), as the final or an intermediate component, is
refused with `symlink`.** Only a `list` of the parent shows a link, as an entry. The spec
now says so explicitly.

**Security impact: none.** The old stat answer came from `Lstat` of the link itself:
type `symlink`, no size, no target path, nothing about the target. `read` and `list` already
used `finalLink = false` and refused with `symlink` on every OS. The only thing disclosed
was that a link exists, which a `list` of the parent already shows. The fix removes even
that from stat, so the rule has no exceptions.

**Fix.**
- `internal/capability/fs.go`: new `componentErr(mode, last)` holds the per-component
  decision as a pure function. `walk` has no `finalLink` parameter any more, so
  symlink/`ModeIrregular` → `symlink` for every component and every op.
- `internal/capability/fetch_test.go`:
  - `TestFetchSymlinkEscapes` now asserts read, stat **and list** → `symlink` for
    `out-dir`, `in-dir`, `out-dir/secret`, `in-dir/real.txt`, `out-file`, `in-file`, and the
    list types of the parent.
  - The file-link cases are a subtest. It skips **only on Windows**, with an explicit
    `SKIP file-symlink cases: ... needs SeCreateSymbolicLinkPrivilege or Developer Mode`
    message. On Linux and macOS, failing to create a link fails the test, and `linkDir` no
    longer falls through to the no-op `makeJunction` skip there.
  - New `TestComponentClassification` checks the decision on mode bits (symlink,
    irregular, dir, FIFO, regular; final and intermediate) and needs no real links, so it
    runs on the gate machine.
- `internal/capability/fetch_windows_test.go`: the junction skip message is explicit.
- `Docs/protocol/grant.md` §Serving fs: the final component is included, `list` is the
  only place a link appears, and the text now matches the code: an intermediate
  non-directory that is not a link (FIFO, device, file) → `not_found`. The old text said
  `symlink`, but the code has always answered `not_found`. Both refuse; nothing was
  served either way.

## 2. `rate_limited` for a sequential holder under -race

**Not the clock.** The limiter already uses the injected clock everywhere: `admit` and
`addServed` take `now` from `s.now()` = `cfg.Now`, and `Flush` does the same. The fake
clock does not move during the test's 20 ops, so the per-second window is correct.

**Root cause: in-flight slots were released after the response was sent.** A job held its
per-peer/daemon slot until `run` returned (`defer s.release(j)`, after `auditOp`), and its
per-grant slot until `serve` returned (`defer release()`). The response was handed to
`Send` **before** both releases. The holder (the test's `h.call`) sees the answer and sends
the next request at once. With 8 workers, the next job can reach `Handle`/`admit` while
the previous one or two jobs are still auditing. That puts the holder at its limit of 2
in flight, and the request is refused with `rate_limited`. Under `-race` the gap is wide
enough to hit, and op 16 was simply where it happened. This is a real bug, not only a
test flake: a well-behaved holder that waits for each answer could be refused by a busy
grantor.

**Fix.**
- `internal/capability/fetch.go`: `run` wraps the peer/daemon release in
  `sync.OnceFunc`. `serve` combines it with the grant release as `finish` and calls it
  **right before the last response**: the stat/list answer, the last read fragment, or
  (in `run`) the error answer. Deferred calls stay as a backstop. Bytes served are now
  counted before each fragment is sent, so the next request also sees the byte total.
  This errs towards counting a failed send, never towards serving more.
- The limits are unchanged. An op's last response is in `Send` when its slot is freed,
  and the holder cannot have seen that response yet, so from the holder's side at most
  2 ops are ever outstanding. The fragment bound towards one holder (16) is also
  unchanged.
- `Docs/protocol/grant.md` §Limits defines when an op stops being in flight.
- Regression test `TestFetchSequentialHolderNotRateLimited`: `Send` sleeps 20 ms after
  delivering, which simulates a slow grantor. It fails on the old code (`op 3:
  "rate_limited"`) and passes on the new code, without `-race`.

## Not changed

`TestFetchRatePerSecond`'s tolerance: it was right and passes now.
