#!/bin/sh
# AgentNet installer for macOS and Linux (ticket 4.4a; Docs/cli/install.md).
#
#   curl -fsSL https://dorylinae.net/install.sh | sh
#   curl -fsSL https://dorylinae.net/install.sh | sh -s -- --version 1.2.3
#
# It downloads one release for this OS and CPU into ~/.local/bin (no sudo,
# ever), and installs it only if:
#   1. SHA256SUMS carries a valid Ed25519 signature by the release key
#      embedded below (checked with OpenSSL >= 3, else minisign; with neither
#      it stops and prints the Homebrew and manual routes; it never installs
#      unsigned);
#   2. SHA256SUMS names exactly one version, that version is not older than
#      AGENTNET_MIN_VERSION below (no rollback to a known-bad release), and it
#      is the version asked for, if one was;
#   3. the archive's SHA-256 matches its line in SHA256SUMS.
#
# What this protects against, honestly: whoever controls the domain serving
# this script controls the key embedded in it. The signature protects against
# a swap of the release files on GitHub before or after the owner signs them
# (a leaked token, a later compromised workflow; the owner signs only the
# SHA256SUMS the tag's CI run logged), because the signing key is kept
# offline and never in GitHub. It cannot catch a build that was already bad
# when the owner signed its SHA256SUMS.
#
# Environment (all optional):
#   AGENTNET_VERSION       install this X.Y.Z instead of the latest release
#   AGENTNET_INSTALL_DIR   install here instead of ~/.local/bin
#   AGENTNET_DOWNLOAD_URL  directory URL holding SHA256SUMS, its signatures and
#                          the archives (a mirror; the signature is still checked)
#   AGENTNET_ALLOW_INSECURE_URL=1  allow a non-https AGENTNET_DOWNLOAD_URL
#                          (tests against a local file server only)

set -eu

# =============================================================================
# RELEASE SIGNING KEY
#
# The public half of the release key, made offline with `go run
# ./tools/releasesign keygen` and written here by `releasesign embed`
# (Docs/ops/release-signing.md). If these lines hold REPLACE_WITH_RELEASE_*
# placeholders instead, this script refuses to install anything.
#
# Both lines hold the SAME Ed25519 public key: PEM for OpenSSL, base64 for
# minisign. `go run ./tools/releasesign verify -install-sh scripts/install.sh
# SHA256SUMS` checks that they match and that a signed SHA256SUMS verifies.
AGENTNET_PUBKEY_PEM='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAyhZFHV5rVAwlTA0TKpCjlN/5oZjW9GgPPzZSoUFGu1M=
-----END PUBLIC KEY-----'
AGENTNET_MINISIGN_PUBKEY='RWRcNKizncDFysoWRR1ea1QMJUwNEyqQo5Tf+aGY1vRoDz82UqFBRrtT'

# The oldest release this script installs. Raise it when a release must never
# be installed again (a rollback to it would reopen a fixed hole).
AGENTNET_MIN_VERSION='0.1.0'
# =============================================================================

REPO='Magazem/Dorylinae-Agentnet'
TAP_CMD='brew install magazem/tap/agentnet'

# Everything below runs from main, called on the last line: `curl | sh` feeds
# the script to sh as it downloads, so a transfer cut short must define
# functions only and run nothing (review 53 L1).
main() {

say() { printf '%s\n' "$*"; }
die() {
	printf 'agentnet install: %s\n' "$*" >&2
	printf 'agentnet install: nothing was installed.\n' >&2
	exit 1
}
# fail: like die, for a failure after something was already installed.
fail() {
	printf 'agentnet install: %s\n' "$*" >&2
	exit 1
}

# --- arguments ---------------------------------------------------------------

want_version=${AGENTNET_VERSION:-}
install_dir=${AGENTNET_INSTALL_DIR:-}
while [ $# -gt 0 ]; do
	case $1 in
	--version)
		[ $# -ge 2 ] || die "--version needs a value (X.Y.Z)"
		want_version=$2
		shift 2
		;;
	--version=*)
		want_version=${1#--version=}
		shift
		;;
	--dir)
		[ $# -ge 2 ] || die "--dir needs a value"
		install_dir=$2
		shift 2
		;;
	--dir=*)
		install_dir=${1#--dir=}
		shift
		;;
	-h | --help)
		say "Usage: install.sh [--version X.Y.Z] [--dir DIR]"
		say "Installs agentnet and agentnetd into DIR (default ~/.local/bin) after checking"
		say "the release signature. Never uses sudo."
		exit 0
		;;
	*) die "unknown argument: $1 (see --help)" ;;
	esac
done

# --- the embedded key must be real --------------------------------------------

case $AGENTNET_PUBKEY_PEM$AGENTNET_MINISIGN_PUBKEY in
*REPLACE_WITH_RELEASE*)
	die "THIS install.sh HAS NO RELEASE SIGNING KEY YET (placeholder). It cannot verify a release, so it refuses to install. Maintainers: see Docs/ops/release-signing.md."
	;;
