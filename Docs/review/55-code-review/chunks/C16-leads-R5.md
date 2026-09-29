# C16 leads from CR-R5 (partial, not a chunk report)

CR-R5 started C16 before its context problem was found (see HANDOFF "PROCESS CHANGE") and
was shut down; the fresh worker CR-C16 owns the C16 report. R5's partial notes, verbatim,
for the verifiers and the consolidator (NOT given to CR-C16, to keep it unanchored).
Model claude-opus-5-5, ~10 min, commit 6cc26a7.

1. **High, confirmed-test (R5's test was lost):** Windows named-pipe squatting. Another local
   user creates `\.\pipe\dorylinae-<id>` first. The daemon then exits "already running" and
   the CLI sends params (mail bodies, webhook_url) to the squatter and accepts forged results;
   there is no server-identity check (`internal/ipc/transport_windows.go:21-50`).
2. **Medium:** on macOS, launchd appends to an unrotated `agentnetd.log`
   (`internal/service/launchd.go:83`); relay-driven Warn lines (`internal/relayclient/relayclient.go:442`)
   grow the disk without bound.
3. **Lows:** Unix Listen dial→remove→listen race (`transport_unix.go:20-30`); a rotate failure
   leaves `w.f=nil` and silently kills the log (`internal/logfile/logfile.go:74-82`); Accept
   errors (EMFILE) end Serve (`internal/ipc/ipc.go:122`); `systemdQuote` leaves newlines
   unescaped; Windows `Ensure` sets no DACL (`internal/paths/paths.go:60`).

Consolidator: dedupe against C16.md; any of these not in C16.md needs its own verification.
