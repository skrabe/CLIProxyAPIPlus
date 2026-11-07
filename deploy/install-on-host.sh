#!/usr/bin/env bash
# Installs ~/.local/bin/cli-proxy-api-plus.new on the machine it runs on, then
# restarts each proxy profile one at a time and sends it a real request. If any
# profile fails, the previous binary is restored and every profile restarted.
#
# Runs locally (macOS, launchd) or over `ssh host bash -s < install-on-host.sh`
# (Linux, systemd --user). Profiles are the dirs under ~/.cli-proxy-api/profiles
# that have a client.env (which holds the port and the client API key).
set -uo pipefail

BIN="$HOME/.local/bin/cli-proxy-api-plus"
PROFILES_DIR="$HOME/.cli-proxy-api/profiles"
PROBE_MODEL="${PROBE_MODEL:-claude-haiku-5-5}"
HOST="$(hostname -s 2>/dev/null || hostname)"

say() { echo "[$HOST] $*"; }

[[ -f "$BIN.new" ]] || { say "missing $BIN.new"; exit 1; }

restart_profile() {
    local p="$1"
    if [[ "$(uname -s)" == "Darwin" ]]; then
        launchctl kickstart -k "gui/$(id -u)/com.cliproxyapiplus.$p"
    else
        systemctl --user restart "cliproxy-$p.service"
    fi
}

service_state() {
    if [[ "$(uname -s)" == "Darwin" ]]; then
        local out
        out="$(launchctl print "gui/$(id -u)/com.cliproxyapiplus.$1" 2>&1 || true)"
        sed -n 's/^[[:space:]]*state = //p' <<<"$out" | head -n1
    else
        systemctl --user is-active "cliproxy-$1.service" 2>/dev/null || true
    fi
}

# Waits up to 20s for the service to be up (launchd/systemd may still be spawning).
profile_running() {
    local p="$1" state=""
    for _ in $(seq 1 20); do
        state="$(service_state "$p")"
        [[ "$state" == "running" || "$state" == "active" ]] && return 0
        sleep 1
    done
    say "$p: service state '$state'"
    return 1
}

# Linux only: make sure the profile comes back after a reboot.
check_boot() {
    local p="$1"
    [[ "$(uname -s)" == "Darwin" ]] && return 0
    systemctl --user is-enabled -q "cliproxy-$p.service" || {
        systemctl --user enable -q "cliproxy-$p.service" && say "$p: enabled at boot"
    }
    [[ "$(loginctl show-user "$USER" -p Linger --value 2>/dev/null)" == "yes" ]] \
        || say "WARNING: linger is off; run 'sudo loginctl enable-linger $USER' or the proxy only starts after login"
}

# 200, or a 429 for a seat that is out of quota, both prove auth + routing work.
probe() {
    local p="$1" env="$PROFILES_DIR/$1/client.env" url key out code
    url="$(. "$env"; echo "$ANTHROPIC_BASE_URL")"
    key="$(. "$env"; echo "$ANTHROPIC_AUTH_TOKEN")"
    for _ in $(seq 1 20); do curl -s -o /dev/null "$url/" && break; sleep 1; done
    out="$(curl -s -m 90 -w '\n%{http_code}' "$url/v1/messages" \
        -H "x-api-key: $key" -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \
        -d "{\"model\":\"$PROBE_MODEL\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"Reply with just OK\"}]}")"
    code="$(tail -n1 <<<"$out")"
    say "$p: probe HTTP $code"
    [[ "$code" == 200 ]] && return 0
    if [[ "$code" == 429 ]] && grep -q "account's rate limit" <<<"$out"; then
        say "$p: seat is rate-limited, but auth and routing work"
        return 0
    fi
    say "$p: $(head -n1 <<<"$out" | cut -c1-300)"
    return 1
}

profiles=()
for env in "$PROFILES_DIR"/*/client.env; do
    [[ -f "$env" ]] && profiles+=("$(basename "$(dirname "$env")")")
done
[[ ${#profiles[@]} -gt 0 ]] || { say "no profiles under $PROFILES_DIR"; exit 1; }

chmod 0755 "$BIN.new"
# Refuse a binary that cannot run here (e.g. built for the wrong CPU); without
# this, systemd restart-loops on "Exec format error" once the old process dies.
if ! "$BIN.new" --help 2>&1 | grep -q 'Version: '; then
    say "refusing $BIN.new: it does not run on $(uname -s)/$(uname -m) ($(file -b "$BIN.new" 2>/dev/null | cut -d, -f1-2))"
    rm -f "$BIN.new"
    exit 1
fi
# Only keep a backup that actually runs, so a rollback never restores a broken binary.
if [[ -f "$BIN" ]] && "$BIN" --help 2>&1 | grep -q 'Version: '; then
    cp -p "$BIN" "$BIN.bak"
fi
mv "$BIN.new" "$BIN"
say "installed $("$BIN" --help 2>&1 | grep -m1 -oE 'Version: [^,]+')"

for p in "${profiles[@]}"; do
    restart_profile "$p"
    sleep 2
    if profile_running "$p" && probe "$p"; then
        check_boot "$p"
        continue
    fi
    say "$p FAILED; rolling back to the previous binary"
    [[ -f "$BIN.bak" ]] && cp -p "$BIN.bak" "$BIN"
    for q in "${profiles[@]}"; do restart_profile "$q"; done
    exit 1
done
say "OK (${profiles[*]})"
