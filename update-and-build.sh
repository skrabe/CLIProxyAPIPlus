#!/usr/bin/env bash
# One command to update the fork and roll it out everywhere:
#
#   ./update-and-build.sh               rebase main onto upstream, test, build,
#                                       deploy to this Mac + every host, push main
#   ./update-and-build.sh --no-rebase   ship the current checkout as-is
#   ./update-and-build.sh --local-only  only this machine (no remote hosts)
#   ./update-and-build.sh --no-push     don't push main to the fork afterwards
#   HOSTS="hermes" ./update-and-build.sh   override the remote host list
#
# Each machine is updated by deploy/install-on-host.sh: it swaps the binary,
# restarts one profile at a time, sends each a real request, and rolls back on
# failure. The script stops at the first machine that fails and does not push.
# See FORK.md for what the fork changes and how the machines are set up.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_NAME="cli-proxy-api-plus"
BRANCH="main"
UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-kaitranntt}"
UPSTREAM_URL="https://github.com/kaitranntt/CLIProxyAPIPlus.git"
FORK_REMOTE="${FORK_REMOTE:-fork}"
HOSTS="${HOSTS:-hermes omarchy}"

rebase=1 push=1 local_only=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-rebase) rebase=0 ;;
        --no-push) push=0 ;;
        --local-only) local_only=1 ;;
        -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
    shift
done

cd "$REPO_DIR"
[[ "$(git branch --show-current)" == "$BRANCH" ]] || { echo "ERROR: check out $BRANCH first." >&2; exit 1; }

if [[ $rebase -eq 1 ]]; then
    if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
        echo "ERROR: uncommitted changes; commit or stash first." >&2
        exit 1
    fi
    git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1 || git remote add "$UPSTREAM_REMOTE" "$UPSTREAM_URL"
    echo "==> Rebasing $BRANCH onto $UPSTREAM_REMOTE/main..."
    git fetch -q "$UPSTREAM_REMOTE"
    if ! git rebase "$UPSTREAM_REMOTE/main"; then
        echo "ERROR: rebase conflict. Resolve, 'git rebase --continue', then re-run with --no-rebase." >&2
        exit 1
    fi
fi

echo "==> Testing the fork's own changes..."
go test ./internal/api/handlers/management/ >/dev/null

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
LDFLAGS="-s -w -X main.Version=$VERSION -X main.Commit=$(git rev-parse --short HEAD) -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

build() {
    echo "==> Building $1/$2 ($VERSION)..."
    CGO_ENABLED=0 GOOS="$1" GOARCH="$2" go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$1-$2" ./cmd/server
}

build "$(go env GOOS)" "$(go env GOARCH)"
[[ $local_only -eq 0 && -n "$HOSTS" ]] && build linux amd64

echo "==> Deploying locally..."
install -m 0755 "$OUT/$(go env GOOS)-$(go env GOARCH)" "$HOME/.local/bin/$BINARY_NAME.new"
bash deploy/install-on-host.sh

if [[ $local_only -eq 0 ]]; then
    for host in $HOSTS; do
        echo "==> Deploying to $host..."
        scp -q "$OUT/linux-amd64" "$host:.local/bin/$BINARY_NAME.new"
        ssh "$host" 'bash -s' < deploy/install-on-host.sh
    done
fi

if [[ $push -eq 1 ]]; then
    echo "==> Pushing $BRANCH to $FORK_REMOTE..."
    git push --force-with-lease "$FORK_REMOTE" "$BRANCH"
fi

echo "==> Done ($VERSION)."
