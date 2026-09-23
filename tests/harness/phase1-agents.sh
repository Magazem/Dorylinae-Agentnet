#!/usr/bin/env bash
# Ticket 1.H: drives two REAL headless coding-agent harnesses (Claude Code and
# Codex CLI) through a live AgentNet request -> inbox -> accept -> complete
# round trip, using only the plain-English agent snippet (Docs/agents/snippet.md)
# as instructions. Assertions come from `agentnet ... --json` output and the
# daemons' audit logs, never from agent prose.
#
# This is the Linux/macOS port of phase1-agents.ps1; see tests/harness/README.md
# for prerequisites. It has not been run in this environment (Windows-only
# sandbox); the PowerShell script is the one that was actually exercised for
# ticket 1.H. Report any divergence found running this one by hand.
#
# Usage: tests/harness/phase1-agents.sh [--repo-root DIR] [--branch NAME]
#                                        [--skip-build] [--only-round 1|2]
#                                        [--sender-harness claude|codex]
#                                        [--recipient-harness claude|codex]
#
# --sender-harness and --recipient-harness must be given together; they
# replace the default two swapped Claude/Codex rounds with a single round
# using the given harness for each role (e.g. an all-Claude smoke test).
# Harnesses: claude, codex, agy (Antigravity CLI).
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BRANCH="p1/t1-H"
SKIP_BUILD=0
ONLY_ROUND=0
AGENT_TIMEOUT_SECONDS=180
TOTAL_TIMEOUT_SECONDS=600
RELAY_PORT_BASE=18787
SENDER_HARNESS=""
RECIPIENT_HARNESS=""
MAX_ATTEMPTS=1

while [ $# -gt 0 ]; do
  case "$1" in
    --repo-root) REPO_ROOT="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    --only-round) ONLY_ROUND="$2"; shift 2 ;;
    --sender-harness) SENDER_HARNESS="$2"; shift 2 ;;
    --recipient-harness) RECIPIENT_HARNESS="$2"; shift 2 ;;
    --max-attempts) MAX_ATTEMPTS="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [ -n "$SENDER_HARNESS" ] || [ -n "$RECIPIENT_HARNESS" ]; then
  if [ -z "$SENDER_HARNESS" ] || [ -z "$RECIPIENT_HARNESS" ]; then
    echo "[1.H] FAIL: --sender-harness and --recipient-harness must be given together" >&2
    exit 2
  fi
fi

SCRIPT_START=$(date +%s)
step() { echo "[1.H] $*"; }
ok()   { echo "[1.H] OK: $*"; }
fail() { echo "[1.H] FAIL: $*" >&2; }

PYTHON=""
for cand in python3 python; do
  if command -v "$cand" >/dev/null 2>&1 && "$cand" -c "import sqlite3" >/dev/null 2>&1; then
    PYTHON="$cand"; break
  fi
done
if [ -z "$PYTHON" ]; then
  echo "[1.H] WARNING: no python3 with sqlite3 found; audit assertions will fail (see README)" >&2
fi

if [ "$SKIP_BUILD" -ne 1 ]; then
  step "building agentnet, agentnetd, relay"
  export PATH="$HOME/tools/go/bin:$PATH"
  ( cd "$REPO_ROOT" && go build -o bin/ ./cmd/... ) || { fail "go build failed"; exit 1; }
fi

AGENTNET="$REPO_ROOT/bin/agentnet"
RELAY="$REPO_ROOT/bin/relay"
DAEMON="$REPO_ROOT/bin/agentnetd"
[ -x "$AGENTNET" ] || AGENTNET="$REPO_ROOT/bin/agentnet.exe"
[ -x "$RELAY" ] || RELAY="$REPO_ROOT/bin/relay.exe"
[ -x "$DAEMON" ] || DAEMON="$REPO_ROOT/bin/agentnetd.exe"
[ -x "$AGENTNET" ] || { fail "agentnet binary not found under $REPO_ROOT/bin"; exit 1; }

ROOT_RUN="$(mktemp -d -t phase1-agents-XXXXXX)"
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

