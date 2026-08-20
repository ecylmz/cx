# cx

A small Codex account switcher for macOS and Ubuntu.

`cx` keeps your normal Codex sessions and project state intact while letting you add multiple ChatGPT accounts, switch between them, and see each account's live weekly quota and reset time.

## Install

Requires the Codex CLI. This repository is private, so authenticate GitHub first:

```sh
gh auth login
gh repo clone ecylmz/cx
cd cx
./install.sh
```

`install.sh` installs the latest release for Apple Silicon/Intel macOS and amd64/arm64 Ubuntu. On macOS it also fixes executable permissions and clears the quarantine attribute that can block downloaded binaries.

To build the current checkout instead:

```sh
CX_BUILD_FROM_SOURCE=1 ./install.sh
```

## Use

```sh
cx add primary
cx add backup
cx
```

The dashboard shows the active account, weekly quota remaining, and the full local reset date/time. If a new account's weekly window has not started yet, `cx` sends one minimal ephemeral Codex request to start it.

Useful commands:

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

`cx update` verifies the release checksum before replacing the current binary. For this private repository it uses your authenticated `gh` session, `GH_TOKEN`, or `GITHUB_TOKEN`.

## Release

Push a `v*` tag, or run the **Release** workflow from GitHub Actions and enter a version such as `0.2.0`. Releases contain native macOS/Linux binaries plus `SHA256SUMS`.
