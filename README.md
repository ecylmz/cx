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

Omarchy ships Codex as a mise-managed lazy launcher in `~/.local/bin`; `cx` works with that launcher directly, so no separate Codex installation path is required. Omarchy also reserves the shell alias `cx` for Claude Code. When the installer detects that default Omarchy alias, it adds an idempotent override to the end of `~/.bashrc` that removes only the `cx` alias, leaving Omarchy's files untouched so the `cx` binary wins. Open a new shell or run `source ~/.bashrc` after installation. If `cx` was installed on Omarchy before this support was added, rerun the installer once; `cx update` only replaces the binary and does not modify shell configuration.

## Use

```sh
cx add primary
cx add backup
cx
```

The dashboard shows the active account, both the 5-hour and weekly quota remaining, each window's full local reset date/time, and every available banked reset with its expiration time. Cached values appear immediately as `refreshing`, then each account switches to live data as its parallel refresh finishes; the footer shows refresh progress across accounts.

If an account's weekly rolling window has not started yet, `cx` sends one minimal ephemeral Codex request to start it; when the 5-hour window is present, that same turn naturally starts it too. A 5-hour window can still show `not started · starts with Codex use` when the weekly window is already active: `cx` deliberately does not spend a model turn merely to start an otherwise-unused 5-hour window. Actual Codex use starts that rolling window.

```sh
cx                    # dashboard
cx status             # live status for every account
cx use                 # interactive account switch
cx use primary         # switch directly
cx add backup          # add with device auth
cx relogin backup      # log in again
cx update              # install latest release
cx version
```

`cx update` verifies the release checksum before replacing the current binary.

To build from source:

```sh
git clone https://github.com/ecylmz/cx.git
cd cx
CX_BUILD_FROM_SOURCE=1 ./install.sh
```
