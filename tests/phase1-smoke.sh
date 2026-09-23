#!/usr/bin/env bash
# Phase 1 single-machine smoke test (bash: Linux, macOS, Git Bash), no admin rights.
# Builds the binaries, runs a local relay (--queue-db) and four daemons (A, B, C, D)
# with separate config dirs as plain child processes (no service install), and drives:
# pairing v2 + fingerprints; team create/invite/join (incl. a third member); presence
# levels and --only-team visibility; request submit with brief/artifacts/urgency, queued
# while offline then delivered; inbox ordering by urgency; accept/decline/defer/complete
# with a D14 result; cancel before and after accept; the 6th-high urgency downgrade; a
# local-HTTP-listener webhook with HMAC verification; the desktop notification setting;
# and an audit-log content check.
#
# Needs: bash, go (only if binaries are missing), python3 (JSON field extraction, the
# webhook receiver, HMAC verification, and the audit_events read -- this repo has no
# sqlite3 CLI or CGo driver; see tests/harness/README.md).
#
# Does NOT cover (two machines only): reboot survival, cross-OS, a real network relay.
# See tests/phase1-manual.md "Two-machine only" section.
#
# Usage: tests/phase1-smoke.sh [BIN_DIR]     (default: ../bin relative to this script; built if missing)
# Exit code: 0 if all steps passed, 1 otherwise.
set -u

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/.." && pwd)"
bin="${1:-$repo/bin}"
exe=""; case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) exe=".exe" ;; esac
start=$SECONDS

PYTHON="$(command -v python3 || command -v python || true)"
if [ -z "$PYTHON" ]; then echo "python3 not found; required for JSON parsing, the webhook receiver and the audit check"; exit 1; fi

if [ ! -x "$bin/agentnet$exe" ] || [ ! -x "$bin/agentnetd$exe" ] || [ ! -x "$bin/relay$exe" ]; then
  echo "building into $bin"
  (cd "$repo" && go build -trimpath -o "$bin/" ./cmd/agentnet ./cmd/agentnetd ./cmd/relay) || { echo "build failed"; exit 1; }
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/dp1sk.XXXXXX")"
homeA="$work/A"; homeB="$work/B"; homeC="$work/C"; homeD="$work/D"
mkdir -p "$homeA" "$homeB" "$homeC" "$homeD"
queue="$work/relay-queue.db"

freeport() {
  "$PYTHON" -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'
}
port="$(freeport)"
relay_url="ws://127.0.0.1:$port"
hookport="$(freeport)"
hookurl="http://127.0.0.1:$hookport/hook/"

fails=0
pid_relay=""; pid_A=""; pid_B=""; pid_C=""; pid_D=""

step() { # step NAME 0|1 [detail]   (0 = ok)
  if [ "$2" -eq 0 ]; then echo "PASS  $1"; else echo "FAIL  $1  ${3:-}"; fails=$((fails + 1)); fi
}
homedir() { case "$1" in A) echo "$homeA" ;; B) echo "$homeB" ;; C) echo "$homeC" ;; D) echo "$homeD" ;; esac; }
ag() { # ag A|B|C|D args...  -> sets OUT and CODE
  local who="$1"; shift
  local h; h="$(homedir "$who")"
  OUT="$(DORYLINAE_HOME="$h" "$bin/agentnet$exe" "$@" 2>&1)"; CODE=$?
}
# jget KEY  -- reads $OUT; first top-level string/number value of "KEY" (simple, non-nested).
jget() {
  printf '%s' "$OUT" | sed -n "s/.*\"$1\": *\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" | head -n1
}
# jpy EXPR -- evaluates a Python expression against the JSON in $OUT (variable "d").
jpy() {
  printf '%s' "$OUT" | "$PYTHON" -c "
import json,sys
d=json.load(sys.stdin)
print($1)
"
}
wait_until() { # wait_until SECONDS cmd...
  local s="$1"; shift; local end=$((SECONDS + s))
  while [ "$SECONDS" -lt "$end" ]; do "$@" && return 0; sleep 0.3; done
  return 1
}
status_ok() { ag "$1" status; [ "$CODE" -eq 0 ]; }

