#!/bin/sh
# jevql installer: curl -fsSL https://jevql.dev/install.sh | sh
# Detects OS and architecture, downloads the matching release tarball,
# verifies its sha256 and installs the jevql binary.
#
# Environment:
#   JEVQL_VERSION      install a specific version (default: latest release)
#   JEVQL_INSTALL_DIR  install directory (default: /usr/local/bin, else ~/.local/bin)
set -eu

REPO="kylemclaren/jevql"
API="https://api.github.com/repos/${REPO}/releases"

say() { printf '%s\n' "$*" >&2; }
die() { say "install.sh: $*"; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "need '$1' on PATH"; }

need curl
need tar

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin) os=darwin ;;
  linux) os=linux ;;
  *) die "unsupported OS: $os (jevql ships macOS and Linux binaries; on other systems use: go install github.com/${REPO}/cmd/jevql@latest)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac

version="${JEVQL_VERSION:-}"
if [ -z "$version" ]; then
  version=$(curl -fsSL "${API}/latest" | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$version" ] || die "could not determine the latest release (set JEVQL_VERSION=x.y.z)"
fi
version="${version#v}"

base="https://github.com/${REPO}/releases/download/v${version}"
file="jevql_${version}_${os}_${arch}.tar.gz"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t jevql)
trap 'rm -rf "$tmp"' EXIT

say "Downloading jevql ${version} for ${os}/${arch}..."
curl -fsSL -o "$tmp/$file" "${base}/${file}" || die "download failed: ${base}/${file}"
curl -fsSL -o "$tmp/checksums.txt" "${base}/checksums.txt" || die "could not fetch checksums.txt"

expected=$(grep " ${file}\$" "$tmp/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || die "no checksum listed for ${file}"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$file" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$file" | awk '{print $1}')
else
  die "need sha256sum or shasum to verify the download"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for ${file}"

tar -xzf "$tmp/$file" -C "$tmp" jevql

dir="${JEVQL_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then
    dir=/usr/local/bin
  else
    dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$dir"
if [ -w "$dir" ]; then
  install -m 0755 "$tmp/jevql" "$dir/jevql"
else
  say "Installing to $dir needs sudo."
  sudo install -m 0755 "$tmp/jevql" "$dir/jevql"
fi

say "Installed $dir/jevql"
"$dir/jevql" --version >&2 || true
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say ""; say "Add it to your PATH:"; say "  export PATH=\"$dir:\$PATH\"" ;;
esac
say ""
say "Next: run 'jevql' and it will ask for a database URL and a TypeSafe API key."
