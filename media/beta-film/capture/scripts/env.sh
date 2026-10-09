# source me
export C=/tmp/claude-0/-home-user-Dorylinae-Agentnet/23e9a7a7-9cfb-51a7-93c9-973648c5f9dc/scratchpad/film/capture
export BIN=$C/bin RUN=$C/run WORK=$C/run/work
export PORT=8787
export RELAY_URL=ws://127.0.0.1:$PORT
export DORYLINAE_KEYSTORE=file
export PATH=$BIN:$PATH
export NO_COLOR=
# Unix socket paths are limited to ~107 bytes, so each home is reached through a short
# symlink /tmp/dn-<who> -> $RUN/<who> (the data itself lives under $RUN).
home_of() { echo "/tmp/dn-$1"; }
# an WHO args...  -> run agentnet as WHO (output to stdout/stderr)
an() { local who=$1; shift; (cd "$WORK" && DORYLINAE_HOME="/tmp/dn-$who" "$BIN/agentnet" "$@"); }
# capture state
CAPF=""
capfile() { CAPF="$C/$1"; : > "$CAPF"; printf '# %s\n# captured %s UTC from the real binaries (local relay, two daemons on one machine)\n\n' "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$CAPF"; }
# run WHO args...  -> append "who$ agentnet args" + output + exit code to CAPF, echo to stdout too
run() {
  local who=$1; shift
  local disp; disp="$(python3 -c 'import shlex,sys; print(shlex.join(sys.argv[1:]))' "$@")"
  local out code
  out="$(an "$who" "$@" 2>&1)"; code=$?
  { printf '%s$ agentnet %s\n' "$who" "$disp"; printf '%s\n' "$out"; printf '[exit %d]\n\n' "$code"; } >> "$CAPF"
  printf '%s$ agentnet %s\n%s\n[exit %d]\n\n' "$who" "$disp" "$out" "$code"
  LAST="$out"; LASTCODE=$code
}
# note TEXT -> append a comment line to CAPF
note() { printf '# %s\n' "$*" >> "$CAPF"; }
jq_() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
start_relay() { "$BIN/relay" --listen 127.0.0.1:$PORT --db "$RUN/relay.db" --verbose >> "$RUN/relay.log" 2>&1 & echo $! > "$RUN/relay.pid"; }
start_d() { # start_d WHO NAME HARNESS
  local who=$1
  mkdir -p "$RUN/$who"; chmod 700 "$RUN/$who"; [ -L "/tmp/dn-$who" ] || ln -s "$RUN/$who" "/tmp/dn-$who"
  DORYLINAE_HOME="/tmp/dn-$who" DORYLINAE_RELAY_URL="$RELAY_URL" DORYLINAE_AGENT_NAME="$2" DORYLINAE_HARNESS="$3" \
    nohup python3 "$C/scripts/daemon_pty.py" "$BIN/agentnetd" "$RUN/$who.tty.txt" "$RUN/$who.stdout.txt" 2.5 > "$RUN/$who.runner.txt" 2>&1 &
  echo $! > "$RUN/$who.runner.pid"
}
