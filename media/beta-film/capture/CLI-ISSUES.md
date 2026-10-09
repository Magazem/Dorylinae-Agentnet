# CLI issues found while capturing the film's terminal output

Documented only; nothing here has been fixed. Each issue was hit while running the real product to capture the output
the beta film quotes (`INDEX.md` in this folder). The capture files cited below hold the exact commands and output.

## How the run was set up

- Binaries built from commit `860afe8` with `go build -trimpath` (`agentnet`, `agentnetd`, `relay`).
- One local relay (`ws://127.0.0.1:8787`) and two daemons with separate homes on one Linux machine (headless container):
  **maya** (owner of team `atlas`) and **theo** (member). Keys in file keystores (`DORYLINAE_KEYSTORE=file`).
- Approvals in terminal mode (`DORYLINAE_APPROVAL=terminal`): each daemon ran on a pseudo-terminal and a small script
  typed the approval code back about 2.5 s after the prompt, as a person would (`scripts/daemon_pty.py`).
- Every command was run as written in the capture files, first in human form and then with `--json`.

## Issues

### 1. `request show <id> --from <name>` fails on the receiver

- **What happened.** On theo (the receiver), `agentnet request show r-0282… --from maya` printed
  `agentnet: no such request` and exited 1. The same command with maya's public key in `--from` worked, and plain
  `agentnet request show r-0282…` worked.
- **Expected.** `Docs/cli/inbox.md` (the `--from <peer>` row) says `--from` takes "A paired peer's name
  (case-insensitive, `@` optional) or public key", and `Docs/cli/request.md` documents `request show <id> [--from <peer>]`.
- **How it happened.** maya sent three requests to theo; theo ran `inbox`, then `request show <id> --from maya`
  (also tried `@maya`).
- **Evidence.** `11-inbox.txt` lines 15–21.
- **Impact.** An agent following the docs gets "no such request" for a request that is in its inbox.

### 2. An incoming request reads "to maya" instead of "from maya"

- **What happened.** On theo, `agentnet request show r-0282…` printed
  `r-0282…  review to maya (team atlas): Review the checkout retry change`. That request is *from* maya, *to* theo.
- **How it happened.** The same `request show` as issue 1, on the receiving side.
- **Where it comes from.** `cmd/agentnet/request_query.go` line 52 prints `"%s  %s to %s (team %s): %s"` for every
  request, without checking the view's `direction` (`"in"` for received requests).
- **Evidence.** `11-inbox.txt` line 21; the JSON of the same request says `"direction":"in"` (line 27).

### 3. `inbox --json` and `request list --json` return an empty `brief`

- **What happened.** In both lists every request has `"brief":""`, although the high-urgency review was sent with a
  three-line brief (`--brief-from-file`). The brief is present in `request show <id> --json`.
- **Expected.** `Docs/cli/inbox.md`, "`--json` output": "`inbox`: … Each view carries the full `brief`".
- **How it happened.** maya sent the review with a brief file, then ran `request list --json`; theo ran `inbox --json`,
  then `request show <id> --json`.
- **Evidence.** `10-request-send.txt` line 29 (`request list --json`), `11-inbox.txt` line 12 (`inbox --json`) and
  line 27 (`request show --json`, brief present). The human `request show` does not print the brief at all.
- **Impact.** An agent that triages its inbox from `inbox --json` (as `Docs/agents/snippet.md` suggests) never sees the
  brief unless it makes a second call per request.

### 4. `request list` (human form) is not column-aligned

- **What happened.** The header and rows of `agentnet request list` do not line up:

  ```
  ID                                  TO   TYPE    URGENCY  STATE      RESULT  DELIVERY   CREATED
  r-02824085f0a43a85a943105fc8af1e7f  theo  review  high  pending  -  delivered  2026-10-08T13:40:08Z
  ```

  `agentnet inbox` in the same run is aligned, so this is specific to `request list`.
- **Evidence.** `10-request-send.txt` lines 21–25.

### 5. Approval prompts have a doubled full stop before the code

- **What happened.** Every approval prompt reads `… you started this yourself.. Code 047176. …` (also `… yourself..`
  for team invites and debate constraints).
- **Where it comes from.** The summaries in `internal/approvaltext/approvaltext.go` end with a full stop, and
  `internal/approval/store.go` line 283 formats them as `"%s. Code %s. …"`, adding a second one.
- **Evidence.** `06-verify-ping.txt` lines 10 and 23, `07-team.txt` line 17, `17-debate.txt` line 58,
  `24-daemon-terminals.txt`.
- **Impact.** Cosmetic, but it is in the one window the human reads before typing a code.

### 6. `agentnet --help` does not list the work-session commands

- **What happened.** The top-level help lists 28 commands but not `sessions`, `session`, `result`, `wait` or
  `accept-result`, although all of them work and `Docs/agents/snippet.md` tells agents to use them.
  (`grants` and `revoke` are mentioned only inside the `grant` line.)
- **Evidence.** `01-help.txt` lines 12–39.
- **Impact.** An agent that starts from `agentnet --help`, as the snippet tells it to, cannot discover the result loop.

### 7. The human debate view does not show human constraints

- **What happened.** After maya added the constraint "Do not change the 45s gateway timeout." (approved on her
  daemon's terminal), `agentnet debate <id>` in human form showed the transcript but no constraint. The `--json` view of
  the same debate has `"constraints":[{… "text":"Do not change the 45s gateway timeout.","state":"active"}]`, and the
  Decision Markdown lists it under human decisions.
- **Expected.** `Docs/cli/debate.md` documents `constraints` in the JSON view; it does not say the human view omits
  them. Treat this as a gap between the two views rather than a confirmed bug.
- **Evidence.** `17-debate.txt` lines 52–58 (constraint added), 82–95 (human view), 97 (JSON view).

## Seen during the run, but not product faults

- **Desktop notifications** failed (`notify --test` printed `Desktop: failed`; the daemon log says
  `exec: "notify-send": executable file not found`). The container has no desktop. Expected.
- **Human presence** always read `unknown`: there is no desktop session to measure idle time. Expected.
- **One `wait` timed out (exit 4)** in `13-result-loop.txt`: the capture script started theo's `wait` after the change
  request had already arrived. A sequencing mistake in the script, annotated in that file.
- **Two `auth rejected` lines** in the relay log come from `agentnet doctor`'s probes, which never sign the challenge
  by design (`25-relay-log.txt`).
