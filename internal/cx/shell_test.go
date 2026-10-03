package cx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellInitWrapsCodexWithFileStoreOverride(t *testing.T) {
	p := makeTestPaths(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(p.ConfigRoot))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(p.DataRoot))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(p.CacheRoot))
	t.Setenv("CODEX_HOME", p.CodexHome)
	t.Setenv("CX_CODEX_SHELL_INTEGRATION", "")
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	bin := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\000' \"$CX_CODEX_SHELL_INTEGRATION\" \"$@\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, shell := range []string{"", "bash", "zsh", "fish"} {
		name := shell
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			os.Args = []string{"cx", "shell-init"}
			if shell != "" {
				os.Args = append(os.Args, shell)
			}
			out := captureStdout(t, Main)
			if !strings.Contains(out, `command codex -c 'cli_auth_credentials_store="file"'`) || !strings.Contains(out, "CX_CODEX_SHELL_INTEGRATION") {
				t.Fatalf("shell-init output missing the override or integration marker:\n%s", out)
			}

			executable := shell
			if executable == "" {
				executable = "bash"
			}
			path, err := exec.LookPath(executable)
			if err != nil {
				t.Skipf("%s not installed; output checked without executing it", executable)
			}
			args := []string{"--noprofile", "--norc", "-c", "alias codex='false'\nsource /dev/stdin\ncodex \"$@\"", "cx-test"}
			if shell == "zsh" {
				args = []string{"-f", "-c", "alias codex='false'\nsource /dev/stdin\ncodex \"$@\"", "cx-test"}
			} else if shell == "fish" {
				args = []string{"--no-config", "-c", "function codex; false; end\nsource\ncodex $argv"}
			}
			forwarded := []string{"hello world", "", "*", "quoted\"'", "line\nbreak"}
			cmd := exec.Command(path, append(args, forwarded...)...)
			cmd.Stdin = strings.NewReader(out)
			got, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
				t.Fatalf("wrapper did not preserve the Codex exit status: %v\n%s", err, got)
			}
			want := strings.Join(append([]string{"1", "-c", `cli_auth_credentials_store="file"`}, forwarded...), "\000") + "\000"
			if string(got) != want {
				t.Fatalf("wrapper arguments or exported marker differ:\ngot  %q\nwant %q", got, want)
			}
		})
	}
}
