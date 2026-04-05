#!/bin/bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_NAME="cli-proxy-api-plus"
INSTALL_PATH="$HOME/.local/bin/$BINARY_NAME"
BACKUP_PATH="$INSTALL_PATH.bak"
FIX_BRANCH="fix/byok-compaction-cached-tokens"
PLIST_LABEL="com.cliproxyapiplus.server"

cd "$REPO_DIR"

echo "==> Fetching upstream..."
git fetch origin

echo "==> Switching to main and pulling..."
git checkout main
git pull origin main

echo "==> Rebasing fix branch onto main..."
git checkout "$FIX_BRANCH"
if ! git rebase main; then
    echo "ERROR: Rebase conflict. Resolve manually:"
    echo "  cd $REPO_DIR"
    echo "  git rebase --continue  (after fixing conflicts)"
    echo "  ./update-and-build.sh  (re-run after resolving)"
    exit 1
fi

echo "==> Building..."
go build -o "$BINARY_NAME" ./cmd/server

echo "==> Backing up current binary..."
cp "$INSTALL_PATH" "$BACKUP_PATH" 2>/dev/null || true

echo "==> Installing..."
cp "$BINARY_NAME" "$INSTALL_PATH"

echo "==> Restarting proxy..."
launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
sleep 1
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/$PLIST_LABEL.plist"

sleep 2
if launchctl list | grep -q "$PLIST_LABEL"; then
    echo "==> Done. Proxy is running with updated binary."
else
    echo "WARNING: Proxy may not have started. Check: launchctl list | grep cliproxy"
fi
