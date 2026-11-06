#!/usr/bin/env bash
# Rebase the fork's main onto the CLIProxyAPIPlus upstream, build, install and
# restart the local proxy profiles. Optionally ship a linux build to remote hosts.
#
#   ./update-and-build.sh                 rebase + build + install + restart locally
#   ./update-and-build.sh --no-rebase     build the current checkout as-is
#   ./update-and-build.sh --remote hermes --remote omarchy
#                                         also cross-compile linux/amd64 and deploy
#
# Remotes run systemd user units cliproxy-<profile>.service and keep the binary
# at ~/.local/bin/cli-proxy-api-plus. Locally, macOS uses launchd agents
# com.cliproxyapiplus.<profile> and Linux uses the same systemd units.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
BINARY_NAME="cli-proxy-api-plus"
INSTALL_PATH="$HOME/.local/bin/$BINARY_NAME"
BRANCH="${BRANCH:-main}"
UPSTREAM_REMOTE="${UPSTREAM_REMOTE:-kaitranntt}"
UPSTREAM_URL="${UPSTREAM_URL:-https://github.com/kaitranntt/CLIProxyAPIPlus.git}"
PROFILES="${PROFILES:-personal work}"

rebase=1
remotes=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-rebase) rebase=0 ;;
        --remote) remotes+=("$2"); shift ;;
        -h|--help) sed -n '2,13p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
    shift
done

cd "$REPO_DIR"

if [[ $rebase -eq 1 ]]; then
    if [[ -n "$(git status --porcelain --untracked-files=no)" ]]; then
        echo "ERROR: working tree has uncommitted changes; commit or stash first." >&2
        exit 1
    fi
    git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1 || git remote add "$UPSTREAM_REMOTE" "$UPSTREAM_URL"
    echo "==> Fetching $UPSTREAM_REMOTE..."
    git fetch "$UPSTREAM_REMOTE"
    git checkout "$BRANCH"
    echo "==> Rebasing $BRANCH onto $UPSTREAM_REMOTE/main..."
    if ! git rebase "$UPSTREAM_REMOTE/main"; then
        echo "ERROR: rebase conflict. Resolve, run 'git rebase --continue', then re-run with --no-rebase." >&2
        exit 1
    fi
fi

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD)"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.Version=$VERSION -X main.Commit=$COMMIT -X main.BuildDate=$BUILT_AT"

build() {
    local goos="$1" goarch="$2" out="$3"
    echo "==> Building $out ($goos/$goarch, $VERSION)..."
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/server
}

restart_local() {
    local profile
    for profile in $PROFILES; do
        if [[ "$(uname -s)" == "Darwin" ]]; then
            local label="com.cliproxyapiplus.$profile"
            local plist="$HOME/Library/LaunchAgents/$label.plist"
            [[ -f "$plist" ]] || { echo "    skip $profile (no $plist)"; continue; }
            launchctl kickstart -k "gui/$(id -u)/$label" 2>/dev/null || {
                launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
                launchctl bootstrap "gui/$(id -u)" "$plist"
            }
        else
            systemctl --user restart "cliproxy-$profile.service"
        fi
        echo "    restarted $profile"
    done
}

build "$(go env GOOS)" "$(go env GOARCH)" "$BINARY_NAME"
mkdir -p "$(dirname "$INSTALL_PATH")"
[[ -f "$INSTALL_PATH" ]] && cp "$INSTALL_PATH" "$INSTALL_PATH.bak"
install -m 0755 "$BINARY_NAME" "$INSTALL_PATH"
echo "==> Installed $INSTALL_PATH; restarting profiles..."
restart_local

if [[ ${#remotes[@]} -gt 0 ]]; then
    build linux amd64 "$BINARY_NAME-linux-amd64"
    for host in "${remotes[@]}"; do
        echo "==> Deploying to $host..."
        scp -q "$BINARY_NAME-linux-amd64" "$host:.local/bin/$BINARY_NAME.new"
        # shellcheck disable=SC2029
        ssh "$host" "set -e; cd ~/.local/bin; [ -f $BINARY_NAME ] && cp $BINARY_NAME $BINARY_NAME.bak; chmod 0755 $BINARY_NAME.new; mv $BINARY_NAME.new $BINARY_NAME; for p in $PROFILES; do systemctl --user restart cliproxy-\$p.service; echo \"    restarted \$p\"; done"
    done
fi

echo "==> Done ($VERSION)."
