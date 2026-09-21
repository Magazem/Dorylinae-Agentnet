#!/usr/bin/env bash
# Phase 0 single-machine smoke test (bash: Linux, macOS, Git Bash).
# Runs a local relay and two daemons (A, B) with separate config dirs, then: status, identity,
# pair v2, peers verify, ping, stop/start B, relay restart, peers remove. Prints PASS/FAIL per step.
# Does NOT cover: service install, reboot, real network, offline mail (see tests/phase0-manual.md).
#
# Usage: tests/phase0-smoke.sh [BIN_DIR]     (default: ../bin relative to this script; built if missing)
# Needs: bash, go (only if binaries are missing). No jq or python required.
# Exit code: 0 if all steps passed, 1 otherwise.
set -u

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/.." && pwd)"
bin="${1:-$repo/bin}"
exe=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe=".exe" ;; esac

if [ ! -x "$bin/agentnet$exe" ] || [ ! -x "$bin/agentnetd$exe" ] || [ ! -x "$bin/relay$exe" ]; then
  echo "building into $bin"
  (cd "$repo" && go build -trimpath -o "$bin/" ./cmd/agentnet ./cmd/agentnetd ./cmd/relay) || { echo "build failed"; exit 1; }
fi

# Short path: unix socket paths are limited to about 104 bytes.
work="$(mktemp -d "${TMPDIR:-/tmp}/dsmk.XXXXXX")"
homeA="$work/A"; homeB="$work/B"; mkdir -p "$homeA" "$homeB"
queue="$work/relay-queue.db"

port="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])' 2>/dev/null \
  || echo $((20000 + RANDOM % 20000)))"
relay_url="ws://127.0.0.1:$port"

fails=0
pid_relay=""; pid_A=""; pid_B=""

step() { # step NAME 0|1 [detail]   (0 = ok)
  if [ "$2" -eq 0 ]; then echo "PASS  $1"; else echo "FAIL  $1  ${3:-}"; fails=$((fails + 1)); fi
}
ag() { # ag A|B args...  -> sets OUT and CODE
  local who="$1"; shift
  local h="$homeA"; [ "$who" = B ] && h="$homeB"
  OUT="$(DORYLINAE_HOME="$h" "$bin/agentnet$exe" "$@" 2>&1)"; CODE=$?
}
jget() { # jget KEY  (reads $OUT; first string/number value of "KEY")
  printf '%s' "$OUT" | sed -n "s/.*\"$1\": *\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" | head -n1
}
wait_until() { # wait_until SECONDS cmd...
  local s="$1"; shift; local end=$((SECONDS + s))
  while [ "$SECONDS" -lt "$end" ]; do "$@" && return 0; sleep 0.3; done
  return 1
}
status_ok() { ag "$1" status; [ "$CODE" -eq 0 ]; }
ping_ok() { ag "$1" ping "@$2" --json; [ "$CODE" -eq 0 ] && printf '%s' "$OUT" | grep -q '"state": *"complete"'; }
wait_ping() { wait_until "$3" ping_ok "$1" "$2"; }

start_relay() { "$bin/relay$exe" --listen "127.0.0.1:$port" --queue-db "$queue" --verbose >>"$work/relay.log" 2>&1 & pid_relay=$!; }
start_d() { # start_d A|B
  local h="$homeA"; [ "$1" = B ] && h="$homeB"
  DORYLINAE_HOME="$h" DORYLINAE_RELAY_URL="$relay_url" "$bin/agentnetd$exe" run >>"$work/$1.log" 2>&1 &
  eval "pid_$1=$!"
}
cleanup() {
  for p in "$pid_A" "$pid_B" "$pid_relay"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  wait 2>/dev/null
  if [ "$fails" -eq 0 ]; then rm -rf "$work"; else echo "logs kept in $work"; fi
}
trap cleanup EXIT

echo "work dir: $work  relay: $relay_url"

# 1. relay
start_relay
wait_until 10 grep -q listening "$work/relay.log" 2>/dev/null
# relay logs its 'listening' line on stdout, which we redirected into relay.log too.
step "relay starts and listens" $?

# 2. daemons + status
start_d A; start_d B
wait_until 20 status_ok A; a=$?; wait_until 20 status_ok B; b=$?
step "status: daemon A running" $a; step "status: daemon B running" $b
if [ $((a + b)) -ne 0 ]; then echo "ABORT  daemons did not start"; fails=$((fails + 1)); exit 1; fi
ag B status --json; pidB1="$(jget pid)"

# 3. identity
ag A identity --json; fpA="$(jget fingerprint)"; nameA="$(jget name)"; keyA="$(jget public_key)"
ag B identity --json; fpB="$(jget fingerprint)"; nameB="$(jget name)"; keyB="$(jget public_key)"
[ "${#fpA}" -eq 20 ] && [ "${#fpB}" -eq 20 ]; step "identity: both have 20-char fingerprints" $?
[ "$fpA" != "$fpB" ]; step "identity: fingerprints differ" $?
refA="$nameA"; refB="$nameB"
if [ "$nameA" = "$nameB" ]; then echo "note: both agents are named '$nameA'; using public keys"; refA="$keyA"; refB="$keyB"; fi

