# cx

A small Codex account switcher for macOS and Linux, including Omarchy/Arch and Ubuntu.

`cx` keeps your normal Codex sessions and project state intact while letting you add multiple ChatGPT accounts, switch between them, and see each account's live 5-hour and weekly quotas, reset times, and available banked resets.

![cx status](assets/cx-status.svg)

## Install

Requires the Codex CLI.

```sh
curl -fsSL https://raw.githubusercontent.com/ecylmz/cx/main/install.sh | sh
```

The installer downloads the latest native release for Apple Silicon/Intel macOS or amd64/arm64 Linux, verifies `SHA256SUMS`, and installs `cx` to `~/.local/bin`. Linux support is distribution-independent; Omarchy/Arch and Ubuntu are tested paths. On macOS the installer also fixes executable permissions and clears the quarantine attribute that can block downloaded binaries.

The installer also adds a small shell integration for bash or zsh. It evaluates `cx shell-init`, which wraps bare `codex` launches with the harmless CLI override `cli_auth_credentials_store="file"`. Modern Codex TUI versions otherwise may reconnect to a long-lived local app-server that still holds the account that was active before a cx switch. The override keeps the same `CODEX_HOME`, sessions, history, memories, and configuration, but makes each new TUI launch read the currently selected `auth.json` instead of implicitly reusing stale app-server auth.

If you update an older cx installation with `cx update`, activate the shell integration once in the current shell:

```sh
eval "$(cx shell-init)"
```

For persistence, add that same line to `~/.zshrc` or `~/.bashrc`, or rerun the installer once.

Omarchy ships Codex as a mise-managed lazy launcher in `~/.local/bin`; `cx` works with that launcher directly. Omarchy also reserves the shell alias `cx` for Claude Code. When the installer detects that default alias, it adds an idempotent override to the end of `~/.bashrc` that removes only the `cx` alias, leaving Omarchy's files untouched so the `cx` binary wins.

## Use

```sh
cx add work --expect you@example.com
cx add backup --expect other@example.com
cx
```

Use `--expect EMAIL` when adding or re-authorizing an account if multiple ChatGPT accounts are signed into the browser. After Codex device authorization finishes, cx reads the resulting credential before persisting it. If the authenticated email differs from the expected email, cx rejects the login and leaves the stored account unchanged. If the same ChatGPT identity is already managed by cx, `cx add` also rejects the duplicate instead of silently merging it into another account name.

The installer sets up the first account for you. If `~/.codex/auth.json` already holds a normal ChatGPT login, it is imported as a managed account named `primary` and immediately becomes the active one, so an existing Codex setup keeps working unchanged. If the system has no Codex login yet, the installer adds nothing and tells you to run `cx add NAME`; cx never invents an account. The first account you add yourself keeps the name you give it — `primary` is only the name used for an imported pre-cx login. Rename it any time with `cx rename primary NEW`.

Running the installer again is a no-op here: once cx manages at least one account, `cx init` imports nothing.

The dashboard shows the active account, both the 5-hour and weekly quota remaining, each window's full local reset date/time, and every available banked reset with its expiration time. Cached values appear immediately as `refreshing`, then each account switches to live data as its parallel refresh finishes; the footer shows refresh progress across accounts.

Opening the interactive `cx` dashboard also starts any unused rolling quota window it finds. If either the 5-hour or weekly window is `not started`, `cx` sends one minimal Codex turn for that account, then refreshes its quota and stores the resulting reset timestamps. The same tiny turn starts both windows when both are unused. The turn goes straight to the Codex responses endpoint with the account's own credential, so it needs no `codex` process, writes no session or history files, and costs a handful of tokens. Window-start requests run in parallel with a small concurrency limit, so multiple accounts do not serialize unnecessarily. This intentionally consumes a very small amount of Codex quota in exchange for keeping every account's 5-hour and weekly reset countdowns active when the dashboard is opened. An account with no quota left cannot start a window at all; cx reports that as a single dim `window not started · usage limit reached` note rather than an error.

Account switching is deliberately simple. `cx use NAME` validates the stored credential, atomically changes the shared `~/.codex/auth.json` symlink to the selected managed credential, updates cx state, and returns. It does not start Codex, probe app-server RPCs, restart daemons, kill processes, or alter session/history databases. The shell integration is what keeps subsequent bare `codex` TUI launches from attaching to stale app-server auth.

```sh
cx                                      # dashboard
cx status                               # live status for every account
cx use                                  # interactive account switch
cx use primary                          # atomic auth.json symlink switch
cx init                                 # import an existing Codex login as "primary" (the installer runs this)
cx add backup --expect you@example.com  # add and reject the wrong browser account
cx relogin backup --expect you@example.com
cx doctor                               # verify cx files, symlink and shell integration
cx shell-init                           # print bash/zsh Codex wrapper
cx update                               # install latest release
cx version
```

`cx update` verifies the release checksum before replacing the current binary.

To build from source:

```sh
git clone https://github.com/ecylmz/cx.git
cd cx
CX_BUILD_FROM_SOURCE=1 ./install.sh
```