esac

# --- versions ----------------------------------------------------------------

# is_version X.Y.Z: decimal parts, no leading zeros, at most 9 digits each.
is_version() {
	printf '%s\n' "$1" | grep -Eq '^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$'
}

# version_ge A B: A >= B (both already is_version).
version_ge() {
	_a=$1
	_b=$2
	for _i in 1 2 3; do
		_x=${_a%%.*}
		_y=${_b%%.*}
		[ "$_x" -gt "$_y" ] && return 0
		[ "$_x" -lt "$_y" ] && return 1
		_a=${_a#*.}
		_b=${_b#*.}
	done
	return 0
}

is_version "$AGENTNET_MIN_VERSION" || die "internal: AGENTNET_MIN_VERSION is not X.Y.Z"
if [ -n "$want_version" ]; then
	want_version=${want_version#v}
	is_version "$want_version" || die "--version must be X.Y.Z, got '$want_version'"
	version_ge "$want_version" "$AGENTNET_MIN_VERSION" ||
		die "version $want_version is older than the oldest this installer allows ($AGENTNET_MIN_VERSION)"
fi

# --- platform ----------------------------------------------------------------

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "this script supports macOS and Linux; on Windows see Docs/cli/install.md" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported CPU $(uname -m) (releases exist for amd64 and arm64)" ;;
esac
# An x86_64 shell under Rosetta on Apple silicon: install the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
	arch=arm64
fi

[ -n "$install_dir" ] || {
	[ -n "${HOME:-}" ] || die "HOME is not set; pass --dir"
	install_dir=$HOME/.local/bin
}

# --- how the signature will be checked (decided before downloading) ---------

verifier=
_ossl=$(openssl version 2>/dev/null || true)
case $_ossl in
"OpenSSL "[3-9].* | "OpenSSL "[1-9][0-9].*) verifier=openssl ;;
esac
if [ -z "$verifier" ] && command -v minisign >/dev/null 2>&1; then
	verifier=minisign
fi
if [ -z "$verifier" ]; then
	cat >&2 <<EOF
agentnet install: cannot check the release signature on this machine.
  It needs OpenSSL 3 or newer (found: ${_ossl:-none}) or minisign.
  Nothing was installed; this script never installs an unsigned release.

  Choose one:
    - Homebrew:  $TAP_CMD
    - install minisign (macOS: brew install minisign; Debian/Ubuntu:
      apt install minisign) and run this script again;
    - manually: download SHA256SUMS, SHA256SUMS.minisig and the archive for
      your system from https://github.com/$REPO/releases, then
        minisign -Vm SHA256SUMS -x SHA256SUMS.minisig -P $AGENTNET_MINISIGN_PUBKEY
        sha256sum -c --ignore-missing SHA256SUMS   (macOS: shasum -a 256 -c --ignore-missing SHA256SUMS)
      and copy agentnet and agentnetd from the archive into ~/.local/bin.
EOF
	exit 1
fi

# --- downloads ---------------------------------------------------------------

if [ -n "${AGENTNET_DOWNLOAD_URL:-}" ]; then
	base=${AGENTNET_DOWNLOAD_URL%/}
elif [ -n "$want_version" ]; then
	base=https://github.com/$REPO/releases/download/v$want_version
else
	base=https://github.com/$REPO/releases/latest/download
fi
case $base in
https://*) insecure= ;;
*)
	[ "${AGENTNET_ALLOW_INSECURE_URL:-}" = 1 ] || die "download URL must be https:// (got $base)"
	insecure=1
	;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() {
		if [ -n "$insecure" ]; then
			curl -fsSL --retry 2 -o "$2" "$1"
		else
			curl -fsSL --retry 2 --proto =https --proto-redir =https --tlsv1.2 -o "$2" "$1"
		fi
	}
elif command -v wget >/dev/null 2>&1; then
	# GNU wget can refuse TLS < 1.2 (its --https-only binds only recursive
	# downloads, not redirects); BusyBox wget has neither option. Transport
	# is defence in depth: the signature check below is what counts.
	wget_tls=
	if [ -z "$insecure" ] && wget --version 2>/dev/null | grep -q 'GNU Wget'; then
		wget_tls=--secure-protocol=TLSv1_2
	fi
	fetch() {
		# shellcheck disable=SC2086 # $wget_tls is empty or one flag
		wget -q $wget_tls -O "$2" "$1"
	}
else
	die "needs curl or wget"
fi

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		openssl dgst -sha256 -r "$1" | cut -d' ' -f1
	fi
}

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t agentnet)
if [ -z "$tmp" ] || [ ! -d "$tmp" ]; then
	die "cannot create a temporary directory"
fi
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM HUP

