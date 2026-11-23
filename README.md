# cx

A small account switcher for Codex and Antigravity on macOS and Linux.

Switch between accounts and see live quota and reset times in one dashboard.

![cx dashboard with aligned quota columns for Antigravity and Codex](assets/cx-dashboard.svg)

## Install

Install the CLI for each provider you use. Antigravity support requires `agy` 1.5.14 or later.

```sh
curl -fsSL https://raw.githubusercontent.com/ecylmz/cx/main/install.sh | sh
```

The installer puts `cx` in `~/.local/bin`. If you have a Codex login, it imports that login as `primary`. On Omarchy, the installer updates `~/.bashrc` to remove the `cx` alias for Claude Code.

### Shell integration (optional)

Codex can reuse a local app server between sessions. If `codex` reconnects to an existing server after `cx use`, it can keep using the previous account.

If this happens, add the shell integration to `~/.zshrc` or `~/.bashrc`.

```sh
eval "$(cx shell-init)"
```

For Fish, add the shell integration to `~/.config/fish/config.fish`.

```fish
cx shell-init | source
```

If you omit the shell argument, `cx shell-init` looks at the process that started it, then at `$SHELL`. If neither identifies Bash, Zsh, or Fish, cx uses Bash or Zsh syntax. Pass `bash`, `zsh`, or `fish` to select the syntax.

The shell function disables app server reuse so each new session reads the account that cx selected. You can also pass the same setting on a single run.

```sh
codex -c 'cli_auth_credentials_store="file"'
```

Each Codex account switch uses `$CODEX_HOME` or `~/.codex` if it is unset. cx sets the file credential store in `config.toml` and links `auth.json` to the selected account. `cx run` applies the selection in that directory before it starts Codex.

## Use

```sh
cx              # open the dashboard
cx add work     # add another account via Codex device auth
cx use work     # activate work and set its subscription as the default
cx auto         # choose an account by available quota
cx run          # start the provider CLI with the default account
```

### Command reference

```sh
cx add [NAME] [--provider codex] [--subscription TIER] [--expect EMAIL]
cx add NAME --provider agy [--subscription TIER]
cx init [--provider codex] [--subscription TIER]
cx init [NAME] --provider agy [--subscription TIER]
cx use [NAME] [--provider PROVIDER] [--subscription TIER] [--resume]
cx auto
cx run [--provider PROVIDER] [ARGS...]
cx status [NAME] [--provider PROVIDER] [--subscription TIER] [--json]
cx current
cx list
cx rename OLD NEW [--provider PROVIDER]
cx remove NAME [--provider PROVIDER]
cx relogin NAME [--provider codex] [--expect EMAIL]
cx doctor
cx shell-init [bash|zsh|fish]
cx update [--force]
cx version
```

### Dashboard

The dashboard groups accounts by subscription. If you have one subscription, the dashboard shows a single list. If you have more than one, groups start open and appear in alphabetical order. A cyan bar marks the selected account row. On other rows, a green dot marks the active account for its provider.

Each account uses one row by default. The row shows the email address and the quota for Codex or Gemini Models. If the email address is unavailable, the row shows the account name. A blank line separates groups and follows each open group heading. The table shows one set of `5 hour`, `weekly`, and `banked` headings. The `↻` symbol marks the reset time.

Press `m` on an Antigravity account to show its other model groups below the account row. Press `m` again to hide them.

Press `Tab` to move to the next subscription. After the last subscription, `Tab` returns to the first.

Press `Enter` on an account to activate it for its provider. Press `←` to fold the focused group. The folded heading shows its active account. Press `→` or `Enter` to open the group.

Press `s` on an account to open its subscription menu. Select a tier and press `Enter` to save it in cx. Press `Esc` to cancel.

With multiple subscriptions, press `d` to set the focused subscription as the default. That subscription supplies the account for `cx run`. It is also the first subscription that `cx auto` tries. Press `?` for all shortcuts.

### Selecting accounts from the command line

If an account name is unique, `cx use NAME` activates it and sets its subscription as the default. Add `--resume` to resume the selected account's last conversation.

If the name matches more than one provider, add `--provider codex` or `--provider agy`. The same rule applies to `cx status`, `cx rename`, and `cx remove`. Before you remove an active account, switch to another account from the same provider.