start_relay() { "$bin/relay$exe" --listen "127.0.0.1:$port" --queue-db "$queue" --verbose >>"$work/relay.log" 2>&1 & pid_relay=$!; }
start_d() { # start_d A|B|C|D [extra env "K=V" ...]
  local who="$1"; shift
  local h; h="$(homedir "$who")"
  # DORYLINAE_KEYSTORE=file: the webhook secret's OS-keychain account is a fixed literal
  # ("webhook"), not scoped per config dir like the identity key's account is. With several
  # daemons on one machine that collides in the OS keychain -- whichever daemon sets or
  # rotates its webhook last silently overwrites every other daemon's stored secret. This
  # looks like a real product bug (see the run report); the file backend sidesteps it here
  # since it IS scoped per config dir.
  env DORYLINAE_HOME="$h" DORYLINAE_KEYSTORE=file "$@" "$bin/agentnetd$exe" run >>"$work/$who.log" 2>&1 &
  eval "pid_$who=$!"
}
stop_d() { # stop_d A|B|C|D
  local who="$1"; local var="pid_$who"; local p="${!var}"
  [ -n "$p" ] && kill "$p" 2>/dev/null && wait "$p" 2>/dev/null
  eval "pid_$who="
}
cleanup() {
  for p in "$pid_A" "$pid_B" "$pid_C" "$pid_D" "$pid_relay"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  wait 2>/dev/null
  [ -n "${hook_pid:-}" ] && kill "$hook_pid" 2>/dev/null
  if [ "$fails" -eq 0 ]; then rm -rf "$work"; else echo "logs kept in $work"; fi
}
trap cleanup EXIT

echo "work dir: $work  relay: $relay_url  hook port: $hookport"

# 1. relay + four daemons (A, B, C, D), no admin, all foreground child processes
start_relay
wait_until 10 grep -q listening "$work/relay.log" 2>/dev/null
step "relay starts and listens" $?

for who in A B C D; do start_d "$who" env DORYLINAE_RELAY_URL="$relay_url"; done
okall=0
for who in A B C D; do wait_until 20 status_ok "$who" || okall=1; done
step "status: A, B, C, D all running" $okall
if [ "$okall" -ne 0 ]; then echo "ABORT  daemons did not start"; exit 1; fi

# 2. identity + pairing v2 + fingerprints (A <-> B)
ag A identity --json; fpA="$(jget fingerprint)"; refA="$(jget public_key)"
ag B identity --json; fpB="$(jget fingerprint)"; refB="$(jget public_key)"
[ "${#fpA}" -eq 20 ] && [ "${#fpB}" -eq 20 ]; step "identity: A and B have 20-char fingerprints" $?
[ "$fpA" != "$fpB" ]; step "identity: fingerprints differ" $?

ag A pair --new --json; code="$(jget code)"
c="$(printf '%s' "$code" | tr -d '[:space:]-')"; [ "${#c}" -eq 15 ]; step "pair --new returns a 15-char v2 code" $? "$OUT"
ag B pair "$code" --json
[ "$(jget state)" = complete ] && [ "$(jget trust)" = code ]; step "pair <code> completes with trust=code" $? "$OUT"

spaced="$(printf '%s' "$fpB" | sed 's/\(....\)/\1 /g; s/ $//')"
ag A peers verify "$refB" "$spaced" --json
[ "$CODE" -eq 0 ] && [ "$(jget trust)" = fingerprint ]; step "peers verify with correct fingerprint sets trust=fingerprint" $? "$OUT"
first="${fpB:0:1}"; swap=A; [ "$first" = A ] && swap=B
ag A peers verify "$refB" "${swap}${fpB:1}"
[ "$CODE" -eq 1 ] && printf '%s' "$OUT" | grep -q fingerprint_mismatch; step "peers verify with wrong fingerprint is rejected" $? "$OUT"

