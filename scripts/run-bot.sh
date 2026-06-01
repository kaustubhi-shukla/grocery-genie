#!/bin/bash
# Kills any running GroceryGenie bot, then starts a fresh one.
# Run from the project root:   ./scripts/run-bot.sh
#
# Why this exists: `go run` spawns a compiled binary under a long
# `/var/folders/.../exe/bot` path that does not match the literal
# string "go run" — so simple `pkill -f "go run"` misses it. This
# script targets BOTH patterns to make sure we never end up with
# multiple bot instances polling Telegram (which causes duplicate
# replies — Telegram delivers each update to one polling client,
# but during transitions both can pick it up).

set -e

# Move to the script's parent directory (project root).
cd "$(dirname "$0")/.."

# Make Homebrew tools (Go) available even when run from a fresh shell.
eval "$(/opt/homebrew/bin/brew shellenv zsh)"

echo "→ Killing any existing bot processes..."
pkill -9 -f "go-build.*exe/bot" 2>/dev/null || true
pkill -9 -f "go run ./cmd/bot" 2>/dev/null || true
sleep 1

# Confirm clean state.
if pgrep -f "exe/bot" >/dev/null; then
  echo "✗ Bot processes still alive — please check manually with 'ps aux | grep bot'"
  exit 1
fi
echo "✓ No bot processes running"

echo "→ Starting fresh bot..."
go run ./cmd/bot/
