#!/bin/sh
# install.sh test (ticket 4.4a): signs fake or real release files with a
# throwaway TEST key, then runs scripts/install.sh as a non-root user in a
# clean Ubuntu container against a local file server. See tests/install/cases.sh
# for the cases (good sums pass; flipped archive byte, flipped SHA256SUMS
# byte and a wrong-key signature fail; old versions refused; no sudo used).
#
# Usage (from the repo root, needs Go and Docker; runs on the CI ubuntu runner):
#   sh tests/install/run.sh                 # fake 1.2.3 release built here
#   sh tests/install/run.sh DIR             # a real release: DIR holds the six
#                                           # archives and SHA256SUMS (release.yml)
#
# The test key is generated fresh in a temp dir and deleted afterwards. It
# is never the release key, and its signatures are never uploaded anywhere.

set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
image=${AGENTNET_TEST_IMAGE:-ubuntu:24.04}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sign() { (cd "$root" && go run ./tools/releasesign "$@"); }

targets='darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64'

# fake_release DIR VERSION: six archives whose unix ones hold stub binaries.
fake_release() {
	mkdir -p "$1/stage"
	for b in agentnet agentnetd relay; do
		printf '#!/bin/sh\necho "%s %s (fake test build)"\n' "$b" "$2" >"$1/stage/$b"
		chmod 755 "$1/stage/$b"
	done
	for t in $targets; do
		case $t in
		windows_*) printf 'not a real zip %s\n' "$t" >"$1/agentnet_$2_$t.zip" ;;
		*) tar -czf "$1/agentnet_$2_$t.tar.gz" -C "$1/stage" agentnet agentnetd relay ;;
		esac
	done
	rm -rf "$1/stage"
	(cd "$1" && sha256sum agentnet_* | LC_ALL=C sort -k2 >SHA256SUMS)
}

srv=$work/srv
mkdir -p "$srv/good" "$srv/old" "$srv/badarchive" "$srv/badsums" "$srv/wrongkey"

if [ $# -ge 1 ]; then
	cp "$1"/agentnet_* "$1"/SHA256SUMS "$srv/good/"
else
	fake_release "$srv/good" 1.2.3
fi
fake_release "$srv/old" 0.0.9

sign keygen -out "$work/test.key" >/dev/null
sign keygen -out "$work/other.key" >/dev/null
sign sign -key "$work/test.key" "$srv/good/SHA256SUMS" >/dev/null
sign sign -key "$work/test.key" "$srv/old/SHA256SUMS" >/dev/null
sign embed -key "$work/test.key" -in "$root/scripts/install.sh" -out "$work/install.sh" >/dev/null
sign verify -install-sh "$work/install.sh" "$srv/good/SHA256SUMS"
cp "$root/scripts/install.sh" "$work/install-placeholder.sh"
rm -f "$work/test.key"

# The linux archive this container will pick (amd64 or arm64, like the host).
case $(uname -m) in
aarch64 | arm64) arch=arm64 ;;
*) arch=amd64 ;;
esac
version=$(sed -n '1s/^.\{64\}  agentnet_\([0-9.]*\)_.*/\1/p' "$srv/good/SHA256SUMS")
echo "$version" >"$work/version"

# badarchive: a valid signed SHA256SUMS, one flipped byte in the archive.
cp "$srv/good"/* "$srv/badarchive/"
f=$srv/badarchive/agentnet_${version}_linux_${arch}.tar.gz
size=$(wc -c <"$f")
b=$(dd if="$f" bs=1 skip=$((size / 2)) count=1 2>/dev/null | od -An -tu1 | tr -d ' ')
if [ "$b" -lt 128 ]; then nb=$((b + 1)); else nb=$((b - 1)); fi # never a NUL byte
printf '%b' "$(printf '\\0%03o' "$nb")" | dd of="$f" bs=1 seek=$((size / 2)) conv=notrunc 2>/dev/null

# badsums: one flipped hex digit in SHA256SUMS, the original signatures.
cp "$srv/good"/* "$srv/badsums/"
awk 'NR == 1 { c = substr($0, 1, 1); $0 = (c == "0" ? "1" : "0") substr($0, 2) } { print }' "$srv/good/SHA256SUMS" >"$srv/badsums/SHA256SUMS"
cmp -s "$srv/good/SHA256SUMS" "$srv/badsums/SHA256SUMS" && { echo "badsums did not change" >&2; exit 1; }

# wrongkey: a well-formed signature by a key install.sh does not know.
cp "$srv/good"/* "$srv/wrongkey/"
rm -f "$srv/wrongkey/SHA256SUMS.sig" "$srv/wrongkey/SHA256SUMS.minisig"
sign sign -key "$work/other.key" "$srv/wrongkey/SHA256SUMS" >/dev/null
rm -f "$work/other.key"

cp "$root/tests/install/cases.sh" "$work/cases.sh"
docker run --rm -v "$work:/work:ro" "$image" sh -eu -c '
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq >/dev/null
	apt-get install -y -qq --no-install-recommends ca-certificates curl openssl minisign python3 >/dev/null
	if command -v sudo >/dev/null 2>&1; then echo "image has sudo; cannot prove it is unused" >&2; exit 1; fi
	# A sudo that records any call: the installer must never invoke it.
	printf "#!/bin/sh\necho called >/tmp/sudo-called\nexit 1\n" >/usr/local/bin/sudo
	chmod 755 /usr/local/bin/sudo
	cp -r /work /srv/t && chmod -R a+rX /srv/t
	useradd -m -s /bin/sh tester
	(cd /srv/t/srv && python3 -m http.server 8765 --bind 127.0.0.1 >/tmp/http.log 2>&1 &)
	for i in 1 2 3 4 5 6 7 8 9 10; do curl -fs http://127.0.0.1:8765/good/SHA256SUMS >/dev/null && break; sleep 0.5; done
	runuser -u tester -- env -i HOME=/home/tester PATH=/usr/local/bin:/usr/bin:/bin sh /srv/t/cases.sh
	if [ -e /tmp/sudo-called ]; then echo "FAIL: sudo was invoked" >&2; exit 1; fi
	echo "PASS: sudo never invoked"
'
