#!/usr/bin/env bash
# Ticket 3.H: drives a debate (Docs/protocol/debate.md) between two REAL
# headless coding-agent harnesses (Claude Code and agy), turn-driven
# (OD-P3-9): this script polls `agentnet debate <id> --json` and, whenever
# it is a side's turn, runs that side's agent headless ONCE with a plain
# prompt, then polls again. `--harness standin` (the default) runs the
# scripted Go stand-in (tests/harness/standin -mode debate) instead of a
# real agent, for CI: it drives both an agreed debate and a forced
# escalation (`-disagree`, deterministic only with the stand-in, OD-P3-7/3.5).
#
# This is the Linux/macOS port of phase3-agents.ps1; see
# tests/harness/README.md for prerequisites. Report any divergence found
# running it by hand.
#
# Usage: tests/harness/phase3-agents.sh [--repo-root DIR] [--skip-build]
#                                        [--only-round 1|2]
#                                        [--harness standin|real]
#                                        [--initiator-harness claude|agy|codex]
#                                        [--respondent-harness claude|agy|codex]
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SKIP_BUILD=0
ONLY_ROUND=0
AGENT_TIMEOUT_SECONDS=420
TOTAL_TIMEOUT_SECONDS=1500
RELAY_PORT_BASE=18897
HARNESS="standin"
INITIATOR_HARNESS=""
RESPONDENT_HARNESS=""
MAX_ATTEMPTS=1

while [ $# -gt 0 ]; do
  case "$1" in
    --repo-root) REPO_ROOT="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --only-round) ONLY_ROUND="$2"; shift 2 ;;
    --harness) HARNESS="$2"; shift 2 ;;
    --initiator-harness) INITIATOR_HARNESS="$2"; shift 2 ;;
    --respondent-harness) RESPONDENT_HARNESS="$2"; shift 2 ;;
    --max-attempts) MAX_ATTEMPTS="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [ -n "$INITIATOR_HARNESS" ] || [ -n "$RESPONDENT_HARNESS" ]; then
  if [ -z "$INITIATOR_HARNESS" ] || [ -z "$RESPONDENT_HARNESS" ]; then
    echo "[3.H] FAIL: --initiator-harness and --respondent-harness must be given together" >&2
    exit 2
  fi
fi

SCRIPT_START=$(date +%s)
step() { echo "[3.H] $*"; }
ok()   { echo "[3.H] OK: $*"; }
fail() { echo "[3.H] FAIL: $*" >&2; }

PYTHON=""
for cand in python3 python; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c "import sqlite3" >/dev/null 2>&1; then
    PYTHON="$cand"; break
  fi
done
if [ -z "$PYTHON" ]; then
  echo "[3.H] WARNING: no python3 with sqlite3 found; audit assertions will fail" >&2
fi

if [ "$SKIP_BUILD" -ne 1 ]; then
  step "building agentnet, agentnetd, relay, and the stand-in"
  export PATH="$HOME/tools/go/bin:$PATH"
  ( cd "$REPO_ROOT" && go build -o bin/ ./cmd/... ) || { fail "go build failed"; exit 1; }
  ( cd "$REPO_ROOT" && go build -o bin/standin ./tests/harness/standin ) || { fail "go build (standin) failed"; exit 1; }
fi

AGENTNET="$REPO_ROOT/bin/agentnet"
RELAY="$REPO_ROOT/bin/relay"
DAEMON="$REPO_ROOT/bin/agentnetd"
STANDIN="$REPO_ROOT/bin/standin"
[ -x "$AGENTNET" ] || AGENTNET="$REPO_ROOT/bin/agentnet.exe"
[ -x "$RELAY" ] || RELAY="$REPO_ROOT/bin/relay.exe"
[ -x "$DAEMON" ] || DAEMON="$REPO_ROOT/bin/agentnetd.exe"
[ -x "$STANDIN" ] || STANDIN="$REPO_ROOT/bin/standin.exe"
[ -x "$AGENTNET" ] || { fail "agentnet binary not found under $REPO_ROOT/bin"; exit 1; }
[ -x "$STANDIN" ] || { fail "standin binary not found under $REPO_ROOT/bin"; exit 1; }

