#!/usr/bin/env bash
# Ticket 2.H: drives two REAL headless coding-agent harnesses (Claude Code and
# agy) through a live AgentNet request -> accept -> grant -> fetch -> consult
# -> result -> accept-result round trip, using only the plain-English agent
# snippet (Docs/agents/snippet.md) as instructions. Assertions come from
# `agentnet ... --json` output and the daemons' audit logs, never from agent
# prose. `--harness standin` (the default) runs the scripted Go stand-in
# (tests/harness/standin) instead of a real agent, for CI (OD-P2-12).
#
# This is the Linux/macOS port of phase2-agents.ps1; see tests/harness/README.md
# for prerequisites. Report any divergence found running it by hand.
#
# Usage: tests/harness/phase2-agents.sh [--repo-root DIR] [--branch NAME]
#                                        [--skip-build] [--only-round 1|2]
#                                        [--harness standin|real]
#                                        [--sender-harness claude|agy|codex]
#                                        [--recipient-harness claude|agy|codex]
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BRANCH="p2/harness-p2"
SKIP_BUILD=0
ONLY_ROUND=0
AGENT_TIMEOUT_SECONDS=420
TOTAL_TIMEOUT_SECONDS=900
RELAY_PORT_BASE=18887
HARNESS="standin"
SENDER_HARNESS=""
RECIPIENT_HARNESS=""
MAX_ATTEMPTS=1

while [ $# -gt 0 ]; do
  case "$1" in
    --repo-root) REPO_ROOT="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --only-round) ONLY_ROUND="$2"; shift 2 ;;
    --harness) HARNESS="$2"; shift 2 ;;
    --sender-harness) SENDER_HARNESS="$2"; shift 2 ;;
    --recipient-harness) RECIPIENT_HARNESS="$2"; shift 2 ;;
    --max-attempts) MAX_ATTEMPTS="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [ -n "$SENDER_HARNESS" ] || [ -n "$RECIPIENT_HARNESS" ]; then
  if [ -z "$SENDER_HARNESS" ] || [ -z "$RECIPIENT_HARNESS" ]; then
    echo "[2.H] FAIL: --sender-harness and --recipient-harness must be given together" >&2
    exit 2
  fi
fi

SCRIPT_START=$(date +%s)
step() { echo "[2.H] $*"; }
ok()   { echo "[2.H] OK: $*"; }
fail() { echo "[2.H] FAIL: $*" >&2; }

PYTHON=""
for cand in python3 python; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c "import sqlite3" >/dev/null 2>&1; then
    PYTHON="$cand"; break
  fi
done
if [ -z "$PYTHON" ]; then
  echo "[2.H] WARNING: no python3 with sqlite3 found; audit assertions will fail" >&2
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
if [ "$HARNESS" = "standin" ] && [ ! -x "$STANDIN" ]; then fail "standin binary not found under $REPO_ROOT/bin"; exit 1; fi

ROOT_RUN="$(mktemp -d -t phase2-agents-XXXXXX)"
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
# Approval pump (2.2d headless machines): reads "AgentNet approval a-XXXXXX:
# <summary>. Code NNNNNN. ..." off a daemon's stderr (fd 2, via a named pipe)
# and writes "<id> <code>" to its stdin (fd 0, via a second named pipe). This
# is the SCRIPT reading and answering codes, never the agent under test
# (Docs/protocol/approval.md §Headless machines).
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
        timeout "${timeout}s" claude "$prompt" -p --restricted --tools Bash \
          --allowedTools "Bash(agentnet *)" "Bash(sleep *)" --permission-prompts none --output-format json \
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

