#!/bin/sh
set -eu
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
PREBUILT="$ROOT/dist/cx-${OS}-${ARCH}"
DEST="${HOME}/.local/bin"
mkdir -p "$DEST"
if [ -f "$PREBUILT" ]; then
  install -m 0755 "$PREBUILT" "$DEST/cx"
elif command -v go >/dev/null 2>&1; then
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT INT TERM
  (cd "$ROOT" && go build -trimpath -ldflags="-s -w" -o "$TMP/cx" .)
  install -m 0755 "$TMP/cx" "$DEST/cx"
else
  echo "no prebuilt binary found and Go is not installed" >&2
  exit 1
fi
echo "installed $DEST/cx"
