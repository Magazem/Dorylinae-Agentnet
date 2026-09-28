#!/bin/sh
# Render packaging/homebrew/agentnet.rb for one release (ticket 4.4a).
#
#   sh packaging/homebrew/render.sh VERSION SHA256SUMS [BASE_URL] > agentnet.rb
#
# SHA256SUMS must be the release's file whose signature you have already
# verified (go run ./tools/releasesign verify ...). BASE_URL defaults to the
# GitHub release download directory for vVERSION; CI passes a file:// URL to
# test the formula against locally built archives.

set -eu

[ $# -ge 2 ] || { echo "usage: render.sh VERSION SHA256SUMS [BASE_URL]" >&2; exit 2; }
version=$1
sums=$2
base=${3:-https://github.com/Magazem/Dorylinae-Agentnet/releases/download/v$version}
here=$(cd "$(dirname "$0")" && pwd)

printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$' ||
	{ echo "render.sh: VERSION must be X.Y.Z" >&2; exit 2; }
case $base in *[\|\&\\\"]*) echo "render.sh: bad BASE_URL" >&2; exit 2 ;; esac

sum() {
	s=$(awk -v f="agentnet_${version}_$1.tar.gz" '$2 == f { print $1 }' "$sums")
	printf '%s\n' "$s" | grep -Eq '^[0-9a-f]{64}$' || { echo "render.sh: no valid sum for $1 in $sums" >&2; exit 1; }
	printf '%s' "$s"
}
da=$(sum darwin_arm64)
di=$(sum darwin_amd64)
la=$(sum linux_arm64)
li=$(sum linux_amd64)

sed -e "s|@VERSION@|$version|g" -e "s|@BASE_URL@|$base|g" \
	-e "s|@SHA256_DARWIN_ARM64@|$da|" -e "s|@SHA256_DARWIN_AMD64@|$di|" \
	-e "s|@SHA256_LINUX_ARM64@|$la|" -e "s|@SHA256_LINUX_AMD64@|$li|" \
	-e '/^# Homebrew formula TEMPLATE/,/^# not the release signature\.$/d' \
	"$here/agentnet.rb"