run_round() { # run_round <round-num> <sender-tool> <recipient-tool> <relay-port> <attempt>
  local round="$1" sender="$2" recipient="$3" port="$4" attempt="${5:-1}"
  local run_dir="$ROOT_RUN/round${round}-attempt${attempt}"
  local relay_home="$run_dir/relay" a_home="$run_dir/a-home" b_home="$run_dir/b-home"
  local a_work="$run_dir/a-work" b_work="$run_dir/b-work" fixture_dir="$run_dir/fixture"
  local context_file="$run_dir/consult-context.md"
  mkdir -p "$relay_home" "$a_home" "$b_home" "$a_work" "$b_work" "$fixture_dir"
  echo "Fixture file for the 2.H headless harness round trip." >"$fixture_dir/NOTES.txt"
  echo "Context for the 2.H consult: this repo's fixture directory holds one small text file." >"$context_file"

  local relay_pid="" a_pid="" b_pid="" a_pump_pid="" b_pump_pid=""
  local a_err_fifo="$run_dir/a.err.fifo" a_in_fifo="$run_dir/a.in.fifo"
  local b_err_fifo="$run_dir/b.err.fifo" b_in_fifo="$run_dir/b.in.fifo"
  mkfifo "$a_err_fifo" "$a_in_fifo" "$b_err_fifo" "$b_in_fifo"
  # Read-write opens of the stdin fifos never block and keep a writer open, so
  # a daemon never sees EOF on its stdin between codes.
  local a_in_fd b_in_fd
  exec {a_in_fd}<>"$a_in_fifo"
  exec {b_in_fd}<>"$b_in_fifo"

  cleanup() {
    [ -n "$a_pump_pid" ] && kill "$a_pump_pid" >/dev/null 2>&1
    [ -n "$b_pump_pid" ] && kill "$b_pump_pid" >/dev/null 2>&1
    [ -n "$a_pid" ] && kill "$a_pid" >/dev/null 2>&1
    [ -n "$b_pid" ] && kill "$b_pid" >/dev/null 2>&1
    [ -n "$relay_pid" ] && kill "$relay_pid" >/dev/null 2>&1
    exec {a_in_fd}>&- {b_in_fd}>&- 2>/dev/null || true
  }
  trap cleanup RETURN

  step "round $round ($sender -> $recipient): starting relay on 127.0.0.1:$port"
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

  step "creating team t2h on A and inviting B"
  cli_json "$a_home" team create t2h >/dev/null
  local invite_json invite_code join_json
  invite_json=$(cli_json "$a_home" team invite t2h)
  invite_code=$(echo "$invite_json" | "$PYTHON" -c "import json,sys;print(json.load(sys.stdin).get('code',''))")
  join_json=$(cli_json "$b_home" team join "$invite_code")
  echo "$join_json" | grep -q '"ok":true' || { fail "team join failed: $join_json"; return 1; }
  wait_until 20 "roster to reach 2 members on A" bash -c \
    "cli_json '$a_home' team show t2h | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get(\"team\",{}).get(\"members\",[]))>=2 else 1)'" || return 1
  ok "team t2h has both members"

  local idem_review="t2h-r${round}-review-$(date +%s)-$$"
  local idem_consult="t2h-r${round}-consult-$(date +%s)-$$"

  if [ "$sender" = "standin" ]; then
    step "invoking stand-in A and B concurrently"
    "$STANDIN" -agentnet "$AGENTNET" -home "$a_home" -role a -peer agent-b \
      -fixture "$fixture_dir" -context "$context_file" -branch "$BRANCH" \
      -review-key "$idem_review" -consult-key "$idem_consult" -timeout "$AGENT_TIMEOUT_SECONDS" \
      >"$run_dir/standin-a.stdout.log" 2>"$run_dir/standin-a.stderr.log" &
    local a_standin_pid=$!
    "$STANDIN" -agentnet "$AGENTNET" -home "$b_home" -role b -timeout "$AGENT_TIMEOUT_SECONDS" \
      >"$run_dir/standin-b.stdout.log" 2>"$run_dir/standin-b.stderr.log" &
    local b_standin_pid=$!
    local a_rc=0 b_rc=0
    wait "$a_standin_pid" || a_rc=$?
    wait "$b_standin_pid" || b_rc=$?
    if [ "$a_rc" -ne 0 ]; then fail "stand-in A exited $a_rc: $(cat "$run_dir/standin-a.stderr.log")"; return 1; fi
    if [ "$b_rc" -ne 0 ]; then fail "stand-in B exited $b_rc: $(cat "$run_dir/standin-b.stderr.log")"; return 1; fi
  else
    local snippet="$a_work/.snippet.md"
    get_snippet_body >"$snippet"
    local a_file="AGENTS.md" b_file="AGENTS.md"
    [ "$sender" = "claude" ] && a_file="CLAUDE.md"
    [ "$recipient" = "claude" ] && b_file="CLAUDE.md"
    cp "$snippet" "$a_work/$a_file"
    cp "$snippet" "$b_work/$b_file"
    rm -f "$snippet"

    local bin_dir="$REPO_ROOT/bin"
    local sender_prompt="You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t2h. Ask agent-b's agent, over AgentNet, to review the directory $fixture_dir (branch $BRANCH) and give agent-b read access to that directory (fs.read) on the same work session. Use exactly this idempotency key for the review request so a retry never sends it twice: $idem_review Wait for agent-b's result (poll every few seconds, up to several minutes). If the result is held back for your release, release it (that asks a human, who is answered for you) and then accept the result. Separately, consult agent-b with one context file ($context_file), asking \"Is the fixture file readable and non-empty?\"; use exactly this idempotency key: $idem_consult Wait for the consult's answer the same way and accept it too. Then stop. To wait, run sleep 5 as its own command and then check again; never chain commands. Run agentnet --help directly as your first command; do not check whether it exists first (for example with which, command -v or type), and do not chain it with any other command."
    local recipient_prompt="Check your AgentNet inbox for anything waiting for you. You should find two items: one asking for a review with a grant of read access to a directory, and one consult question. For the review: accept it, wait until you have read access, list and read the granted file(s) with agentnet fetch, then submit a result with status pass, a one-line summary and a short note describing what you read. For the consult: answer the question with a short result (no need to accept it separately; answering it accepts it). Then stop. To wait, run sleep 5 as its own command and then check again; never chain commands. Run agentnet --help directly as your first command; do not check whether it exists first (for example with which, command -v or type), and do not chain it with any other command."

    step "invoking A ($sender) and B ($recipient) concurrently"
    local a_agent_err b_agent_err a_agent_rc=0 b_agent_rc=0
    a_agent_err=$(invoke_agent "$sender" "$sender_prompt" "$a_work" "$bin_dir" "$a_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/sender-$sender") &
    local a_agent_pid=$!
    b_agent_err=$(invoke_agent "$recipient" "$recipient_prompt" "$b_work" "$bin_dir" "$b_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/recipient-$recipient") &
    local b_agent_pid=$!
    wait "$a_agent_pid" || a_agent_rc=$?
    wait "$b_agent_pid" || b_agent_rc=$?
    if [ "$a_agent_rc" -ne 0 ]; then fail "sender ($sender) could not run: $a_agent_err"; return 1; fi
    if [ "$b_agent_rc" -ne 0 ]; then fail "recipient ($recipient) could not run: $b_agent_err"; return 1; fi
  fi

  step "asserting outcome from --json and audit logs"
  wait_until 30 "exactly one review request on A" bash -c \
    "cli_json '$a_home' request list | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len([r for r in d.get(\"requests\",[]) if r[\"type\"]==\"review\"])==1 else 1)'" || return 1
  wait_until 30 "exactly one question request on A" bash -c \
    "cli_json '$a_home' request list | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len([r for r in d.get(\"requests\",[]) if r[\"type\"]==\"question\"])==1 else 1)'" || return 1
  wait_until 60 "both sessions closed with outcome accepted" bash -c \
    "cli_json '$a_home' sessions --role requester | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len([s for s in d.get(\"sessions\",[]) if s.get(\"state\")==\"closed\" and s.get(\"outcome\")==\"accepted\"])>=2 else 1)'" || return 1

  local grants_json
  grants_json=$(cli_json "$a_home" grants --issued)
  echo "$grants_json" | "$PYTHON" -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get('grants',[]))>=1 else 1)" \
    || { fail "expected at least 1 issued grant on A"; return 1; }
  wait_until 30 "the grant to be revoked after session close" bash -c \
    "cli_json '$a_home' grants --issued | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len([g for g in d.get(\"grants\",[]) if g.get(\"state\")==\"revoked\"])>=1 else 1)'" || return 1

  if [ -z "$PYTHON" ]; then fail "no python3 with sqlite3 module available to read audit_events"; return 1; fi
  local audit_a audit_b missing=""
  audit_a=$(audit_actions "$a_home/dorylinae.db")
  audit_b=$(audit_actions "$b_home/dorylinae.db")
  for action in request.submit request.in request.accept ws.close grant.create grant.fetch; do
    if ! grep -qx "$action" <<<"$audit_a"$'\n'"$audit_b"; then missing="$missing $action"; fi
  done
  if [ -n "$missing" ]; then fail "audit log missing:$missing"; return 1; fi

  ok "round $round ($sender -> $recipient): PASS"
  return 0
}