# 3. team create, invite, join (incl. a third member)
ag A team create backend --json
[ "$CODE" -eq 0 ] && [ "$(jget role)" = owner ]; step "team create: A owns backend" $? "$OUT"
ag A team invite backend --json; code="$(jget code)"
[ "$CODE" -eq 0 ] && [ -n "$code" ]; step "team invite: returns a code" $? "$OUT"
ag B team join "$code" --json
[ "$CODE" -eq 0 ] && [ "$(jget state)" = complete ]; step "team join: B joins" $? "$OUT"
roster2() { ag A team show backend --json; [ "$(jpy "len(d['team']['members'])")" -ge 2 ]; }
wait_until 20 roster2; step "team show: A sees 2 members after B joins" $?

ag A team invite backend --json; code2="$(jget code)"
[ "$CODE" -eq 0 ] && [ -n "$code2" ]; step "team invite: second code for C" $? "$OUT"
ag C team join "$code2" --json
[ "$CODE" -eq 0 ] && [ "$(jget state)" = complete ]; step "team join: C (a third member) joins" $? "$OUT"
roster3() { ag A team show backend --json; [ "$(jpy "len(d['team']['members'])")" -ge 3 ]; }
wait_until 20 roster3; step "team show: A sees 3 members (A, B, C) after C joins" $?
list_ok() { ag C team list --json; [ "$(jpy "sum(1 for t in d['teams'] if t['name']=='backend')")" -ge 1 ]; }
wait_until 20 list_ok; step "team list: C sees backend" $?

# D shares a *different* team with B ("outsiders", not "backend"), so it can act as an
# outside-the-team peer for the --only-team visibility check below.
ag D team create outsiders --json
step "team create: D owns outsiders (for the outside-peer presence check)" "$CODE" "$OUT"
ag D team invite outsiders --json; codeD="$(jget code)"
ag B team join "$codeD" --json
[ "$(jget state)" = complete ]; step "B joins D's outsiders team (B is now on two teams)" $? "$OUT"
rosterD() { ag D team show outsiders --json; [ "$(jpy "len(d['team']['members'])")" -ge 2 ]; }
wait_until 20 rosterD; step "team show: D sees B on outsiders" $?

# 4. presence: status --team with all three levels, invisible/only-team/human-off, offline after stop
ag A status --team backend --json
bonline="$(jpy "str(next(m for m in d['team']['members'] if m['public_key']=='$refB')['daemon_online']).lower()")"
blast="$(jpy "next(m for m in d['team']['members'] if m['public_key']=='$refB').get('last_seen')")"
[ "$bonline" = true ] && [ -n "$blast" ] && [ "$blast" != None ]; step "status --team: B shows online with a last_seen" $? "$OUT"

ag B presence --invisible --json
[ "$(jget mode)" = invisible ]; step "presence --invisible: B reports invisible" $? "$OUT"
b_offline() { ag A status --team backend --json; [ "$(jpy "str(next(m for m in d['team']['members'] if m['public_key']=='$refB')['daemon_online']).lower()")" = false ]; }
wait_until 15 b_offline; step "presence: A sees B go offline at once after --invisible" $?

ag B presence --visible --json
[ "$(jget mode)" = visible ]; step "presence --visible: B reports visible" $? "$OUT"
b_online() { ag A status --team backend --json; [ "$(jpy "str(next(m for m in d['team']['members'] if m['public_key']=='$refB')['daemon_online']).lower()")" = true ]; }
wait_until 15 b_online; step "presence: A sees B online again after --visible" $?

