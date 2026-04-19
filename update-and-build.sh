#!/bin/bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_NAME="cli-proxy-api-plus"
INSTALL_PATH="$HOME/.local/bin/$BINARY_NAME"
BACKUP_PATH="$INSTALL_PATH.bak"
FIX_BRANCH="${FIX_BRANCH:-feat/claude-opus-4-7}"
PLIST_LABEL="com.cliproxyapiplus.server"

# The remote to pull upstream main from. Defaults to "upstream" (standard
# fork workflow: origin=your-fork, upstream=router-for-me/CLIProxyAPIPlus).
# If you cloned the original repo directly, set UPSTREAM_REMOTE=origin.
UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-upstream}"

cd "$REPO_DIR"

if ! git remote get-url "$UPSTREAM_REMOTE" > /dev/null 2>&1; then
    echo "ERROR: git remote '$UPSTREAM_REMOTE' not configured."
    echo "  Add it with:  git remote add upstream https://github.com/router-for-me/CLIProxyAPIPlus.git"
    echo "  Or run with:  UPSTREAM_REMOTE=origin ./update-and-build.sh"
    exit 1
fi

echo "==> Fetching $UPSTREAM_REMOTE..."
git fetch "$UPSTREAM_REMOTE"

echo "==> Switching to main and pulling..."
git checkout main
git pull "$UPSTREAM_REMOTE" main

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
