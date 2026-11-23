# cx

A small account switcher for Codex and Antigravity on macOS and Linux.

Switch between accounts and see live quota and reset times in one dashboard.

![cx dashboard](assets/cx-dashboard.svg)

## Install

Install the CLI for each provider you use. Antigravity support requires `agy` 1.5.14 or later.

```sh
curl -fsSL https://raw.githubusercontent.com/ecylmz/cx/main/install.sh | sh
```

This installs `cx` to `~/.local/bin` and imports your existing Codex login as an account named `primary` (if you have one). It leaves your shell startup files alone, with one exception: Omarchy reserves `cx` for Claude Code, so on an Omarchy machine the installer appends a small block to `~/.bashrc` that drops that alias — and says so when it does.

### Shell integration (optional)

Recent Codex versions can keep a local app-server alive between sessions. While one is running, a bare `codex` may reattach to it and keep serving the account that was active when it started — so a switch you just made with `cx use` looks like it did nothing.

If you see that, add this to `~/.zshrc` or `~/.bashrc` for Zsh or Bash:

```sh
eval "$(cx shell-init)"
```

For Fish, add this to `~/.config/fish/config.fish`:

```fish
cx shell-init | source
```

When you omit the shell argument, `cx shell-init` checks the process that started it, then `$SHELL`. If neither identifies Bash, Zsh, or Fish, it uses Bash/Zsh syntax. Pass `bash`, `zsh`, or `fish` to select the output explicitly, for example `cx shell-init fish | source`.

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
cx auto         # choose an account by available quota
cx run          # start the agent for the selected account
```

That's it for daily use. Everything else:

```sh
cx status [NAME] [--json]   # live quota status for every account
cx current                  # show the account for cx run
cx list                     # list accounts, no network
cx rename OLD NEW
cx remove NAME
cx relogin NAME             # refresh one account's credential
cx doctor                   # verify files, symlink and credentials
cx update                   # install latest release
cx version
```

### Antigravity

If you already use Antigravity, run `cx agy init` to register that login as `primary`. This profile uses your existing Antigravity configuration. To choose a different name, run `cx agy init NAME`.

To add another Google account:

1. In an interactive terminal, run `cx agy add work`. Use a different name for each account.
2. Open the Google sign-in URL that `agy` prints. Select the Google account you want to add.
3. If Google gives you an authorization code, paste it into the same terminal.

Each new profile has its own home directory, credentials, configuration, and conversation history.

```sh
cx list               # list registered accounts
cx use agy/work       # select work
cx use agy/           # pick an Antigravity account
cx status agy/work    # show quota by model group
cx run                # start the agent for the selected account
```

Use `cx agy run` to start the last selected Antigravity profile directly. Use `cx agy help` to list the Antigravity commands.

The dashboard lists one provider's accounts directly. If you use more than one provider, it opens groups in alphabetical order by provider name. A green dot marks each provider's active account. Press Enter on an account to activate it for that provider.

Press `d` on a group or one of its accounts to set the default provider for `cx run` and `cx auto`. The group heading shows `default`.

Press the left arrow to fold the group under the cursor. The folded heading shows the active account. Press the right arrow or Enter to open the group.

The dashboard shows one row per Antigravity model group. The columns show the remaining 5-hour and weekly quotas. A wide terminal also shows the time until reset. Run `cx status agy/NAME` for the full quota details.

Use `agy/NAME` or `codex/NAME` to select an account by provider. A name without a prefix selects a Codex account. The same prefixes work with `cx status`, `cx rename`, and `cx remove`.

### Adding the right account

If several ChatGPT accounts are signed into your browser, `--expect` is an optional safety check:

```sh
cx add work --expect you@example.com
```

cx verifies the email that device auth actually returned and refuses to save the credential if it doesn't match. It also rejects adding an account that cx already manages under another name.

### Automatic selection

`cx auto` starts with the default provider. It keeps that provider's active account while it has quota. If necessary, cx looks for another account from the same provider. It tries the other provider only if the first provider has no usable account.

Codex requires quota in every reported window. Antigravity requires quota in every reported window of at least one model group. For each usable account, cx ranks its best available group by weekly quota and then by 5-hour quota. It selects the account with the least remaining quota.

If cx cannot read an account's quota, it skips that account. If no account qualifies, cx keeps the selection and exits with status 1.

### Quota windows

Opening the dashboard starts unused Codex quota windows with one minimal request per account. This uses a few tokens. Antigravity quota reads and `cx auto` use read-only quota checks.

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