ag B presence --only-team backend --json
[ "$(jget mode)" = only_team ] && [ "$(jpy "d['team']['name']")" = backend ]; step "presence --only-team: B reports only_team backend" $? "$OUT"
ag B presence --json
[ "$(jget mode)" = only_team ] && [ "$(jpy "d['team']['name']")" = backend ]; step "presence (no flags): B still reports only_team backend" $? "$OUT"
d_sees_offline() {
  ag D status --team outsiders --json
  [ "$(jpy "str(next((m for m in d['team']['members'] if m['public_key']=='$refB'), {'daemon_online':True})['daemon_online']).lower()")" = false ]
}
wait_until 15 d_sees_offline; step "presence --only-team: a peer on a different team sees B as never seen / offline" $?
ag B presence --visible --json
[ "$(jget mode)" = visible ]; step "presence: B back to visible for the rest of the run" $? "$OUT"
ag B presence --human off --json
[ "$(jget human_share)" = false ]; step "presence --human off: human sharing turned off" $? "$OUT"
ag B presence --human on --json
[ "$(jget human_share)" = true ]; step "presence --human on: human sharing turned back on" $? "$OUT"

# 5. request with brief/artifacts/urgency, queued while B is stopped, then delivered
stop_d B
ag B status; [ "$CODE" -eq 3 ]; step "status: B not running before offline request (exit 3)" $? "code=$CODE"
ag A request "$refB" task --title "Offline artifact request" \
  --brief "$(printf 'What: SMOKEMARKBRIEF check the artifact\nWhy: smoke test\nDone when: reviewed')" \
  --urgency blocking --urgency-reason "smoke test needs a blocking sample" \
  --artifact "url=https://example.test/x branch=main commit=abcdef1234567890 path=foo/bar" \
  --idempotency-key p1smoke-offline-1 --json
