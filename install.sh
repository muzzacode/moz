#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

echo "[moz] Building..."
mkdir -p "$ROOT/bin"
(cd "$ROOT" && go build -o bin/moz ./cmd/moz)

echo "[moz] Installing to $INSTALL_DIR..."
install -d "$INSTALL_DIR"
install -m 0755 "$ROOT/bin/moz" "$INSTALL_DIR/moz"

echo "[moz] Installed. Run 'moz' to start."

if ! command -v moz >/dev/null 2>&1; then
	echo "[moz] Add $INSTALL_DIR to your PATH if 'moz' is not found."
fi
