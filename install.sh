#!/bin/sh
set -eu

REPO="${CX_REPO:-ecylmz/cx}"
DEST_DIR="${CX_INSTALL_DIR:-${HOME}/.local/bin}"
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

fail() { echo "cx install: $*" >&2; exit 1; }

OS_RAW=$(uname -s)
case "$OS_RAW" in
  Darwin) OS=darwin ;;
  Linux)
    OS=linux
    if [ -r /etc/os-release ]; then
      . /etc/os-release
      case "${ID:-}" in
        ubuntu) : ;;
        *) fail "Linux distribution '${ID:-unknown}' is not supported by this installer; supported: Ubuntu" ;;
      esac
    else
      fail "cannot identify Linux distribution; supported: Ubuntu"
    fi
    ;;
  *) fail "unsupported OS: $OS_RAW (supported: macOS, Ubuntu)" ;;
esac

ARCH_RAW=$(uname -m)
case "$ARCH_RAW" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) fail "unsupported architecture: $ARCH_RAW" ;;
esac

ASSET="cx-${OS}-${ARCH}"
PREBUILT="$ROOT/dist/$ASSET"
mkdir -p "$DEST_DIR"
TMPDIR_CX=""
cleanup() { [ -n "$TMPDIR_CX" ] && rm -rf "$TMPDIR_CX"; }
trap cleanup EXIT INT TERM

install_binary() {
  src=$1
  chmod 0755 "$src"
  if [ "$OS" = darwin ] && command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine "$src" 2>/dev/null || true
  fi
  install -m 0755 "$src" "$DEST_DIR/cx"
  chmod 0755 "$DEST_DIR/cx"
  if [ "$OS" = darwin ]; then
    if command -v xattr >/dev/null 2>&1; then
      xattr -d com.apple.quarantine "$DEST_DIR/cx" 2>/dev/null || true
    fi
    if command -v codesign >/dev/null 2>&1; then
      codesign --force --sign - "$DEST_DIR/cx" >/dev/null 2>&1 || true
    fi
  fi
}

verify_download() {
  file=$1
  sums=$2
  expected=$(awk -v n="$ASSET" '$2 == n || $2 == "*" n { print $1; exit }' "$sums")
  [ -n "$expected" ] || fail "SHA256SUMS has no entry for $ASSET"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$file" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$file" | awk '{print $1}')
  else
    fail "no SHA-256 tool found (sha256sum/shasum)"
  fi
  [ "$expected" = "$actual" ] || fail "checksum mismatch for $ASSET"
}

if [ -f "$PREBUILT" ]; then
  install_binary "$PREBUILT"
elif command -v go >/dev/null 2>&1 && [ -f "$ROOT/go.mod" ]; then
  TMPDIR_CX=$(mktemp -d)
  VERSION=$(git -C "$ROOT" describe --tags --always 2>/dev/null | sed 's/^v//' || printf 'dev')
  (cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$TMPDIR_CX/cx" .)
  install_binary "$TMPDIR_CX/cx"
else
  TMPDIR_CX=$(mktemp -d)
  OUT="$TMPDIR_CX/$ASSET"
  SUMS="$TMPDIR_CX/SHA256SUMS"

  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    gh release download --repo "$REPO" --pattern "$ASSET" --pattern "SHA256SUMS" --dir "$TMPDIR_CX" >/dev/null
  else
    TOKEN=${GH_TOKEN:-${GITHUB_TOKEN:-}}
    [ -n "$TOKEN" ] || fail "no local binary/Go compiler found; private releases require authenticated gh CLI or GH_TOKEN"
    command -v curl >/dev/null 2>&1 || fail "curl is required when gh CLI is unavailable"
    command -v python3 >/dev/null 2>&1 || fail "python3 is required for token-based release install when gh CLI is unavailable"

    META="$TMPDIR_CX/release.json"
    curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.github+json" \
      "https://api.github.com/repos/$REPO/releases/latest" -o "$META"

    IDS=$(python3 - "$META" "$ASSET" <<'PY'
import json, sys
meta = json.load(open(sys.argv[1]))
want = {sys.argv[2], "SHA256SUMS"}
found = {a.get("name"): a.get("id") for a in meta.get("assets", []) if a.get("name") in want}
print(found.get(sys.argv[2], ""))
print(found.get("SHA256SUMS", ""))
PY
)
    ASSET_ID=$(printf '%s\n' "$IDS" | sed -n '1p')
    SUMS_ID=$(printf '%s\n' "$IDS" | sed -n '2p')
    [ -n "$ASSET_ID" ] || fail "latest release does not contain $ASSET"
    [ -n "$SUMS_ID" ] || fail "latest release does not contain SHA256SUMS"

    curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/octet-stream" \
      "https://api.github.com/repos/$REPO/releases/assets/$ASSET_ID" -o "$OUT"
    curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/octet-stream" \
      "https://api.github.com/repos/$REPO/releases/assets/$SUMS_ID" -o "$SUMS"
  fi

  [ -f "$OUT" ] || fail "release download failed: $ASSET"
  [ -f "$SUMS" ] || fail "release download failed: SHA256SUMS"
  verify_download "$OUT" "$SUMS"
  install_binary "$OUT"
fi

echo "installed $DEST_DIR/cx"
if ! printf '%s' ":$PATH:" | grep -q ":$DEST_DIR:"; then
  echo "note: add $DEST_DIR to PATH"
fi