# A short path under /tmp: macOS $TMPDIR (/var/folders/...) is long enough that
# <run>/<round>/<home>/agentnetd.sock passes the 104-byte Unix socket limit,
# and the workflow uploads /tmp/phase3-agents-* on failure.
ROOT_RUN="$(mktemp -d /tmp/phase3-agents-XXXXXX)"
step "run directory: $ROOT_RUN"

cli_json() { # cli_json <home> <args...>  -> prints JSON on stdout, sets CLI_EXIT
  local home="$1"; shift
  local out
  out=$(DORYLINAE_HOME="$home" "$AGENTNET" "$@" --json 2>/tmp/agentnet-stderr.$$)
  CLI_EXIT=$?
  rm -f /tmp/agentnet-stderr.$$
  echo "$out"
}
export AGENTNET PYTHON
export -f cli_json

wait_until() { # wait_until <timeout_s> <what> <command...>
  local timeout="$1" what="$2"; shift 2
  local start elapsed
  start=$(date +%s)
  while true; do
    if "$@"; then return 0; fi
    elapsed=$(( $(date +%s) - start ))
    if [ "$elapsed" -ge "$timeout" ]; then
      fail "timed out waiting for $what"
      return 1
    fi
    sleep 0.5
  done
}

audit_actions() { # audit_actions <db-path>
  local db="$1"
  [ -n "$PYTHON" ] || return 1
  [ -f "$db" ] || { echo ""; return 0; }
  "$PYTHON" -c "
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
for (a,) in con.execute('select action from audit_events order by id'):
    print(a)
" "$db"
}

# ---------------------------------------------------------------------------
# Approval pump (2.2d headless machines, unchanged): reads "AgentNet approval
# a-XXXXXX: <summary>. Code NNNNNN. ..." off a daemon's stderr (fd 2, via a
# named pipe) and writes "<id> <code>" to its stdin (fd 0, via a second named
# pipe). This is the SCRIPT reading and answering codes, never the agent
# under test (Docs/protocol/approval.md §Headless machines). Used both for
# grant-shaped approvals (none in a debate, OD-P3-4) and for the
# `debate_constraint` approval (3.4): same mechanism, no debate-specific code.
# ---------------------------------------------------------------------------

approval_pump() { # approval_pump <stderr-fifo> <stdin-fd> <log>
  local errfifo="$1" infd="$2" log="$3"
  while IFS= read -r line; do
    echo "$line" >>"$log"
    if [[ "$line" =~ AgentNet\ approval\ (a-[0-9a-f]+):.*Code\ ([0-9]+)\. ]]; then
      echo "${BASH_REMATCH[1]} ${BASH_REMATCH[2]}" >&"$infd"
    fi
  done <"$errfifo"
}

get_snippet_body() {
  "$PYTHON" - "$REPO_ROOT/Docs/agents/snippet.md" <<'PYEOF'
import re, sys
text = open(sys.argv[1], encoding="utf-8").read()
m = re.search(r"```markdown\r?\n(.*?)\r?\n```", text, re.S)
if not m:
    sys.exit("no fenced snippet block found")
sys.stdout.write(m.group(1))
PYEOF
}

