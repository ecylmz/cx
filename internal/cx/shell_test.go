package cx

import (
	"strings"
	"testing"
)

func TestShellInitWrapsCodexWithFileStoreOverride(t *testing.T) {
	out := captureStdout(t, printShellInit)
	for _, want := range []string{
		"unalias codex",
		"command codex -c 'cli_auth_credentials_store=\"file\"' \"$@\"",
		"CX_CODEX_SHELL_INTEGRATION=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("shell-init output missing %q:\n%s", want, out)
		}
	}
}
