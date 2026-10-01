#!/bin/bash
# Checks the authorized_keys filter in deploy/early/setup.sh (review 88 F1).
# Run: bash tests/early-setup-keyfilter.sh
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
SETUP_SH_SOURCE_ONLY=1
# shellcheck disable=SC1091
source "$root/deploy/early/setup.sh"
tmp=$(mktemp -d)
trap 'rm -f "$tmp/ak"; rmdir "$tmp"' EXIT
fail=0
check() { # want(ok|no) line
	printf '%s\n' "$2" >"$tmp/ak"
	if unrestricted_key_present "$tmp/ak"; then got=ok; else got=no; fi
	if [ "$got" != "$1" ]; then echo "FAIL want=$1 got=$got: $2"; fail=1; fi
}
K=AAAAC3NzaC1lZDI1NTE5AAAAIexample
check ok "ssh-ed25519 $K me@host"
check ok "ssh-rsa AAAAB3NzaC1yc2E me@host"
check ok "ecdsa-sha2-nistp256 AAAAE2VjZHNh me@host"
check ok "  ssh-ed25519 $K"
check no "command=\"/bin/false\" ssh-ed25519 $K"
check no "Command=\"/bin/false\" ssh-ed25519 $K"
check no "environment=\"A=1\",COMMAND=\"/bin/false\" ssh-ed25519 $K"
check no "from=\"10.0.0.1\" ssh-ed25519 $K"
check no "FROM=\"10.0.0.1\" ssh-ed25519 $K"
check no "expiry-time=\"20300101\" ssh-ed25519 $K"
check no "restrict ssh-ed25519 $K"
check no "no-pty ssh-ed25519 $K"
check no "ssh-dss AAAAB3NzaC1kc3M me@host"
check no "sk-ssh-ed25519@openssh.com $K"
check no "# ssh-ed25519 $K"
check no ""
printf '%s\n%s\n' "command=\"/bin/false\" ssh-ed25519 $K" "ssh-ed25519 $K" >"$tmp/ak"
unrestricted_key_present "$tmp/ak" || { echo "FAIL mixed file"; fail=1; }
unrestricted_key_present "$tmp/missing" && { echo "FAIL missing file"; fail=1; }
[ "$fail" = 0 ] && echo "keyfilter: all ok"
exit "$fail"