invoke_agent() { # invoke_agent <tool> <prompt> <workdir> <bindir> <home> <timeout_s> <log-prefix>
  local tool="$1" prompt="$2" workdir="$3" bindir="$4" home="$5" timeout="$6" logpfx="$7"
  local out="$logpfx.stdout.log" err="$logpfx.stderr.log"
  local rc=0
  case "$tool" in
    claude)
      command -v claude >/dev/null 2>&1 || { echo "claude executable not found"; return 1; }
      ( cd "$workdir" && DORYLINAE_HOME="$home" PATH="$bindir:$PATH" \
        timeout "${timeout}s" claude "$prompt" -p --restricted --tools Bash,Skill --model claude-opus-5-5 \
          --allowedTools "Bash(agentnet *)" "Bash(sleep *)" "Bash(cat *)" --permission-prompts none --output-format json \
          --strict-mcp-config --mcp-config '{"mcpServers":{}}' --setting-sources project \
          >"$out" 2>"$err" )
      rc=$?
      ;;
    codex)
      command -v codex >/dev/null 2>&1 || { echo "codex executable not found"; return 1; }
      ( cd "$workdir" && DORYLINAE_HOME="$home" PATH="$bindir:$PATH" \
        timeout "${timeout}s" codex exec --skip-git-repo-check --sandbox workspace-write \
          -C "$workdir" --json "$prompt" >"$out" 2>"$err" )
      rc=$?
      ;;
    agy)
      command -v agy >/dev/null 2>&1 || { echo "agy executable not found"; return 1; }
      # --sandbox is deliberately NOT used: see tests/harness/README.md (UAC
      # elevation prompt headless mode cannot answer on a no-admin machine).
      ( cd "$workdir" && DORYLINAE_HOME="$home" PATH="$bindir:$PATH" \
        timeout "${timeout}s" agy -p "$prompt" --output-format json --print-timeout "${timeout}s" \
          --dangerously-skip-permissions --disable-slash-commands --add-dir "$workdir" \
          >"$out" 2>"$err" )
      rc=$?
      ;;
    *) echo "unknown harness $tool"; return 1 ;;
  esac
  if [ "$rc" -eq 124 ]; then echo "$tool timed out after ${timeout}s"; return 1; fi
  if grep -qiE "usage limit|rate limit|not logged in|authentic|quota" "$out" "$err" 2>/dev/null; then
    echo "$tool reported an auth/usage-limit error (see $out / $err)"
    return 1
  fi
  if [ "$rc" -ne 0 ]; then echo "$tool exited $rc (see $out / $err)"; return 1; fi
  return 0
}

write_fixture() { # write_fixture <fixture-dir>
  local dir="$1"
  cat >"$dir/NOTES.md" <<'EOF'
# Two candidate designs for `Debounce`

This tiny repo has two candidate implementations of a `Debounce(fn, delay)`
helper that should call `fn` only after `delay` has passed with no new calls.

## Design A (`design_a.go`): a single timer, reset on every call

Simple: one `time.Timer`, `Stop()` and re-`Reset()` it on every call. Easy to
read, but every call takes a lock to touch the shared timer.

## Design B (`design_b.go`): a generation counter, no timer reset

Each call bumps an atomic generation counter and starts its own
`time.AfterFunc`; when it fires, it only calls `fn` if its generation is still
the newest. No shared timer to reset, but it spawns one goroutine per call
under heavy load.

Pick whichever you think is the better default and defend it.
EOF
  cat >"$dir/design_a.go" <<'EOF'
package debounce

import (
	"sync"
	"time"
)

// Design A: one timer, reset on every call.
func Debounce(fn func(), delay time.Duration) func() {
	var mu sync.Mutex
	var timer *time.Timer
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(delay, fn)
	}
}
EOF
  cat >"$dir/design_b.go" <<'EOF'
package debounce

import (
	"sync/atomic"
	"time"
)

// Design B: a generation counter, no shared timer to reset.
func Debounce(fn func(), delay time.Duration) func() {
	var gen int64
	return func() {
		g := atomic.AddInt64(&gen, 1)
		time.AfterFunc(delay, func() {
			if atomic.LoadInt64(&gen) == g {
				fn()
			}
		})
	}
}
EOF
}