offReqId="$(jget id)"
[ "$CODE" -eq 0 ] && [ "$(jget status)" = queued ] && [ "$(jget urgency)" = blocking ]
step "request: accepted as queued while B is offline, urgency blocking, has an artifact" $? "$OUT"
start_d B env DORYLINAE_RELAY_URL="$relay_url"
wait_until 20 status_ok B; step "status: B running again" $?
delivered() { ag B inbox --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id']=='$offReqId')")" -ge 1 ]; }
wait_until 30 delivered; step "request: delivered to B after it restarts" $?
ag A request show "$offReqId" --json
[ "$(jpy "len(d['request'].get('artifacts',[]))")" -ge 1 ]; step "request show: artifact is present on A's mirror" $? "$OUT"

# 6. inbox order by urgency: high, normal, low -> high first, then normal, then low
ag A request "$refB" question --title "Low prio" --brief "What: low" --urgency low --idempotency-key p1smoke-order-low --json
lowId="$(jget id)"
sleep 0.2
ag A request "$refB" review --title "High prio" --brief "What: high" --urgency high --urgency-reason "smoke order test" --idempotency-key p1smoke-order-high-1 --json
highId="$(jget id)"
sleep 0.2
ag A request "$refB" task --title "Normal prio" --brief "What: normal" --idempotency-key p1smoke-order-normal --json
normId="$(jget id)"
allin() { ag B inbox --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id'] in ('$lowId','$highId','$normId'))")" -eq 3 ]; }
wait_until 20 allin; step "inbox: all three ordering requests arrived" $?
ag B inbox --json
order="$(jpy "','.join(r['id'] for r in d['requests'] if r['id'] in ('$lowId','$highId','$normId'))")"
[ "$order" = "$highId,$normId,$lowId" ]; step "inbox: ordered high, normal, low" $? "$order"

# 7. accept / decline / defer / complete with a D14 result
ag B accept "$highId" --json; step "accept: B accepts the high-priority request" "$CODE" "$OUT"
accepted() { ag A request show "$highId" --json; [ "$(jget state)" = accepted ]; }
wait_until 15 accepted; step "accept: A's mirror shows accepted" $?

ag B decline "$normId" --reason "SMOKEMARKDECLINE not needed for the smoke test" --json
step "decline: B declines the normal request" "$CODE" "$OUT"
declined() { ag A request show "$normId" --json; [ "$(jget state)" = declined ]; }
wait_until 15 declined; step "decline: A's mirror shows declined" $?

ag B defer "$lowId" --until 2h --json; step "defer: B defers the low-priority request" "$CODE" "$OUT"
deferred() { ag A request show "$lowId" --json; [ "$(jget state)" = deferred ]; }
wait_until 15 deferred; step "defer: A's mirror shows deferred" $?

outfile="$work/complete-output.txt"
printf 'SMOKEMARKOUTPUT line 1\nline 2' > "$outfile"
ag B complete "$highId" --note "SMOKEMARKNOTE all good" \
  --status pass --summary "SMOKEMARKSUMMARY all green" --exit-code 0 \
  --output-from-file "$outfile" --artifact "url=https://example.test/y branch=main commit=1234567890abcdef path=out/log" --json
step "complete: B completes with a D14 result" "$CODE" "$OUT"
completed() { ag A request show "$highId" --json; [ "$(jget state)" = completed ]; }
wait_until 15 completed; step "complete: A's mirror shows completed" $?
ag A request show "$highId" --json
[ "$(jpy "d['request']['result']['status']")" = pass ] \
  && printf '%s' "$OUT" | grep -q SMOKEMARKSUMMARY \
  && printf '%s' "$OUT" | grep -q SMOKEMARKOUTPUT \
  && [ "$(jpy "len(d['request']['result'].get('artifacts',[]))")" -ge 1 ]
step "complete: A's mirror has the D14 result (status, summary, output, artifact)" $? "$OUT"

# 8. cancel before accept -> cancelled; cancel after accept -> refused
ag A request "$refB" task --title "Cancel before accept" --brief "What: c1" --idempotency-key p1smoke-cancel-1 --json
rc1="$(jget id)"
arrived1() { ag B inbox --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id']=='$rc1')")" -ge 1 ]; }
wait_until 30 arrived1; step "cancel test: request 1 reached B's inbox" $?
ag A request cancel "$rc1" --reason "SMOKEMARKCANCEL not needed" --json
step "cancel before accept: cancel accepted locally" "$CODE" "$OUT"
can1() { ag B inbox --all --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id']=='$rc1' and r['state']=='cancelled')")" -ge 1 ]; }
wait_until 30 can1; step "cancel before accept: B's inbox --all shows cancelled" $?
can1a() { ag A request show "$rc1" --json; [ "$(jget state)" = cancelled ]; }
wait_until 30 can1a; step "cancel before accept: A's mirror shows cancelled" $?

# Per Docs/protocol/request.md "After accept the sender cannot cancel": once A's own mirror
# has observed the accept, request_cancel refuses locally with bad_state. The "refused"
# outcome only happens when A's cancel is created while A's own mirror still reads
# "pending", and *then* reaches B after B has already accepted. To make this deterministic
# instead of racing a freshly restarted daemon against an already-open relay connection:
# stop A, have B accept while A is offline (the confirmation queues at the relay), then
# bring A back up with NO relay connection at all so it is physically unable to receive
# that confirmation -- A's mirror is certainly still "pending" -- fire the cancel there
# (it queues locally in A's own outbox), then restart A once more with the relay
# reconnected so both the queued cancel and the queued accept confirmation actually flow.
ag A request "$refB" task --title "Cancel after accept" --brief "What: c2" --idempotency-key p1smoke-cancel-2 --json
rc2="$(jget id)"
arrived2() { ag B inbox --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id']=='$rc2')")" -ge 1 ]; }
wait_until 30 arrived2; step "cancel test: request 2 reached B's inbox" $?

stop_d A
ag A status; [ "$CODE" -eq 3 ]; step "status: A not running before the accept-then-cancel test (exit 3)" $? "code=$CODE"
ag B accept "$rc2" --json; step "cancel after accept: B accepts request 2 while A is offline" "$CODE" "$OUT"
start_d A   # deliberately no DORYLINAE_RELAY_URL: A cannot receive B's queued "accepted" mail
wait_until 20 status_ok A; step "status: A running again, deliberately relay-less" $?
ag A request cancel "$rc2" --json
[ "$CODE" -eq 0 ] && [ "$(jpy "d['request']['state']")" = pending ]
step "cancel after accept: cancel accepted locally while A's mirror still reads pending" $? "$OUT"
stop_d A
start_d A env DORYLINAE_RELAY_URL="$relay_url"
wait_until 20 status_ok A; step "status: A running again with the relay reconnected" $?
can2refused() { ag A request show "$rc2" --json; [ "$(jget state)" = accepted ] && [ "$(jget cancel)" = refused ]; }
wait_until 30 can2refused; step "cancel after accept: A's mirror shows cancel=refused, state stays accepted" $?

# 9. the urgency limit: the 6th high in the run arrives as normal with a note.
# highId above was high #1; send five more to reach the 6th.
downOut=""
for i in 2 3 4 5 6; do
  ag A request "$refB" task --title "High #$i" --brief "What: high $i" \
    --urgency high --urgency-reason "smoke urgency-limit test" --idempotency-key "p1smoke-highlimit-$i" --json
  [ "$i" -eq 6 ] && downOut="$OUT"
done
OUT="$downOut"
[ "$(jget urgency)" = normal ] && [ "$(jget urgency_declared)" = high ] && [ -n "$(jget urgency_note)" ]
step "urgency limit: the 6th high request is downgraded to normal with urgency_declared=high and a note" $? "$OUT"

# 10. webhook to a local HTTP listener, HMAC signature verified
cat > "$work/hookserver.py" <<'PYEOF'
import http.server, sys, json, os
port = int(sys.argv[1]); outdir = sys.argv[2]
n = [0]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n[0] += 1
        length = int(self.headers.get('Content-Length', '0'))
        body = self.rfile.read(length)
        rec = {"headers": dict(self.headers.items()), "body": body.decode('utf-8')}
        with open(os.path.join(outdir, f"post-{n[0]}.json"), "w") as f:
            json.dump(rec, f)
        self.send_response(200); self.end_headers()
srv = http.server.HTTPServer(("127.0.0.1", port), H)
srv.serve_forever()
PYEOF
mkdir -p "$work/hookposts"
"$PYTHON" "$work/hookserver.py" "$hookport" "$work/hookposts" >>"$work/hook.log" 2>&1 &
hook_pid=$!
sleep 0.3

ag B notify --webhook "$hookurl" --webhook-title on --json
secretB="$(jget secret)"
[ "$CODE" -eq 0 ] && case "$secretB" in whsec_*) true ;; *) false ;; esac
step "notify --webhook: B gets a whsec_ secret" $? "$OUT"
ag A notify --webhook "$hookurl" --webhook-title on --json
secretA="$(jget secret)"
[ "$CODE" -eq 0 ] && case "$secretA" in whsec_*) true ;; *) false ;; esac
step "notify --webhook: A gets a whsec_ secret" $? "$OUT"

ag A request "$refB" task --title "Webhook test" --brief "What: webhook" --idempotency-key p1smoke-webhook-1 --json
wreqId="$(jget id)"
wreq_arrived() { ag B inbox --json; [ "$(jpy "sum(1 for r in d['requests'] if r['id']=='$wreqId')")" -ge 1 ]; }
wait_until 15 wreq_arrived
ag B accept "$wreqId" --json

two_posts() { [ "$(ls "$work/hookposts" 2>/dev/null | wc -l)" -ge 2 ]; }
wait_until 20 two_posts
npost="$(ls "$work/hookposts" 2>/dev/null | wc -l)"
[ "$npost" -ge 2 ]; step "webhook: at least 2 signed deliveries arrived (request.received + request.accepted)" $? "$npost received"
kill "$hook_pid" 2>/dev/null; hook_pid=""

verify_out="$("$PYTHON" - "$work/hookposts" "$secretB" "$secretA" <<'PYEOF'
import sys, os, json, hmac, hashlib, base64

def b64url_decode(s):
    s = s.replace('-', '+').replace('_', '/')
    pad = (-len(s)) % 4
    return base64.b64decode(s + '=' * pad)

posts_dir, secretB, secretA = sys.argv[1], sys.argv[2], sys.argv[3]
keyB = b64url_decode(secretB[len('whsec_'):])
keyA = b64url_decode(secretA[len('whsec_'):])

def b64url(b):
    return base64.urlsafe_b64encode(b).rstrip(b'=').decode()

total = 0
verified = 0
for name in sorted(os.listdir(posts_dir)):
    total += 1
    with open(os.path.join(posts_dir, name)) as f:
        rec = json.load(f)
    body = rec["body"]
    headers = {k.lower(): v for k, v in rec["headers"].items()}
    sig = headers.get("dorylinae-signature", "")
    ts = headers.get("dorylinae-webhook-timestamp", "")
    wid = headers.get("dorylinae-webhook-id", "")
    payload = json.loads(body)
    key = keyB if payload.get("event") == "request.received" else keyA
    signed = f"v1:{ts}:{wid}:{body}".encode()
    expect = "v1=" + b64url(hmac.new(key, signed, hashlib.sha256).digest())
    if expect == sig and wid == payload.get("id"):
        verified += 1
print(f"{verified}/{total}")
PYEOF
)"
verified="${verify_out%%/*}"; total="${verify_out##*/}"
[ "$total" -ge 2 ] && [ "$verified" = "$total" ]
step "webhook: HMAC signature verifies for every delivery received" $? "$verify_out verified"

