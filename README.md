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

Omarchy ships Codex as a mise-managed lazy launcher in `~/.local/bin`; `cx` works with that launcher directly, so no separate Codex installation path is required. The first `cx` action that invokes Codex may therefore cause Omarchy to install/initialize Codex in the normal way.

## Use

```sh
cx add primary
cx add backup
cx
```

The dashboard shows the active account, both the 5-hour and weekly quota remaining, each window's full local reset date/time, and every available banked reset with its expiration time. It draws cached weekly quota values immediately while refreshing live quota and banked-reset data in parallel. If an account's weekly rolling window has not started yet, `cx` sends one minimal ephemeral Codex request to start it; when the 5-hour window is present, that same turn naturally starts it too. `cx` does not send a priming request merely because an otherwise-unused 5-hour window is not started.

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
