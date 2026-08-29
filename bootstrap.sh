#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"

command_exists() { command -v "$1" >/dev/null 2>&1; }

install_go() {
  if command_exists go; then
    echo "[moz] Go is already installed: $(go version)"
    return
  fi
  if command_exists brew; then
    echo "[moz] Installing Go via Homebrew..."
    brew install go
  else
    echo "[moz] Homebrew not found. Please install Go manually."
    exit 1
  fi
}

check_ollama() {
  if command_exists ollama; then
    echo "[moz] Ollama found: $(ollama --version)"
  else
    echo "[moz] Ollama not found. Install it or run PAIEP: make start"
  fi
}

echo "[moz] Bootstrapping environment..."
install_go
check_ollama

echo "[moz] Building..."
make -C "$ROOT" build

echo "[moz] Bootstrap complete. Run 'make install' to install to PATH."