ag A notify --webhook off --json; whA_off="$OUT"
ag B notify --webhook off --json; whB_off="$OUT"
! printf '%s' "$whA_off" | grep -q '"webhook":{' && ! printf '%s' "$whB_off" | grep -q '"webhook":{'
step "notify --webhook off: removed on both sides" $? "$whA_off $whB_off"

# 11. desktop notification setting: check the setting and that the call path runs
ag B notify --desktop off --json
[ "$(jget desktop)" = false ]; step "notify --desktop off" $? "$OUT"
ag B notify --test --json
[ "$CODE" -eq 0 ] && [ "$(jget desktop)" = disabled ]; step "notify --test with desktop off reports disabled" $? "$OUT"
ag B notify --desktop on --json
[ "$(jget desktop)" = true ]; step "notify --desktop on" $? "$OUT"
ag B notify --test --json
d="$(jget desktop)"
[ "$CODE" -eq 0 ] && { [ "$d" = shown ] || [ "$d" = failed ]; }
step "notify --test with desktop on runs the call path (shown or failed, not disabled)" $? "$OUT"

# 12. audit log has the events and no content
audit_out="$("$PYTHON" - "$homeA/dorylinae.db" "$homeB/dorylinae.db" <<'PYEOF'
import sys, sqlite3, json

