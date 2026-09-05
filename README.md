# cx

A small Codex account switcher for macOS and Linux.

Add multiple ChatGPT accounts, switch between them, and see each account's live 5-hour and weekly quotas with reset times — without touching your Codex sessions, history, or config.

![cx dashboard](assets/cx-dashboard.svg)

## Install

Requires the Codex CLI.

```sh
curl -fsSL https://raw.githubusercontent.com/ecylmz/cx/main/install.sh | sh
```

This installs `cx` to `~/.local/bin` and imports your existing Codex login as an account named `primary` (if you have one). It leaves your shell startup files alone, with one exception: Omarchy reserves `cx` for Claude Code, so on an Omarchy machine the installer appends a small block to `~/.bashrc` that drops that alias — and says so when it does.

### Shell integration (optional)

Recent Codex versions can keep a local app-server alive between sessions. While one is running, a bare `codex` may reattach to it and keep serving the account that was active when it started — so a switch you just made with `cx use` looks like it did nothing.

If you see that, add this to `~/.zshrc` or `~/.bashrc`:

```sh
eval "$(cx shell-init)"
```

It defines a small `codex` shell function that passes one config override, which turns that reuse off so every new session reads the account cx selected. You can also pass the override by hand on the one run where it matters, instead of keeping a wrapper around:

```sh
codex -c 'cli_auth_credentials_store="file"'
```

Most setups never hit the stale-session case, so cx does not install this for you and `cx doctor` does not complain when it is missing. Nothing else in cx depends on it — every switch already writes the credential store setting into `~/.codex/config.toml` and repoints `auth.json`.

## Use

```sh
cx              # dashboard: pick an account, see quotas
cx add work     # add another account via Codex device auth
cx use work     # switch account
cx auto         # switch only if the current account ran out of quota
```

That's it for daily use. Everything else:

```sh
cx status [NAME] [--json]   # live quota status for every account
cx current                  # show active account
cx list                     # list accounts, no network
cx rename OLD NEW
cx remove NAME
cx relogin NAME             # refresh one account's credential
cx doctor                   # verify files, symlink and credentials
cx update                   # install latest release
cx version
```

### Adding the right account

If several ChatGPT accounts are signed into your browser, `--expect` is an optional safety check:

```sh
cx add work --expect you@example.com
```

cx verifies the email that device auth actually returned and refuses to save the credential if it doesn't match. It also rejects adding an account that cx already manages under another name.

### Automatic selection

`cx auto` keeps the active account as long as it has both 5-hour and weekly quota left. Once either runs out, it switches to the usable account with the *least* weekly quota remaining — spending the nearly-empty accounts first and keeping the fresh ones in reserve. It exits with status 1 without switching if every account is exhausted.

### Quota windows

Opening the dashboard starts any rolling quota window that hasn't started yet, by sending one minimal Codex request per account. This spends a handful of tokens so your 5-hour and weekly reset countdowns stay active. Accounts with no quota left are shown as `window not started · usage limit reached`. (`cx auto` never does this — it only reads.)

### Banked resets

Accounts with banked rate-limit resets get a third meter line. Its bar is a fixed 30-day axis — the life of a banked reset — with a tick at each week, and every reset sits at the date it expires:

```
banked  ─────┼●───┼────┼────┼─       1 left   next in 8d 23h
```

The scale is the same on every account, so the lines can be read against each other. A pip turns yellow inside a week of expiring and red inside a day; a cell holding more than one reset is drawn `◉`. A reset the backend gives no expiry date for cannot be placed on the axis, so the count says so instead — `2 left · 1?` is two banked resets, one of them undated. Press `b` in the dashboard for the exact dates of every account, or run `cx status`, which always lists them in full.

## Build from source

```sh
git clone https://github.com/ecylmz/cx.git
cd cx
CX_BUILD_FROM_SOURCE=1 ./install.sh
```