# 4. pair v2
ag A pair --new --json; pidA_pair="$(jget pairing_id)"; code="$(jget code)"
if [ -z "$code" ]; then
  for _ in 1 2 3 4 5 6 7 8 9 10; do ag A pair --status "$pidA_pair" --json; code="$(jget code)"; [ -n "$code" ] && break; sleep 0.5; done
fi
c="$(printf '%s' "$code" | tr -d '[:space:]-')"; [ "${#c}" -eq 15 ]; step "pair --new returns a 15-char v2 code" $? "$OUT"
ag B pair "$code" --json
if [ "$(jget state)" = pending ]; then
  pid_r="$(jget pairing_id)"
  for _ in $(seq 1 100); do ag B pair --status "$pid_r" --json; [ "$(jget state)" != pending ] && break; sleep 0.3; done
fi
[ "$(jget state)" = complete ] && [ "$(jget trust)" = code ]; step "pair <code> completes with trust=code" $? "$OUT"
peers_a_one() { ag A peers --json; [ "$(printf '%s' "$OUT" | grep -o '"public_key"' | wc -l)" -eq 1 ]; }
wait_until 30 peers_a_one; step "peers: A lists B" $?
ag A peers --json; pa="$(jget fingerprint)"; ag B peers --json; pb="$(jget fingerprint)"
[ "$pa" = "$fpB" ] && [ "$pb" = "$fpA" ]; step "peers: fingerprints match identity" $?
ag B pair "$code" --json
[ "$CODE" -ne 0 ] && printf '%s' "$OUT" | grep -q code_used; step "pair: reusing the code fails (code_used)" $? "$OUT"

# 5. verify
spaced="$(printf '%s' "$fpB" | sed 's/\(....\)/\1 /g; s/ $//')"
ag A peers verify "$refB" "$spaced"; step "peers verify with correct fingerprint" "$CODE" "$OUT"
first="${fpB:0:1}"; swap=A; [ "$first" = A ] && swap=B
ag A peers verify "$refB" "${swap}${fpB:1}" --json
[ "$CODE" -eq 1 ] && printf '%s' "$OUT" | grep -q fingerprint_mismatch; step "peers verify with wrong fingerprint is rejected" $? "$OUT"
ag A peers --json
[ "$(jget trust)" = fingerprint ]; step "peers: trust is fingerprint after verify" $? "$OUT"

# 6. ping both ways
wait_ping A "$refB" 30; step "ping A -> B" $? "$OUT"
wait_ping B "$refA" 30; step "ping B -> A" $? "$OUT"

# 7. stop B, ping, restart B
kill "$pid_B" 2>/dev/null; wait "$pid_B" 2>/dev/null
ag B status; [ "$CODE" -eq 3 ]; step "status: B not running after stop (exit 3)" $? "code=$CODE"
ag A ping "@$refB" --json
if printf '%s' "$OUT" | grep -q '"pending"'; then
  id="$(jget ping_id)"
  for _ in $(seq 1 50); do ag A ping --status "$id" --json; printf '%s' "$OUT" | grep -q '"pending"' || break; sleep 0.3; done
fi
[ "$CODE" -ne 0 ] && printf '%s' "$OUT" | grep -Eq '"failed"|peer_offline|timeout'; step "ping to stopped B fails cleanly" $? "$OUT"
status_ok A; step "daemon A still running" $?
start_d B
wait_until 20 status_ok B; step "status: B running again" $?
ag B status --json; pidB2="$(jget pid)"
[ "$pidB1" != "$pidB2" ]; step "status: B has a new PID" $? "$pidB1 -> $pidB2"
ag B peers --json; [ "$(printf '%s' "$OUT" | grep -o '"public_key"' | wc -l)" -eq 1 ]; step "peers survive B restart" $?
wait_ping A "$refB" 40; step "ping A -> B after B restart" $? "$OUT"

# 8. relay restart
kill "$pid_relay" 2>/dev/null; wait "$pid_relay" 2>/dev/null
start_relay
wait_ping A "$refB" 60; step "ping A -> B after relay restart" $? "$OUT"

# 9. remove
ag B peers remove "$refA"; step "peers remove" "$CODE" "$OUT"
ag B peers --json; [ "$(printf '%s' "$OUT" | grep -o '"public_key"' | wc -l)" -eq 0 ]; step "peers empty after remove" $?
ag B ping "@$refA" --json
[ "$CODE" -eq 1 ] && printf '%s' "$OUT" | grep -q unknown_peer; step "ping to removed peer fails (unknown_peer)" $? "$OUT"

if [ "$fails" -eq 0 ]; then echo "ALL PASS"; exit 0; fi
echo "$fails FAILED"; exit 1
