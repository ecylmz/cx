package cx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrimeWeeklyWindowErrorsWithoutCodex(t *testing.T) {
	p := makeTestPaths(t)
	t.Setenv("PATH", t.TempDir())
	if err := primeWeeklyWindow(p, Account{ID: "a"}); err == nil {
		t.Fatal("expected missing codex error")
	}
}

func TestPrimeWeeklyWindowPropagatesExecFailure(t *testing.T) {
	p := makeTestPaths(t)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho fail >&2\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := primeWeeklyWindow(p, Account{ID: "a"}); err == nil {
		t.Fatal("expected exec failure")
	}
}