sums=$tmp/SHA256SUMS
fetch "$base/SHA256SUMS" "$sums" || die "cannot download $base/SHA256SUMS"

# --- 1. signature ------------------------------------------------------------

if [ "$verifier" = openssl ]; then
	fetch "$base/SHA256SUMS.sig" "$tmp/SHA256SUMS.sig" || die "cannot download $base/SHA256SUMS.sig"
	printf '%s\n' "$AGENTNET_PUBKEY_PEM" >"$tmp/release.pub"
	out=$(openssl pkeyutl -verify -pubin -inkey "$tmp/release.pub" -rawin \
		-in "$sums" -sigfile "$tmp/SHA256SUMS.sig" 2>&1) || out="FAILED: $out"
	case $out in
	"Signature Verified Successfully"*) ;;
	*) die "SHA256SUMS signature check FAILED (openssl): the release files do not carry a valid signature by the AgentNet release key" ;;
	esac
else
	fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "cannot download $base/SHA256SUMS.minisig"
	minisign -Vqm "$sums" -x "$tmp/SHA256SUMS.minisig" -P "$AGENTNET_MINISIGN_PUBKEY" >/dev/null 2>&1 ||
		die "SHA256SUMS signature check FAILED (minisign): the release files do not carry a valid signature by the AgentNet release key"
fi

# --- 2. contents and version -------------------------------------------------

line_re='^[0-9a-f]{64}  agentnet_(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})_(darwin|linux|windows)_(amd64|arm64)\.(tar\.gz|zip)$'
if grep -Evq "$line_re" "$sums"; then
	die "signed SHA256SUMS has an unexpected line; refusing"
fi
versions=$(sed -E 's/^[0-9a-f]{64}  agentnet_([0-9.]+)_.*$/\1/' "$sums" | sort -u)
case $versions in
*"
"*) die "signed SHA256SUMS names more than one version; refusing" ;;
esac
version=$versions
is_version "$version" || die "signed SHA256SUMS names no version; refusing"
if [ -n "$want_version" ] && [ "$version" != "$want_version" ]; then
	die "asked for $want_version but the signed SHA256SUMS is for $version; refusing"
fi
version_ge "$version" "$AGENTNET_MIN_VERSION" ||
	die "release $version is older than the oldest this installer allows ($AGENTNET_MIN_VERSION); refusing (rollback)"

archive=agentnet_${version}_${os}_${arch}.tar.gz
want_sum=$(awk -v f="$archive" '$2 == f { print $1 }' "$sums")
[ -n "$want_sum" ] || die "the release has no $archive"

# --- 3. archive --------------------------------------------------------------

fetch "$base/$archive" "$tmp/$archive" || die "cannot download $base/$archive"
got_sum=$(sha256_of "$tmp/$archive")
[ "$got_sum" = "$want_sum" ] || die "$archive SHA-256 mismatch (expected $want_sum, got $got_sum); refusing"

mkdir "$tmp/x"
# Only the two binaries, and only as regular files (review 53 L2).
tar -xzf "$tmp/$archive" -C "$tmp/x" agentnet agentnetd || die "cannot unpack agentnet and agentnetd from $archive"
for b in agentnet agentnetd; do
	if [ ! -f "$tmp/x/$b" ] || [ -h "$tmp/x/$b" ]; then die "$archive has no regular file $b"; fi
done

mkdir -p "$install_dir" || die "cannot create $install_dir"
# Stage both binaries before replacing either (R55-133): a failure while
# staging leaves the old install untouched.
for b in agentnet agentnetd; do
	if ! cp "$tmp/x/$b" "$install_dir/.$b.new.$$" || ! chmod 755 "$install_dir/.$b.new.$$"; then
		rm -f "$install_dir/.agentnet.new.$$" "$install_dir/.agentnetd.new.$$" 2>/dev/null
		die "cannot write to $install_dir"
	fi
done
replaced=
for b in agentnet agentnetd; do
	if ! mv -f "$install_dir/.$b.new.$$" "$install_dir/$b"; then
		rm -f "$install_dir/.agentnet.new.$$" "$install_dir/.agentnetd.new.$$" 2>/dev/null
		[ -n "$replaced" ] || die "cannot replace $install_dir/$b"
		fail "partially installed: $replaced replaced, $b not (cannot replace $install_dir/$b); fix that and rerun the installer"
	fi
	replaced="$replaced${replaced:+ and }$b"
done

say "Installed agentnet $version ($os/$arch) into $install_dir (signature checked with $verifier)."
"$install_dir/agentnet" --version || true
case :${PATH:-}: in
*:"$install_dir":*) ;;
*) say "Note: $install_dir is not on your PATH. Add it, e.g.: export PATH=\"$install_dir:\$PATH\"" ;;
esac
say "Next: agentnetd install, then agentnet doctor"
}

main "$@"