Run `cx use` to open the account picker. Add `--provider` or `--subscription` to filter the picker or `cx status`.

Run `cx status` to see all accounts. For Antigravity accounts only, run `cx status --provider agy`.

`cx list` marks the account for `cx run` with `*`. `cx status` marks that account with a green dot. In JSON output, that account has `"active": true`.

### Subscription tiers

A subscription combines a provider and a tier. For example, `Codex Plus` combines the provider `Codex` and the tier `Plus`. Each account belongs to one subscription. See the [dictionary](DICT.md).

New Codex accounts default to Plus. New Antigravity accounts default to Pro. Use `--subscription` to choose another tier, such as `pro-100` for Codex or `ultra-100` for Antigravity.

If you specify a tier and sign-in reports a different one, cx reports the mismatch before it saves the account. Some sign-ins provide limited subscription information.

See [Codex pricing](https://chatgpt.com/codex/pricing/), [ChatGPT Pro tiers](https://help.openai.com/en/articles/9793128-about-chatgpt-pro-tiers), [Antigravity plans](https://antigravity.google/docs/plans/), and [Antigravity for Gemini Enterprise](https://antigravity.google/docs/enterprise/) for plan details.

### Antigravity

Antigravity account switching uses the login Keychain on macOS and Secret Service on Linux. On Linux, make sure that `secret-tool` (from libsecret) is installed and that your keyring is unlocked.

If you already use Antigravity, run `cx init --provider agy` to register your current login as `primary`. For a different name, run `cx init NAME --provider agy`.

To add another Google account, follow these steps.

1. In an interactive terminal, run `cx add work --provider agy`. Use a different name for each Antigravity account.
2. Open the Google sign-in URL that `agy` prints. Select the Google account you want to add.
3. If Google gives you an authorization code, paste it into the same terminal.

Each profile keeps a copy of its credentials for account switching and quota requests. After you sign in, cx activates the new account and makes its subscription the default.

Close active Antigravity sessions before you switch. The system keyring entry is shared with other Antigravity apps on the same machine.

```sh
cx list                          # list registered accounts
cx use work --provider agy       # activate work and make its subscription the default
agy                              # start Antigravity with the active login
cx use --provider agy            # open the picker for Antigravity accounts
cx status work --provider agy    # show quota by model group
```

Each new `agy` session uses your usual Antigravity configuration and conversation history.

The dashboard reads the Google email from the local Antigravity profile when it is available. Run `cx status NAME --provider agy` for the full quota details.

### Adding the right account

If you use several ChatGPT accounts in your browser, pass the email address of the account you want to add.

```sh
cx add work --expect you@example.com
```

If sign-in returns a different email address, cx rejects the credential. cx also rejects an account it already manages under another name.

### Automatic selection

`cx auto` starts with the default subscription. It keeps the active account if that account qualifies. If the active account lacks usable quota, cx tries the other accounts in that subscription first. If none qualify, cx tries other subscriptions. A switch makes the new account's subscription the default.

Codex accounts need quota left in every reported window. Antigravity accounts need quota left in every reported window of at least one model group.

For Antigravity, cx uses the usable model group with the most quota left for each account. Among the accounts that qualify, cx selects the account with the least quota left. Both comparisons use weekly quota first and 5-hour quota to break ties.

If cx cannot read an account's quota, it skips that account. If no account qualifies, cx keeps the selection and exits with status 1.

### Quota windows

The dashboard and `cx status` start unused Codex quota windows with a minimal request. This uses a few tokens. Antigravity quota reads and `cx auto` use read-only quota checks.

### Banked resets

The `banked` column shows how many Codex resets remain. If there is room, it shows the earliest known expiry date and time. A wide terminal also shows a 30-day timeline. If any resets remain, press `b` for the full dates. `cx status` also lists them.

```
banked  ─────┼●───┼────┼────┼─       1 left   next in 8d 23h
```

The timeline uses the same scale across Codex accounts. A mark turns yellow within a week of expiry and red within a day. A cell with more than one reset shows `◉`. In full output, `2 left · 1?` means that one of two resets has an unknown expiry date.

## Build from source

```sh
git clone https://github.com/ecylmz/cx.git
cd cx
CX_BUILD_FROM_SOURCE=1 ./install.sh
```
