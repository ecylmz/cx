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
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$TMPDIR_CX/cx" .)
  install_binary "$TMPDIR_CX/cx"
}

release_available() {
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release view --repo "$REPO" >/dev/null 2>&1
    return $?
  fi
  command -v curl >/dev/null 2>&1 || return 1
  token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
  if [ -n "$token" ]; then
    curl -fsSL -o /dev/null \
      -H "Authorization: Bearer $token" \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/$REPO/releases/latest"
  else
    curl -fsSL -o /dev/null "https://github.com/$REPO/releases/latest"
  fi
}

download_release() {
  out="$TMPDIR_CX/$ASSET"
  sums="$TMPDIR_CX/SHA256SUMS"

  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release download --repo "$REPO" --pattern "$ASSET" --pattern SHA256SUMS --dir "$TMPDIR_CX" >/dev/null
  elif [ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]; then
    token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
    command -v curl >/dev/null 2>&1 || fail "curl is required"
    command -v python3 >/dev/null 2>&1 || fail "python3 is required when using GH_TOKEN without gh"

    meta="$TMPDIR_CX/release.json"
    curl -fsSL \
      -H "Authorization: Bearer $token" \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/$REPO/releases/latest" -o "$meta"

    ids=$(python3 - "$meta" "$ASSET" <<'PY'
import json, sys
meta = json.load(open(sys.argv[1]))
want = {sys.argv[2], "SHA256SUMS"}
assets = {a.get("name"): a.get("id") for a in meta.get("assets", []) if a.get("name") in want}
print(assets.get(sys.argv[2], ""))
print(assets.get("SHA256SUMS", ""))
PY
)
    asset_id=$(printf '%s\n' "$ids" | sed -n '1p')
    sums_id=$(printf '%s\n' "$ids" | sed -n '2p')
    [ -n "$asset_id" ] || fail "latest release does not contain $ASSET"
    [ -n "$sums_id" ] || fail "latest release does not contain SHA256SUMS"

    curl -fsSL -H "Authorization: Bearer $token" -H "Accept: application/octet-stream" \
      "https://api.github.com/repos/$REPO/releases/assets/$asset_id" -o "$out"
    curl -fsSL -H "Authorization: Bearer $token" -H "Accept: application/octet-stream" \
      "https://api.github.com/repos/$REPO/releases/assets/$sums_id" -o "$sums"
  else
    command -v curl >/dev/null 2>&1 || fail "curl is required"
    base="https://github.com/$REPO/releases/latest/download"
    if ! curl -fsSL "$base/$ASSET" -o "$out" || ! curl -fsSL "$base/SHA256SUMS" -o "$sums"; then
      fail "release download failed; for a private repository run 'gh auth login' or set GH_TOKEN"
    fi
  fi

  [ -f "$out" ] || fail "release download failed: $ASSET"
  [ -f "$sums" ] || fail "release download failed: SHA256SUMS"
  verify_download "$out" "$sums"
  install_binary "$out"
}

if [ "${CX_BUILD_FROM_SOURCE:-0}" = 1 ]; then
  build_source
elif [ -f "$PREBUILT" ]; then
  install_binary "$PREBUILT"
elif release_available; then
  download_release
elif [ -f "$ROOT/go.mod" ] && command -v go >/dev/null 2>&1; then
  echo "no release found; building current checkout"
  build_source
else
  download_release
fi

printf 'installed %s\n' "$DEST_DIR/cx"
"$DEST_DIR/cx" version
if ! printf '%s' ":$PATH:" | grep -q ":$DEST_DIR:"; then
  printf 'add %s to PATH\n' "$DEST_DIR"
fi
