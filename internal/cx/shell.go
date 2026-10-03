package cx

import "fmt"

// printShellInit emits a tiny interactive-shell wrapper for Codex. Modern Codex
// TUI versions can reuse a long-lived local app-server whose in-memory account
// predates a cx symlink switch. Passing any CLI config override disables that
// implicit daemon reuse; reinforcing the file credential store is a harmless
// override that makes every new TUI read the currently selected auth.json.
func printShellInit(shell string) error {
	switch shell {
	case "bash", "zsh":
		fmt.Print(`unalias codex 2>/dev/null || true
function codex {
  command codex -c 'cli_auth_credentials_store="file"' "$@"
}
export CX_CODEX_SHELL_INTEGRATION=1
`)
	case "fish":
		fmt.Print(`function codex
    command codex -c 'cli_auth_credentials_store="file"' $argv
end
set -gx CX_CODEX_SHELL_INTEGRATION 1
`)
	default:
		return fmt.Errorf("unsupported shell %q; use bash, zsh, or fish", shell)
	}
	return nil
}
