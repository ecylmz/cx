package cx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellInit(t *testing.T) {
	bin := t.TempDir()
	binary := filepath.Join(bin, "cx")
	if out, err := exec.Command("go", "build", "-o", binary, "../../cmd/cx").CombinedOutput(); err != nil {
		t.Fatalf("build cx: %v\n%s", err, out)
	}
	p := makeTestPaths(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(p.ConfigRoot))
	t.Setenv("XDG_DATA_HOME", filepath.Dir(p.DataRoot))
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(p.CacheRoot))
	t.Setenv("CODEX_HOME", p.CodexHome)
	t.Setenv("CX_CODEX_SHELL_INTEGRATION", "")
	t.Setenv("ZDOTDIR", t.TempDir())
	stub := "#!/bin/sh\nprintf '%s\\000' \"$CX_CODEX_SHELL_INTEGRATION\" \"$@\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, explicit := range []bool{false, true} {
			name := shell + "/auto"
			init := "cx shell-init"
			if explicit {
				name = shell + "/explicit"
				init += " " + shell
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("SHELL", "/bin/"+shell)
				initArgs := []string{"shell-init"}
				if explicit {
					initArgs = append(initArgs, shell)
				}
				out, err := exec.Command(binary, initArgs...).CombinedOutput()
				if err != nil {
					t.Fatalf("shell-init: %v\n%s", err, out)
				}
				if !strings.Contains(string(out), `command codex -c 'cli_auth_credentials_store="file"'`) || !strings.Contains(string(out), "CX_CODEX_SHELL_INTEGRATION") {
					t.Fatalf("shell-init output missing the override or integration marker:\n%s", out)
				}

				path, err := exec.LookPath(shell)
				if err != nil {
					t.Skipf("%s not installed; output checked without executing it", shell)
				}
				script := "alias codex='false'\neval \"$(" + init + ")\"\ncodex \"$@\""
				args := []string{"--noprofile", "--norc", "-c", script, "cx-test"}
				if shell == "zsh" {
					args = []string{"-f", "-c", script, "cx-test"}
				} else if shell == "fish" {
					args = []string{"--no-config", "-c", "function codex; false; end\n" + init + " | source\ncodex $argv"}
				}
				forwarded := []string{"hello world", "", "*", "quoted\"'", "line\nbreak"}
				cmd := exec.Command(path, append(args, forwarded...)...)
				// The caller must win over an inherited login-shell value.
				login := "/bin/fish"
				if shell == "fish" {
					login = "/bin/bash"
				}
				cmd.Env = append(os.Environ(), "SHELL="+login)
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

	t.Run("explicit-overrides-caller", func(t *testing.T) {
		path, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash not installed")
		}
		t.Setenv("SHELL", "/bin/zsh")
		out, err := exec.Command(path, "--noprofile", "--norc", "-c", "cx shell-init fish\n:").CombinedOutput()
		if err != nil || !strings.HasPrefix(string(out), "function codex\n") {
			t.Fatalf("explicit Fish selection: %v\n%s", err, out)
		}
	})

	failedPS := t.TempDir()
	if err := os.WriteFile(filepath.Join(failedPS, "ps"), []byte("#!/bin/sh\nprintf 'fish\\n'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, shell, path, prefix string
	}{
		{"environment", "/usr/local/bin/fish", "", "function codex\n"},
		{"login-name", "-fish", "", "function codex\n"},
		{"missing-ps", "/bin/fish", bin, "function codex\n"},
		{"failed-ps", "/bin/bash", failedPS, "unalias codex"},
		{"unknown-environment", "/bin/sh", "", "unalias codex"},
		{"empty-environment", "", "", "unalias codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHELL", tc.shell)
			if tc.path != "" {
				t.Setenv("PATH", tc.path)
			}
			out, err := exec.Command(binary, "shell-init").CombinedOutput()
			if err != nil || !strings.HasPrefix(string(out), tc.prefix) {
				t.Fatalf("shell detection: %v\n%s", err, out)
			}
		})
	}
}