if [ "$HARNESS" = "standin" ]; then
  ROUNDS=("1 standin standin")
  if [ "$ONLY_ROUND" != "0" ]; then ROUNDS=("${ROUNDS[$((ONLY_ROUND-1))]}"); fi
elif [ -n "$SENDER_HARNESS" ]; then
  ROUNDS=("1 $SENDER_HARNESS $RECIPIENT_HARNESS")
else
  ROUNDS=("1 claude agy" "2 agy claude")
  if [ "$ONLY_ROUND" != "0" ]; then ROUNDS=("${ROUNDS[$((ONLY_ROUND-1))]}"); fi
fi

OVERALL=0
for spec in "${ROUNDS[@]}"; do
  read -r num sender recipient <<<"$spec"
  port=$((RELAY_PORT_BASE + num))
  round_pass=0
  for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
    if run_round "$num" "$sender" "$recipient" "$port" "$attempt"; then
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
echo "=== 2.H summary ==="
elapsed=$(( $(date +%s) - SCRIPT_START ))
echo "  elapsed: ${elapsed}s (limit ${TOTAL_TIMEOUT_SECONDS}s)"
echo "  logs: $ROOT_RUN"
[ "$elapsed" -le "$TOTAL_TIMEOUT_SECONDS" ] || OVERALL=1
exit $OVERALL
