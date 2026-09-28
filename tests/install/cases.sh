#!/bin/sh
# install.sh cases, run by tests/install/run.sh inside a clean Ubuntu
# container as the non-root user "tester", with the release files served by
# a local HTTP server on 127.0.0.1:8765. Not meant to be run by hand.

set -u

T=/srv/t
U=http://127.0.0.1:8765
V=$(cat "$T/version")
fails=0
n=0

# A LibreSSL-like openssl (what stock macOS has): no Ed25519 in pkeyutl.
mkdir -p /tmp/libre
cat >/tmp/libre/openssl <<'EOF'
#!/bin/sh
[ "$1" = version ] && { echo "LibreSSL 3.3.6"; exit 0; }
exit 1
EOF
chmod 755 /tmp/libre/openssl
# A PATH with neither OpenSSL 3 nor minisign.
mkdir -p /tmp/none
for f in /usr/bin/* /bin/*; do
	b=${f##*/}
	case $b in openssl | minisign) continue ;; esac
	[ -e "/tmp/none/$b" ] || ln -s "$f" "/tmp/none/$b"
done
ln -sf /tmp/libre/openssl /tmp/none/openssl

PATH_OSSL=$PATH
PATH_MINI=/tmp/libre:$PATH
PATH_NONE=/tmp/none

# check NAME ok|fail PATTERN DIR PATH URL [install.sh args...]
check() {
	name=$1 want=$2 pattern=$3 dir=$4 path=$5 url=$6
	shift 6
	n=$((n + 1))
	script=$T/install.sh
	[ "$name" = placeholder ] && script=$T/install-placeholder.sh
	out=$(env PATH="$path" AGENTNET_ALLOW_INSECURE_URL=1 AGENTNET_DOWNLOAD_URL="$url" \
		AGENTNET_INSTALL_DIR="$dir" sh "$script" "$@" 2>&1)
	rc=$?
	ok=1
	if [ "$want" = ok ]; then
		[ $rc -eq 0 ] || ok=0
		[ -x "$dir/agentnet" ] && [ -x "$dir/agentnetd" ] || ok=0
		"$dir/agentnet" --version >/dev/null 2>&1 || ok=0
	else
		[ $rc -ne 0 ] || ok=0
		[ ! -e "$dir/agentnet" ] && [ ! -e "$dir/agentnetd" ] || ok=0
	fi
	case $out in *"$pattern"*) ;; *) ok=0 ;; esac
	if [ $ok = 1 ]; then
		echo "ok   $n $name"
	else
		echo "FAIL $n $name (exit $rc, want $want, pattern '$pattern'):"
		printf '%s\n' "$out" | sed 's/^/     | /'
		fails=$((fails + 1))
	fi
}

d=$HOME/case
check placeholder fail "NO RELEASE SIGNING KEY" "$d/1" "$PATH_OSSL" "$U/good"
check good-openssl ok "signature checked with openssl" "$d/2" "$PATH_OSSL" "$U/good"
check good-minisign ok "signature checked with minisign" "$d/3" "$PATH_MINI" "$U/good"
check no-verifier fail "brew install" "$d/4" "$PATH_NONE" "$U/good"
check archive-byte-flipped-openssl fail "SHA-256 mismatch" "$d/5" "$PATH_OSSL" "$U/badarchive"
check archive-byte-flipped-minisign fail "SHA-256 mismatch" "$d/6" "$PATH_MINI" "$U/badarchive"
check sums-byte-flipped-openssl fail "signature check FAILED" "$d/7" "$PATH_OSSL" "$U/badsums"
check sums-byte-flipped-minisign fail "signature check FAILED" "$d/8" "$PATH_MINI" "$U/badsums"
check wrong-key-openssl fail "signature check FAILED" "$d/9" "$PATH_OSSL" "$U/wrongkey"
check wrong-key-minisign fail "signature check FAILED" "$d/10" "$PATH_MINI" "$U/wrongkey"
check signed-but-too-old fail "rollback" "$d/11" "$PATH_OSSL" "$U/old"
check asked-other-version fail "asked for 99.0.0" "$d/12" "$PATH_OSSL" "$U/good" --version 99.0.0
check asked-too-old fail "older than the oldest" "$d/13" "$PATH_OSSL" "$U/old" --version 0.0.0
check asked-this-version ok "Installed agentnet $V" "$d/14" "$PATH_OSSL" "$U/good" --version "$V"
check upgrade-in-place ok "Installed agentnet $V" "$d/2" "$PATH_OSSL" "$U/good"
out=$(env PATH="$PATH_OSSL" AGENTNET_DOWNLOAD_URL="$U/good" AGENTNET_INSTALL_DIR="$d/15" sh "$T/install.sh" 2>&1)
rc=$?
n=$((n + 1))
case $rc:$out in
0:*) echo "FAIL $n http-url-without-opt-in: exit 0"; fails=$((fails + 1)) ;;
*"must be https"*) echo "ok   $n http-url-without-opt-in" ;;
*) echo "FAIL $n http-url-without-opt-in: $out"; fails=$((fails + 1)) ;;
esac
check symlink-in-archive fail "no regular file agentnet" "$d/16" "$PATH_OSSL" "$U/symlink"
# A `curl | sh` cut short runs nothing (review 53 L1): every prefix of the
# script, piped to sh, installs nothing and leaves no install dir.
n=$((n + 1))
total=$(wc -l <"$T/install.sh")
bad=
i=1
while [ "$i" -lt "$total" ]; do
	head -n "$i" "$T/install.sh" | env PATH="$PATH_OSSL" AGENTNET_ALLOW_INSECURE_URL=1 \
		AGENTNET_DOWNLOAD_URL="$U/good" AGENTNET_INSTALL_DIR="$d/17" sh >/dev/null 2>&1
	[ -e "$d/17" ] && { bad="$bad $i"; rm -rf "$d/17"; }
	i=$((i + 1))
done
if [ -z "$bad" ]; then
	echo "ok   $n truncated-script-runs-nothing ($total prefixes)"
else
	echo "FAIL $n truncated-script-runs-nothing: prefixes of$bad lines installed something"
	fails=$((fails + 1))
fi
# The default install dir is ~/.local/bin (no --dir, no AGENTNET_INSTALL_DIR).
n=$((n + 1))
if env PATH="$PATH_OSSL" AGENTNET_ALLOW_INSECURE_URL=1 AGENTNET_DOWNLOAD_URL="$U/good" sh "$T/install.sh" >/dev/null 2>&1 &&
	[ -x "$HOME/.local/bin/agentnet" ]; then
	echo "ok   $n default-dir-is-home-local-bin"
else
	echo "FAIL $n default-dir-is-home-local-bin"
	fails=$((fails + 1))
fi

echo "$n cases, $fails failed"
[ $fails -eq 0 ]
