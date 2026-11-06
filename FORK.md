# skrabe/CLIProxyAPIPlus

This fork is upstream [kaitranntt/CLIProxyAPIPlus](https://github.com/kaitranntt/CLIProxyAPIPlus) `main` plus a few small commits on top. There is one branch, `main`. Keep fork changes small, because they get replayed onto upstream on every update.

## What the fork adds

- **Separate seats for one Claude email.** The management API reports each Claude auth's `organization_uuid` as `id_token.chatgpt_account_id`, so T3 Code's usage panel shows a personal seat and a team seat on the same email as two accounts. The code is `claudeWorkspaceClaims` in `internal/api/handlers/management/auth_files.go`.
- **One-command update and deploy:** `update-and-build.sh` and `deploy/install-on-host.sh`.

New model support, cache-control handling and tool-name cloaking all come from upstream. Older per-model branches are archived as `archive/20261106/*` tags.

## Updating

```sh
./update-and-build.sh
```

The script runs these steps and stops at the first machine that fails:

1. Rebase onto upstream.
2. Test the fork's package.
3. Build for macOS and linux/amd64.
4. Deploy to this Mac, then `hermes`, then `omarchy`. On each machine it restarts each profile, sends a real `claude-haiku-5-5` request, and rolls back if the request fails.
5. Push `main` to the fork.

The previous binary is kept as `~/.local/bin/cli-proxy-api-plus.bak` on every machine.

If the rebase conflicts, resolve it, run `git rebase --continue`, then `./update-and-build.sh --no-rebase`.

## Machines

All three machines have the same layout:

| | |
|---|---|
| Binary | `~/.local/bin/cli-proxy-api-plus` |
| Profiles | `~/.cli-proxy-api/profiles/{personal,work}/`, each with `config.yaml`, `auth/`, `client.env`, `management.key` |
| Ports | personal `:8318`, work `:8319` |
| Seats | Both use `batric@decidr.ai`. personal = the personal Max sub, work = the DecidrDevs team seat. |

Services:

- **macOS:** launchd agents `com.cliproxyapiplus.{personal,work}` with `RunAtLoad` and `KeepAlive`.
  - `com.cliproxyapiplus.wake` (07:55) restarts both profiles daily via `~/.local/bin/cliproxyapiplus-refresh`.
  - `com.cliproxyapiplus.logrotate` (04:00) rotates stdout/stderr.
- **Linux (hermes, omarchy):** systemd user units `cliproxy-{personal,work}.service`. They are enabled, use `Restart=always`, and linger is on, so they start at boot without a login.

To re-authenticate a seat, sign in on each machine separately. Copied tokens break when one machine refreshes them.

```sh
cliproxy-login personal   # or: work
```

Pick the matching seat in the browser, then check it: `GET https://api.anthropic.com/api/oauth/profile` with the token and `anthropic-beta: oauth-2025-04-20` should show the right organization.