run_round() { # run_round <round-num> <initiator> <respondent> <scenario> <relay-port> <attempt>
  local round="$1" initiator="$2" respondent="$3" scenario="$4" port="$5" attempt="${6:-1}"
  local run_dir="$ROOT_RUN/round${round}-attempt${attempt}"
  local relay_home="$run_dir/relay" a_home="$run_dir/a-home" b_home="$run_dir/b-home"
  local a_work="$run_dir/a-work" b_work="$run_dir/b-work" fixture_dir="$run_dir/fixture"
  mkdir -p "$relay_home" "$a_home" "$b_home" "$a_work" "$b_work" "$fixture_dir"
  write_fixture "$fixture_dir"

  local relay_pid="" a_pid="" b_pid="" a_pump_pid="" b_pump_pid=""
  local a_err_fifo="$run_dir/a.err.fifo" a_in_fifo="$run_dir/a.in.fifo"
  local b_err_fifo="$run_dir/b.err.fifo" b_in_fifo="$run_dir/b.in.fifo"
  mkfifo "$a_err_fifo" "$a_in_fifo" "$b_err_fifo" "$b_in_fifo"
  # Read-write opens of the stdin fifos never block and keep a writer open, so
  # a daemon never sees EOF on its stdin between codes.
  # Fixed fd numbers: bash 3.2 (macOS) has no `exec {var}<>file`.
  local a_in_fd=7 b_in_fd=8
  exec 7<>"$a_in_fifo"
  exec 8<>"$b_in_fifo"

  cleanup() {
    [ -n "$a_pump_pid" ] && kill "$a_pump_pid" >/dev/null 2>&1
    [ -n "$b_pump_pid" ] && kill "$b_pump_pid" >/dev/null 2>&1
    [ -n "$a_pid" ] && kill "$a_pid" >/dev/null 2>&1
    [ -n "$b_pid" ] && kill "$b_pid" >/dev/null 2>&1
    [ -n "$relay_pid" ] && kill "$relay_pid" >/dev/null 2>&1
    exec 7>&- 8>&- || true
  }
  trap cleanup RETURN

  step "round $round ($initiator -> $respondent, $scenario): starting relay on 127.0.0.1:$port"
  "$RELAY" --listen "127.0.0.1:$port" --queue-db "$relay_home/relay-queue.db" \
    >"$run_dir/relay.out.log" 2>"$run_dir/relay.err.log" &
  relay_pid=$!
  sleep 0.5
  kill -0 "$relay_pid" 2>/dev/null || { fail "relay exited immediately"; return 1; }

  local relay_url="ws://127.0.0.1:$port"
  step "starting daemon A and B with DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1"
  ( cd "$a_home" && DORYLINAE_HOME="$a_home" DORYLINAE_AGENT_NAME="agent-a" \
      DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1 "$DAEMON" --relay "$relay_url" \
      <"$a_in_fifo" >"$run_dir/daemonA.out.log" 2>"$a_err_fifo" ) &
  a_pid=$!
  ( cd "$b_home" && DORYLINAE_HOME="$b_home" DORYLINAE_AGENT_NAME="agent-b" \
      DORYLINAE_APPROVAL=terminal DORYLINAE_DEBUG=1 "$DAEMON" --relay "$relay_url" \
      <"$b_in_fifo" >"$run_dir/daemonB.out.log" 2>"$b_err_fifo" ) &
  b_pid=$!

  approval_pump "$a_err_fifo" "$a_in_fd" "$run_dir/daemonA.approvals.log" &
  a_pump_pid=$!
  approval_pump "$b_err_fifo" "$b_in_fd" "$run_dir/daemonB.approvals.log" &
  b_pump_pid=$!

  wait_until 15 "daemon A ready" bash -c "cli_json '$a_home' status >/dev/null; [ \$CLI_EXIT -eq 0 ]" || return 1
  wait_until 15 "daemon B ready" bash -c "cli_json '$b_home' status >/dev/null; [ \$CLI_EXIT -eq 0 ]" || return 1
  ok "both daemons answer status"

  step "pairing A and B"
  local new_json code redeem_json
  new_json=$(cli_json "$a_home" pair --new)
  code=$(echo "$new_json" | "$PYTHON" -c "import json,sys;print(json.load(sys.stdin).get('code',''))")
  [ -n "$code" ] || { fail "pair --new returned no code: $new_json"; return 1; }
  redeem_json=$(cli_json "$b_home" pair "$code")
  echo "$redeem_json" | grep -q '"state":"complete"' || { fail "pair redeem did not complete: $redeem_json"; return 1; }
  ok "paired"

  step "creating team t3h on A and inviting B"
  cli_json "$a_home" team create t3h >/dev/null
  local invite_json invite_code join_json
  invite_json=$(cli_json "$a_home" team invite t3h)
  invite_code=$(echo "$invite_json" | "$PYTHON" -c "import json,sys;print(json.load(sys.stdin).get('code',''))")
  join_json=$(cli_json "$b_home" team join "$invite_code")
  echo "$join_json" | grep -q '"ok":true' || { fail "team join failed: $join_json"; return 1; }
  wait_until 20 "roster to reach 2 members on A" bash -c \
    "cli_json '$a_home' team show t3h | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get(\"team\",{}).get(\"members\",[]))>=2 else 1)'" || return 1
  ok "team t3h has both members"

  local idem_key="t3h-r${round}-debate-$(date +%s)-$$"
  local constraint_text="Keep the debounce helper dependency-free (standard library only)."
  local session_id=""

  if [ "$initiator" = "standin" ]; then
    step "invoking stand-in A and B concurrently ($scenario)"
    local disagree_flag=()
    [ "$scenario" = "escalated" ] && disagree_flag=(-disagree)
    "$STANDIN" -mode debate -agentnet "$AGENTNET" -home "$a_home" -role a -peer agent-b \
      -topic "Which Debounce design should we use: a single reset timer, or a generation counter?" \
      -claim "Use design A (a single reset timer)." \
      -argument "It is easier to read and audit, and the lock it takes is uncontended in the common case." \
      -rounds 2 -debate-key "$idem_key" -timeout "$AGENT_TIMEOUT_SECONDS" \
      >"$run_dir/standin-a.stdout.log" 2>"$run_dir/standin-a.stderr.log" &
    local a_standin_pid=$!
    "$STANDIN" -mode debate -agentnet "$AGENTNET" -home "$b_home" -role b \
      -claim "Use design B (a generation counter)." \
      -argument "It never blocks on a shared timer, which matters under heavy call rates." \
      -timeout "$AGENT_TIMEOUT_SECONDS" ${disagree_flag[@]+"${disagree_flag[@]}"} \
      >"$run_dir/standin-b.stdout.log" 2>"$run_dir/standin-b.stderr.log" &
    local b_standin_pid=$!

    wait_until 30 "the debate session id to appear on A" bash -c \
      "cli_json '$a_home' debates | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get(\"debates\",[]))>=1 else 1)'" || { kill "$a_standin_pid" "$b_standin_pid" 2>/dev/null; return 1; }
    session_id=$(cli_json "$a_home" debates | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['debates'][0]['session'])")
    wait_until 60 "both positions to exist (phase past positions)" bash -c \
      "cli_json '$a_home' debate '$session_id' | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d[\"debate\"][\"phase\"] not in (\"invited\",\"positions\") else 1)'" || { kill "$a_standin_pid" "$b_standin_pid" 2>/dev/null; return 1; }
    local constrain_json
    constrain_json=$(cli_json "$a_home" debate "$session_id" --constrain "$constraint_text")
    echo "$constrain_json" | grep -q '"ok":true' || { fail "--constrain failed: $constrain_json"; kill "$a_standin_pid" "$b_standin_pid" 2>/dev/null; return 1; }

    local a_rc=0 b_rc=0
    wait "$a_standin_pid" || a_rc=$?
    wait "$b_standin_pid" || b_rc=$?
    if [ "$a_rc" -ne 0 ]; then fail "stand-in A exited $a_rc: $(cat "$run_dir/standin-a.stderr.log")"; return 1; fi
    if [ "$b_rc" -ne 0 ]; then fail "stand-in B exited $b_rc: $(cat "$run_dir/standin-b.stderr.log")"; return 1; fi
  else
    local snippet_body a_file="AGENTS.md" b_file="AGENTS.md"
    snippet_body=$(get_snippet_body)
    [ "$initiator" = "claude" ] && a_file="CLAUDE.md"
    [ "$respondent" = "claude" ] && b_file="CLAUDE.md"
    echo "$snippet_body" >"$a_work/$a_file"
    echo "$snippet_body" >"$b_work/$b_file"
    cp "$fixture_dir/NOTES.md" "$a_work/NOTES.md"
    cp "$fixture_dir/NOTES.md" "$b_work/NOTES.md"
    # --setting-sources project loads skills only from the agent's own work dir.
    mkdir -p "$a_work/.claude/skills/agentnet-debate" "$b_work/.claude/skills/agentnet-debate"
    cp "$REPO_ROOT/.claude/skills/agentnet-debate/SKILL.md" "$a_work/.claude/skills/agentnet-debate/SKILL.md"
    cp "$REPO_ROOT/.claude/skills/agentnet-debate/SKILL.md" "$b_work/.claude/skills/agentnet-debate/SKILL.md"

    local bin_dir="$REPO_ROOT/bin"
    local start_prompt="You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t3h. Read NOTES.md in your working directory: it describes two candidate designs of one function. Start a debate with agent-b about which design is better, arguing for whichever you judge stronger, with at most 2 rounds. Use exactly this idempotency key so a retry never starts it twice: $idem_key Then stop; you will be asked to take further steps in the same debate later."
    local join_prompt="A teammate's agent (agent-a) invited you to an AgentNet debate. Read NOTES.md in your working directory: it describes two candidate designs of one function. Check for the debate and take your next step in it, arguing for whichever design you judge stronger. Then stop; you will be asked to take further steps in the same debate later."
    local cli_hint=' The CLI is `agentnet`; start with `agentnet --help`.'
    [ "$initiator" = "claude" ] && start_prompt="$start_prompt$cli_hint"
    [ "$respondent" = "claude" ] && join_prompt="$join_prompt$cli_hint"
    local follow_up_prompt="Your AgentNet debate with your teammate is waiting for you. Take your next step, then stop."

    local a_started=0 b_joined=0 constraint_done=0 turn_count=0 max_turns=14
    local deadline=$(( $(date +%s) + (AGENT_TIMEOUT_SECONDS * 8 < TOTAL_TIMEOUT_SECONDS - 60 ? AGENT_TIMEOUT_SECONDS * 8 : TOTAL_TIMEOUT_SECONDS - 60) ))

    while true; do
      if [ "$(date +%s)" -gt "$deadline" ]; then fail "turn-driven loop exceeded its deadline"; return 1; fi
      if [ "$turn_count" -ge "$max_turns" ]; then fail "gave up after $max_turns agent turns without the debate closing"; return 1; fi

      if [ "$a_started" -eq 0 ]; then
        step "turn $((turn_count+1)): initiator ($initiator) starts the debate"
        local err
        err=$(invoke_agent "$initiator" "$start_prompt" "$a_work" "$bin_dir" "$a_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/turn$((turn_count+1))-a-start") || { fail "initiator ($initiator) could not start the debate: $err"; return 1; }
        turn_count=$((turn_count+1)); a_started=1
        continue
      fi

      if [ -z "$session_id" ]; then
        local list_json
        list_json=$(cli_json "$a_home" debates)
        session_id=$(echo "$list_json" | "$PYTHON" -c "import json,sys
d=json.load(sys.stdin)
ds=d.get('debates',[])
print(ds[0]['session'] if ds else '')")
        [ -n "$session_id" ] || { sleep 1; continue; }
      fi

      local a_view phase
      a_view=$(cli_json "$a_home" debate "$session_id")
      phase=$(echo "$a_view" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin).get('debate',{}).get('phase',''))" 2>/dev/null)
      [ -n "$phase" ] || { sleep 1; continue; }
      if [ "$phase" = "closed" ] || [ "$phase" = "broken" ]; then break; fi

      if [ "$b_joined" -eq 0 ]; then
        local b_list
        b_list=$(cli_json "$b_home" debates --phase invited)
        if echo "$b_list" | "$PYTHON" -c "import json,sys; sys.exit(0 if len(json.load(sys.stdin).get('debates',[]))>=1 else 1)"; then
          step "turn $((turn_count+1)): respondent ($respondent) joins the debate"
          err=$(invoke_agent "$respondent" "$join_prompt" "$b_work" "$bin_dir" "$b_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/turn$((turn_count+1))-b-join") || { fail "respondent ($respondent) could not join the debate: $err"; return 1; }
          turn_count=$((turn_count+1)); b_joined=1
          continue
        fi
        sleep 1
        continue
      fi

      if [ "$constraint_done" -eq 0 ] && [ "$phase" != "invited" ] && [ "$phase" != "positions" ]; then
        step "adding a human constraint on A (the script, not an agent, as OD-P3-3 requires)"
        local constrain_json
        constrain_json=$(cli_json "$a_home" debate "$session_id" --constrain "$constraint_text")
        echo "$constrain_json" | grep -q '"ok":true' || { fail "--constrain failed: $constrain_json"; return 1; }
        constraint_done=1
        sleep 1.5
        continue
      fi

      local a_turn
      a_turn=$(echo "$a_view" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin).get('debate',{}).get('turn',''))")
      if [ "$a_turn" = "you" ]; then
        step "turn $((turn_count+1)): initiator ($initiator)'s turn"
        err=$(invoke_agent "$initiator" "$follow_up_prompt" "$a_work" "$bin_dir" "$a_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/turn$((turn_count+1))-a") || { fail "initiator ($initiator) turn failed: $err"; return 1; }
        turn_count=$((turn_count+1))
        continue
      fi

      local b_view b_turn
      b_view=$(cli_json "$b_home" debate "$session_id")
      b_turn=$(echo "$b_view" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin).get('debate',{}).get('turn',''))" 2>/dev/null)
      if [ "$b_turn" = "you" ]; then
        step "turn $((turn_count+1)): respondent ($respondent)'s turn"
        err=$(invoke_agent "$respondent" "$follow_up_prompt" "$b_work" "$bin_dir" "$b_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/turn$((turn_count+1))-b") || { fail "respondent ($respondent) turn failed: $err"; return 1; }
        turn_count=$((turn_count+1))
        continue
      fi

      sleep 1
    done
  fi

  step "asserting outcome from --json and audit logs"
  if [ -z "$session_id" ]; then
    session_id=$(cli_json "$a_home" debates | "$PYTHON" -c "import json,sys
d=json.load(sys.stdin)
ds=d.get('debates',[])
print(ds[0]['session'] if ds else '')")
    [ -n "$session_id" ] || { fail "no debate found on A after the run"; return 1; }
  fi

  local list_json
  list_json=$(cli_json "$a_home" request list)
  echo "$list_json" | "$PYTHON" -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if len([r for r in d.get('requests',[]) if r['type']=='debate'])==1 else 1)" \
    || { fail "expected exactly one debate request on A"; return 1; }

  wait_until 60 "debate closed on A" bash -c \
    "cli_json '$a_home' debate '$session_id' | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d[\"debate\"][\"phase\"] in (\"closed\",\"broken\") else 1)'" || return 1
  local final_a phase outcome rounds_current decision_id
  final_a=$(cli_json "$a_home" debate "$session_id")
  phase=$(echo "$final_a" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['debate']['phase'])")
  outcome=$(echo "$final_a" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['debate'].get('outcome',''))")
  rounds_current=$(echo "$final_a" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['debate']['rounds']['current'])")
  decision_id=$(echo "$final_a" | "$PYTHON" -c "import json,sys; print(json.load(sys.stdin)['debate'].get('decision',{}).get('id',''))")
  [ "$phase" = "closed" ] || { fail "debate ended phase $phase, not closed"; return 1; }
  case "$outcome" in agreed|escalated) ;; *) fail "unexpected outcome $outcome"; return 1 ;; esac
  [ "$rounds_current" -le 2 ] || { fail "rounds.current $rounds_current exceeds 2"; return 1; }
  [ -n "$decision_id" ] || { fail "no decision id on the closed debate"; return 1; }

  wait_until 60 "debate closed on B" bash -c \
    "cli_json '$b_home' debate '$session_id' | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d[\"debate\"][\"phase\"] in (\"closed\",\"broken\") else 1)'" || return 1

  local decision_json_path="$run_dir/decision.json"
  DORYLINAE_HOME="$a_home" "$AGENTNET" decision "$decision_id" --out "$decision_json_path" --force --json >/dev/null 2>"$run_dir/decision-export.err.log"
  [ -f "$decision_json_path" ] || { fail "decision --json --out failed: $(cat "$run_dir/decision-export.err.log")"; return 1; }

  local verify_json verify_rc=0
  verify_json=$("$AGENTNET" decision verify "$decision_json_path" --json) || verify_rc=$?
  [ "$verify_rc" -eq 0 ] || { fail "decision verify exited $verify_rc (want 0, two signatures): $verify_json"; return 1; }
  echo "$verify_json" | "$PYTHON" -c "import json,sys; sys.exit(0 if json.load(sys.stdin).get('complete') else 1)" \
    || { fail "decision verify: not signed by both sides: $verify_json"; return 1; }

  local decision_md_path="$run_dir/decision.md"
  DORYLINAE_HOME="$a_home" "$AGENTNET" decision "$decision_id" --md --out "$decision_md_path" --force >/dev/null 2>"$run_dir/decision-md.err.log"
  [ -f "$decision_md_path" ] || { fail "decision --md --out failed: $(cat "$run_dir/decision-md.err.log")"; return 1; }

  "$PYTHON" -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if len(d['decision'].get('human_decisions',[]))>=1 else 1)" "$decision_json_path" \
    || { fail "expected the human constraint in human_decisions, found none"; return 1; }

  if [ -z "$PYTHON" ]; then fail "no python3 with sqlite3 module available to read audit_events"; return 1; fi
  local audit_a audit_b missing=""
  audit_a=$(audit_actions "$a_home/dorylinae.db")
  audit_b=$(audit_actions "$b_home/dorylinae.db")
  for action in request.submit request.in request.accept; do
    if ! grep -qx "$action" <<<"$audit_a"$'\n'"$audit_b"; then missing="$missing $action"; fi
  done
  if [ -n "$missing" ]; then fail "audit log missing:$missing"; return 1; fi

  for home in "$a_home" "$b_home"; do
    DORYLINAE_HOME="$home" "$AGENTNET" log --verify >/dev/null 2>"$run_dir/log-verify.$$.err.log" \
      || { fail "log --verify on $home failed: $(cat "$run_dir/log-verify.$$.err.log")"; return 1; }
  done

  ok "round $round ($initiator -> $respondent, $scenario): PASS (outcome=$outcome)"
  return 0
}

