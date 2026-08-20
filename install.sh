#!/bin/sh
set -eu

REPO="${CX_REPO:-ecylmz/cx}"
DEST_DIR="${CX_INSTALL_DIR:-${HOME}/.local/bin}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || pwd)"

fail() { echo "cx install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)
    OS=linux
    if [ -r /etc/os-release ]; then
      . /etc/os-release
      [ "${ID:-}" = ubuntu ] || fail "supported Linux distribution: Ubuntu"
    else
      fail "cannot identify Linux distribution; supported: Ubuntu"
    fi
    ;;
  *) fail "supported systems: macOS and Ubuntu" ;;
esac

case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

ASSET="cx-${OS}-${ARCH}"
PREBUILT="$ROOT/dist/$ASSET"
TMPDIR_CX="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_CX"' EXIT INT TERM
mkdir -p "$DEST_DIR"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    fail "sha256sum or shasum is required"
  fi
}

verify_download() {
  file=$1
  sums=$2
  expected=$(awk -v n="$ASSET" '$2 == n || $2 == "*" n { print $1; exit }' "$sums")
  [ -n "$expected" ] || fail "SHA256SUMS has no entry for $ASSET"
  actual=$(sha256_file "$file")
  [ "$expected" = "$actual" ] || fail "checksum mismatch for $ASSET"
}

prepare_macos_binary() {
  file=$1
  [ "$OS" = darwin ] || return 0
  if command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine "$file" 2>/dev/null || true
  fi
  if command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$file" >/dev/null 2>&1 || true
  fi
}

install_binary() {
  src=$1
  chmod 0755 "$src"
  prepare_macos_binary "$src"
  install -m 0755 "$src" "$DEST_DIR/cx"
  chmod 0755 "$DEST_DIR/cx"
  prepare_macos_binary "$DEST_DIR/cx"
}

build_source() {
  command -v go >/dev/null 2>&1 || fail "Go is required to build from source"
  [ -f "$ROOT/go.mod" ] || fail "run source builds from a cx checkout"
  version=$(git -C "$ROOT" describe --tags --always 2>/dev/null | sed 's/^v//' || printf dev)
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/ecylmz/cx/internal/cx.Version=$version" -o "$TMPDIR_CX/cx" ./cmd/cx)
  install_binary "$TMPDIR_CX/cx"
}

download_release() {
  command -v curl >/dev/null 2>&1 || fail "curl is required"
  out="$TMPDIR_CX/$ASSET"
  sums="$TMPDIR_CX/SHA256SUMS"
  base="https://github.com/$REPO/releases/latest/download"

  if ! curl -fsSL "$base/$ASSET" -o "$out"; then
    return 1
  fi
  if ! curl -fsSL "$base/SHA256SUMS" -o "$sums"; then
    return 1
  fi

  verify_download "$out" "$sums"
  install_binary "$out"
}

if [ "${CX_BUILD_FROM_SOURCE:-0}" = 1 ]; then
  build_source
elif [ -f "$PREBUILT" ]; then
  install_binary "$PREBUILT"
elif download_release; then
  :
elif [ -f "$ROOT/go.mod" ] && command -v go >/dev/null 2>&1; then
  echo "release download failed; building current checkout"
  build_source
else
  fail "could not download the latest release"
fi

printf 'installed %s\n' "$DEST_DIR/cx"
"$DEST_DIR/cx" version
if ! printf '%s' ":$PATH:" | grep -q ":$DEST_DIR:"; then
  printf 'add %s to PATH\n' "$DEST_DIR"
fi
