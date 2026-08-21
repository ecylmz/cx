# cx

A small Codex account switcher for macOS and Ubuntu.

`cx` keeps your normal Codex sessions and project state intact while letting you add multiple ChatGPT accounts, switch between them, and see each account's live weekly quota, reset time, and available banked resets.

![cx status](assets/cx-status.svg)

## Install

Requires the Codex CLI.

```sh
curl -fsSL https://raw.githubusercontent.com/ecylmz/cx/main/install.sh | sh
```

The installer downloads the latest native release for Apple Silicon/Intel macOS or amd64/arm64 Ubuntu, verifies `SHA256SUMS`, and installs `cx` to `~/.local/bin`. On macOS it also fixes executable permissions and clears the quarantine attribute that can block downloaded binaries.

## Use

```sh
cx add primary
cx add backup
cx
```

The dashboard shows the active account, weekly quota remaining, the full local reset date/time, and every available banked reset with its expiration time. It draws cached quota values immediately while refreshing live quota and banked-reset data in parallel. If a new account's weekly window has not started yet, `cx` sends one minimal ephemeral Codex request to start it.

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
