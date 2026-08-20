# cx

`cx` is a small, file-based Codex account switcher for macOS and Linux. It keeps Codex sessions, history, memories and configuration in the normal shared `CODEX_HOME`, while each ChatGPT account owns exactly one canonical `auth.json`.

The active `~/.codex/auth.json` is a symlink to the selected account credential. Codex therefore refreshes the canonical credential in place; `cx` never implements OAuth refresh itself and never copies credentials during switching.

## Features

- Add accounts with the official `codex login --device-auth` flow.
- Interactive account selection and atomic symlink switching.
- Shared Codex sessions/memories/history across accounts.
- **Weekly quota only**: live used/remaining percentage, locale-aware full reset date/time, and remaining duration.
- `cx` and `cx status` perform a live `account/rateLimits/read` through `codex app-server` every time.
- If the weekly rolling window has not started, `cx` performs one minimal real Codex turn to start it, then refreshes quota. The turn is ephemeral and is not stored as a session.
- Cached quota is used only as a visibly stale fallback when a live request fails.
- JSON status output for scripting.
- macOS + Ubuntu installer with executable-bit repair and macOS quarantine cleanup.
- `cx update` downloads the latest GitHub release, verifies `SHA256SUMS`, and atomically replaces the running installation.
- `cx use NAME --resume` switches and runs `codex resume --last`.
- Credential identity mismatch protection and `0600`/`0700` storage.

## Install

The downloadable project ZIP includes prebuilt binaries in `dist/`. A Git clone builds from source automatically when you run `./install.sh`. You can also build manually:

```sh
go build -o cx .
install -m 0755 cx ~/.local/bin/cx
```

Requirements: a recent `codex` CLI in `PATH`; `stty` for the interactive dashboard. `install.sh` explicitly supports macOS and Ubuntu on amd64/arm64. On macOS it repairs executable bits, removes `com.apple.quarantine`, and applies an ad-hoc code signature with `codesign` when available. This avoids the common Gatekeeper block on a GitHub-downloaded CLI binary. It is not Apple notarization.

Because this repository is private, release installation/update needs either an authenticated GitHub CLI (`gh auth login`) or `GH_TOKEN`/`GITHUB_TOKEN`. If the repository becomes public, `cx update` also works without authentication.

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
cx                         live weekly quota dashboard; starts unused window
cx status [NAME] [--json] live weekly quota status; starts unused window
cx add [NAME]             add via device auth
cx relogin NAME           safely replace one credential
cx use [NAME] [--resume]  switch; interactive without NAME
cx current
cx list
cx rename OLD NEW
cx remove NAME
cx doctor
cx update [--force]
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

Every dashboard/status refresh starts `codex app-server` under each account's credential home, performs the normal initialization handshake, and calls `account/rateLimits/read`. This deliberately leaves token refresh to Codex itself.

If an unused weekly window is reported as `100% left` with the moving `now + 7 days` reset placeholder, `cx` intentionally starts the window. It runs one real Codex request using `codex exec --ephemeral --ignore-user-config --ignore-rules --sandbox read-only` in an empty temporary directory and asks only for `OK`. After that request completes, `cx` reads quota again and stores the resulting reset timestamp as non-secret account metadata so repeated status checks do not prime the same active window again. This request consumes a small amount of Codex quota by design.

## Updating

```sh
cx update
```

`cx update` reads the latest GitHub release, selects the current OS/architecture asset, downloads `SHA256SUMS`, verifies the binary, writes a temporary executable next to the current installation, and atomically renames it into place. On macOS it also clears the quarantine xattr and applies an ad-hoc `codesign` signature to the replacement binary when available. Use `cx update --force` to reinstall the current release.

## GitHub Actions

- `CI`: runs tests, vet, build, installer syntax checks, and an installer smoke test on Ubuntu and macOS.
- `Release`: tags matching `v*` build macOS amd64/arm64 and Linux amd64/arm64 binaries, generate `SHA256SUMS`, and publish a GitHub Release.

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