get_snippet_body() {
  # Extracts the fenced ```markdown ... ``` block from Docs/agents/snippet.md.
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
      # --setting-sources project (drops user-level settings, where account
      # connectors like Google Drive/Picsart/Claude Docs are enabled) and
      # --strict-mcp-config with an empty config (drops every MCP server)
      # keep those out of the tool list, which otherwise makes the model
      # think it needs a dedicated "AgentNet" tool instead of running the
      # agentnet CLI via Bash.
      ( cd "$workdir" && DORYLINAE_HOME="$home" PATH="$bindir:$PATH" \
        timeout "${timeout}s" claude "$prompt" -p --restricted --tools Bash \
          --allowedTools "Bash(agentnet *)" --permission-prompts none --output-format json \
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
      # agy (Antigravity CLI) has no verified per-binary command allowlist
      # equivalent to Claude's --allowedTools "Bash(agentnet *)" (see
      # phase1-agents.ps1 and tests/phase1-manual.md for the settings.json
      # permissions.allow(command(...)) investigation). Falls back to
      # --dangerously-skip-permissions, compensated by $workdir holding only
      # the AGENTS.md snippet. --sandbox is deliberately NOT used: on the
      # Windows machine this ticket was run on (no admin rights) it triggered
      # a UAC elevation prompt that headless mode cannot answer; do not add
      # it back without confirming the target has admin rights, or that
      # agy's sandbox no longer needs elevation. No MCP servers are
      # configured on this account.
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
  local a_work="$run_dir/a-work" b_work="$run_dir/b-work"
  mkdir -p "$relay_home" "$a_home" "$b_home" "$a_work" "$b_work"

  local relay_pid="" a_pid="" b_pid=""
  cleanup() {
    [ -n "$a_pid" ] && kill "$a_pid" >/dev/null 2>&1
    [ -n "$b_pid" ] && kill "$b_pid" >/dev/null 2>&1
    [ -n "$relay_pid" ] && kill "$relay_pid" >/dev/null 2>&1
  }
  trap cleanup RETURN

  step "round $round ($sender -> $recipient): starting relay on 127.0.0.1:$port"
  "$RELAY" --listen "127.0.0.1:$port" --queue-db "$relay_home/relay-queue.db" \
    >"$run_dir/relay.out.log" 2>"$run_dir/relay.err.log" &
  relay_pid=$!
  sleep 0.5
  kill -0 "$relay_pid" 2>/dev/null || { fail "relay exited immediately (see $run_dir/relay.err.log)"; return 1; }

  local relay_url="ws://127.0.0.1:$port"
  step "starting daemon A (agent-a) and daemon B (agent-b)"
  ( cd "$a_home" && DORYLINAE_HOME="$a_home" DORYLINAE_AGENT_NAME="agent-a" "$DAEMON" --relay "$relay_url" \
      >"$run_dir/daemonA.out.log" 2>"$run_dir/daemonA.err.log" ) &
  a_pid=$!
  ( cd "$b_home" && DORYLINAE_HOME="$b_home" DORYLINAE_AGENT_NAME="agent-b" "$DAEMON" --relay "$relay_url" \
      >"$run_dir/daemonB.out.log" 2>"$run_dir/daemonB.err.log" ) &
  b_pid=$!

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

  step "creating team t1h on A and inviting B"
  cli_json "$a_home" team create t1h >/dev/null
  local invite_json invite_code join_json
  invite_json=$(cli_json "$a_home" team invite t1h)
  invite_code=$(echo "$invite_json" | "$PYTHON" -c "import json,sys;print(json.load(sys.stdin).get('code',''))")
  [ -n "$invite_code" ] || { fail "team invite returned no code: $invite_json"; return 1; }
  join_json=$(cli_json "$b_home" team join "$invite_code")
  echo "$join_json" | grep -q '"ok":true' || { fail "team join failed: $join_json"; return 1; }

  wait_until 20 "roster to reach 2 members on A" bash -c \
    "cli_json '$a_home' team show t1h | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get(\"team\",{}).get(\"members\",[]))>=2 else 1)'" || return 1
  ok "team t1h has both members"

  local snippet="$a_work/.snippet.md"
  get_snippet_body >"$snippet"
  local a_file="AGENTS.md" b_file="AGENTS.md"
  [ "$sender" = "claude" ] && a_file="CLAUDE.md"
  [ "$recipient" = "claude" ] && b_file="CLAUDE.md"
  cp "$snippet" "$a_work/$a_file"
  cp "$snippet" "$b_work/$b_file"
  rm -f "$snippet"

  local idem_key="t1h-r$round-$(date +%s)-$$"
  # The "Run agentnet --help directly..." sentence is harness-only guidance (not
  # part of the product snippet, which stays short and general per the ticket): it
  # heads off the model probing for the CLI with which/command -v/type, chained
  # into one command with `;`/`&&` -- a chained command is denied whole by
  # --allowedTools's prefix match (Claude Code will not auto-approve a
  # multi-statement command off a prefix match), so the agent would see one
  # denial and give up instead of trying a plain `agentnet --help`.
  local sender_prompt="You are working with a teammate whose AgentNet peer name is agent-b, on the shared team t1h. Ask agent-b's agent, over AgentNet, for a code review of the branch $BRANCH. Use exactly this idempotency key so a retry never sends the request twice: $idem_key Send the request and then stop. Do not do anything else, and do not wait for the answer. Run agentnet --help directly as your first command; do not check whether it exists first (for example with which, command -v or type), and do not chain it with any other command."
  local recipient_prompt="Check your AgentNet inbox for anything waiting for you. Accept whatever is there. Do not do the review, and do not do anything else with it. Then mark it complete with the short note \"Acknowledged, no work performed\" and a result with status n/a and the one-line summary \"Acknowledged, no work performed.\" Then stop. Run agentnet --help directly as your first command; do not check whether it exists first (for example with which, command -v or type), and do not chain it with any other command."

  local bin_dir="$REPO_ROOT/bin"
  step "invoking sender agent ($sender) in $a_work"
  local sender_err
  sender_err=$(invoke_agent "$sender" "$sender_prompt" "$a_work" "$bin_dir" "$a_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/sender-$sender")
  if [ $? -ne 0 ]; then fail "sender ($sender) could not run: $sender_err"; return 1; fi

  wait_until 30 "a request to appear in A's outbox" bash -c \
    "cli_json '$a_home' request list | $PYTHON -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if len(d.get(\"requests\",[]))>=1 else 1)'" || return 1

  local list_a req_count req_id
  list_a=$(cli_json "$a_home" request list)
  req_count=$(echo "$list_a" | "$PYTHON" -c "import json,sys;print(len(json.load(sys.stdin).get('requests',[])))")
  if [ "$req_count" -ne 1 ]; then fail "expected exactly 1 request on A, found $req_count"; return 1; fi
  req_id=$(echo "$list_a" | "$PYTHON" -c "import json,sys;print(json.load(sys.stdin)['requests'][0]['id'])")
  ok "sender agent queued exactly one request: $req_id"

  wait_until 30 "request delivered to B's inbox" bash -c \
    "cli_json '$b_home' inbox | grep -q '$req_id'" || return 1

  step "invoking recipient agent ($recipient) in $b_work"
  local recip_err
  recip_err=$(invoke_agent "$recipient" "$recipient_prompt" "$b_work" "$bin_dir" "$b_home" "$AGENT_TIMEOUT_SECONDS" "$run_dir/recipient-$recipient")
  if [ $? -ne 0 ]; then fail "recipient ($recipient) could not run: $recip_err"; return 1; fi

  wait_until 30 "B's inbox to show it completed" bash -c \
    "cli_json '$b_home' inbox --all | $PYTHON -c \"import json,sys; d=json.load(sys.stdin); sys.exit(0 if any(r['id']=='$req_id' and r['state']=='completed' for r in d.get('requests',[])) else 1)\"" || return 1
  wait_until 30 "A's mirror to show it completed" bash -c \
    "cli_json '$a_home' request show '$req_id' | grep -q '\"state\":\"completed\"'" || return 1

  local show_a
  show_a=$(cli_json "$a_home" request show "$req_id")
  echo "$show_a" | "$PYTHON" -c "
import json, sys
d = json.load(sys.stdin)['request']
assert d['state'] == 'completed', d['state']
assert d.get('note'), 'no completion note'
r = d.get('result')
assert r, 'no D14 result'
assert r.get('status') in ('n/a', 'pass'), r.get('status')
assert r.get('summary'), 'no result summary'
" || { fail "A's mirror assertions failed for $req_id: $show_a"; return 1; }

  list_a=$(cli_json "$a_home" request list)
  req_count=$(echo "$list_a" | "$PYTHON" -c "import json,sys;print(len(json.load(sys.stdin).get('requests',[])))")
  if [ "$req_count" -ne 1 ]; then fail "duplicate request detected: $req_count requests on A"; return 1; fi

  if [ -z "$PYTHON" ]; then
    fail "no python3 with sqlite3 module available to read audit_events"
    return 1
  fi
  local audit_a audit_b missing=""
  audit_a=$(audit_actions "$a_home/dorylinae.db")
  audit_b=$(audit_actions "$b_home/dorylinae.db")
  for action in request.submit request.in request.accept request.complete request.state; do
    if ! grep -qx "$action" <<<"$audit_a$'\n'$audit_b"; then missing="$missing $action"; fi
  done
  if [ -n "$missing" ]; then
    fail "audit log missing:$missing (A: $audit_a; B: $audit_b)"
    return 1
  fi

  ok "round $round ($sender -> $recipient): PASS ($req_id)"
  return 0
}

if [ -n "$SENDER_HARNESS" ]; then
  ROUNDS=("1 $SENDER_HARNESS $RECIPIENT_HARNESS")
else
  # agy (Antigravity CLI) is the documented second harness while Codex CLI's
  # account usage limit is in effect (resets 2026-10-02; see
  # tests/phase1-manual.md). Codex remains supported via --sender-harness/
  # --recipient-harness codex.
  ROUNDS=("1 claude agy" "2 agy claude")
  if [ "$ONLY_ROUND" != "0" ]; then
    ROUNDS=("${ROUNDS[$((ONLY_ROUND-1))]}")
  fi
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
echo "=== 1.H summary ==="
elapsed=$(( $(date +%s) - SCRIPT_START ))
echo "  elapsed: ${elapsed}s (limit ${TOTAL_TIMEOUT_SECONDS}s)"
echo "  logs: $ROOT_RUN"
[ "$elapsed" -le "$TOTAL_TIMEOUT_SECONDS" ] || OVERALL=1
exit $OVERALL