needed = ["request.submit", "request.in", "request.accept", "request.decline",
          "request.defer", "request.complete", "team.create", "team.join", "notify.config"]
markers = ["SMOKEMARKBRIEF", "SMOKEMARKDECLINE", "SMOKEMARKNOTE", "SMOKEMARKSUMMARY",
           "SMOKEMARKOUTPUT", "SMOKEMARKCANCEL"]

actions = set()
leak = ""
for path in sys.argv[1:]:
    try:
        con = sqlite3.connect(path)
        for action, detail in con.execute("select action, detail from audit_events order by id"):
            actions.add(action)
            for m in markers:
                if detail and m in detail:
                    leak = f"{action}: {m}"
    except Exception as e:
        print(f"ERROR {path}: {e}")
        sys.exit(1)

missing = [n for n in needed if n not in actions]
print("MISSING:" + ",".join(missing))
print("LEAK:" + leak)
PYEOF
)"
missing_line="$(printf '%s\n' "$audit_out" | grep '^MISSING:' | cut -d: -f2-)"
leak_line="$(printf '%s\n' "$audit_out" | grep '^LEAK:' | cut -d: -f2-)"
[ -z "$missing_line" ]; step "audit: all expected P1 actions are present across A and B" $? "missing: $missing_line"
[ -z "$leak_line" ]; step "audit: no request content (title/brief/reason/note/summary/output) leaks into audit_events" $? "$leak_line"

elapsed=$((SECONDS - start))
echo "elapsed: ${elapsed}s"
if [ "$fails" -eq 0 ]; then echo "ALL PASS"; exit 0; fi
echo "$fails FAILED"; exit 1
