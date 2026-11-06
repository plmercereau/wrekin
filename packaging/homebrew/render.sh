#!/bin/sh
# Renders the Homebrew formula for one release.
#
#   sh packaging/homebrew/render.sh <version> <checksums.txt> > Formula/wrekin.rb
#
# <version> is the tag without its leading v (0.1.0); <checksums.txt> is the
# file GoReleaser writes next to the archives (sha256 and file name per line).
# Every one of the four archives must be listed, or the script fails.
set -eu

version=$1
checksums=$2
here=$(dirname "$0")

sha_of() {
  # $1: the archive's os_arch suffix, e.g. darwin_arm64.
  sha=$(awk -v name="wrekin_${version}_$1.tar.gz" '$2 == name { print $1 }' "$checksums")
  if [ -z "$sha" ]; then
    echo "render.sh: no checksum for wrekin_${version}_$1.tar.gz in $checksums" >&2
    exit 1
  fi
  printf '%s' "$sha"
}

darwin_arm64=$(sha_of darwin_arm64)
darwin_amd64=$(sha_of darwin_amd64)
linux_arm64=$(sha_of linux_arm64)
linux_amd64=$(sha_of linux_amd64)

sed \
  -e "s/@VERSION@/$version/g" \
  -e "s/@SHA_DARWIN_ARM64@/$darwin_arm64/g" \
  -e "s/@SHA_DARWIN_AMD64@/$darwin_amd64/g" \
  -e "s/@SHA_LINUX_ARM64@/$linux_arm64/g" \
  -e "s/@SHA_LINUX_AMD64@/$linux_amd64/g" \
  "$here/wrekin.rb.tmpl"