if [ "$HARNESS" = "standin" ]; then
  # 3.5/OD-P3-7: the forced escalation is deterministic only with the
  # stand-in, so the weekly CI job covers both an agreed debate and a forced
  # escalation here (real-agent rounds never force disagreement).
  ROUNDS=("1 standin standin agreed" "2 standin standin escalated")
  if [ "$ONLY_ROUND" != "0" ]; then ROUNDS=("${ROUNDS[$((ONLY_ROUND-1))]}"); fi
elif [ -n "$INITIATOR_HARNESS" ]; then
  ROUNDS=("1 $INITIATOR_HARNESS $RESPONDENT_HARNESS agreed")
else
  ROUNDS=("1 claude agy agreed" "2 agy claude agreed")
  if [ "$ONLY_ROUND" != "0" ]; then ROUNDS=("${ROUNDS[$((ONLY_ROUND-1))]}"); fi
fi

OVERALL=0
for spec in "${ROUNDS[@]}"; do
  read -r num initiator respondent scenario <<<"$spec"
  port=$((RELAY_PORT_BASE + num))
  round_pass=0
  for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
    if run_round "$num" "$initiator" "$respondent" "$scenario" "$port" "$attempt"; then
      round_pass=1
      break
    fi
  done
  [ "$round_pass" -eq 1 ] || OVERALL=1
  now=$(date +%s)
  if [ $((now - SCRIPT_START)) -gt "$TOTAL_TIMEOUT_SECONDS" ]; then
    fail "total run time exceeded ${TOTAL_TIMEOUT_SECONDS}s; stopping"
    OVERALL=1
    break
  fi
done

echo ""
echo "=== 3.H summary ==="
elapsed=$(( $(date +%s) - SCRIPT_START ))
echo "  elapsed: ${elapsed}s (limit ${TOTAL_TIMEOUT_SECONDS}s)"
echo "  logs: $ROOT_RUN"
[ "$elapsed" -le "$TOTAL_TIMEOUT_SECONDS" ] || OVERALL=1
exit $OVERALL
