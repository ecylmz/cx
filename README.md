# cx

`cx` is a small, file-based Codex account switcher for macOS and Linux. It keeps Codex sessions, history, memories and configuration in the normal shared `CODEX_HOME`, while each ChatGPT account owns exactly one canonical `auth.json`.

The active `~/.codex/auth.json` is a symlink to the selected account credential. Codex therefore refreshes the canonical credential in place; `cx` never implements OAuth refresh itself and never copies credentials during switching.

## Features

- Add accounts with the official `codex login --device-auth` flow.
- Interactive account selection and atomic symlink switching.
- Shared Codex sessions/memories/history across accounts.
- **Weekly quota only**: live used/remaining percentage, locale-aware full reset date/time, and remaining duration.
- `cx` and `cx status` perform a live `account/rateLimits/read` through `codex app-server` every time.
- Cached quota is used only as a visibly stale fallback when a live request fails.
- JSON status output for scripting.
- `cx use NAME --resume` switches and runs `codex resume --last`.
- Credential identity mismatch protection and `0600`/`0700` storage.

## Install

The downloadable project ZIP includes prebuilt binaries in `dist/`. A Git clone builds from source automatically when you run `./install.sh`. You can also build manually:

```sh
go build -o cx .
install -m 0755 cx ~/.local/bin/cx
```

Requirements: a recent `codex` CLI in `PATH`; `stty` for the interactive dashboard (standard on macOS/Linux).

## Quick start

```sh
cx add main
cx add backup
cx
```

Remote/headless login works naturally because account creation delegates to:

```sh
codex login --device-auth
```

inside an isolated temporary `CODEX_HOME`.

## Commands

```text
cx                         live weekly quota dashboard
cx status [NAME] [--json] live weekly quota status
cx add [NAME]             add via device auth
cx relogin NAME           safely replace one credential
cx use [NAME] [--resume]  switch; interactive without NAME
cx current
cx list
cx rename OLD NEW
cx remove NAME
cx doctor
cx version
```

Dashboard keys: `↑/↓` or `j/k`, `enter`, `r`, `q`.

## Storage

```text
~/.codex/
  sessions/       shared
  memories/       shared
  history.jsonl   shared
  config.toml     shared; cx enforces cli_auth_credentials_store = "file"
  auth.json -> ~/.local/share/cx/accounts/<id>/auth.json

~/.local/share/cx/accounts/<id>/
  auth.json       canonical credential
  account.json    non-secret identity metadata
  config.toml     file credential-store mode for login/probes
```

`cx` backs up an unmanaged pre-existing `~/.codex/auth.json` by renaming it to `auth.json.cx-backup` on the first switch. It refuses ambiguous/unmanaged states rather than silently overwriting them.

## Quota behavior

Only the longest Codex rate-limit window is shown, which is treated as the weekly window. No 5-hour window is displayed. Reset timestamps are rendered as a full local date/time plus a relative countdown. `LC_TIME`/`LC_ALL` are honored; on macOS `AppleLocale` is used as a system fallback, then `LANG`.

Every dashboard/status refresh starts `codex app-server` under the selected account's credential home, performs the normal initialization handshake, and calls `account/rateLimits/read`. This deliberately leaves token refresh to Codex itself. A quota read is a status request, not a model request; `cx` does not send dummy prompts to consume quota merely to start a rolling window.

## Security model

- no custom OAuth implementation
- no direct refresh-token request
- no auth token output/logging
- account identity stored separately and checked before switching
- atomic symlink replacement
- private files `0600`, directories `0700`

## Development

```sh
go test ./...
go vet ./...
go build ./...
```
