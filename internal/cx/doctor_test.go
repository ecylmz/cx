package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorHealthyAndMissingCodex(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-test 1.0'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := switchAccount(p, a); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := doctor(p); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"codex-test 1.0", "credential store: file", "accounts: 1", "active auth symlink: managed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}

	t.Setenv("PATH", t.TempDir())
	if err := doctor(p); err == nil {
		t.Fatal("expected doctor failure without codex")
	}
}
