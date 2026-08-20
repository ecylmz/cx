package cx

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func makeTestPaths(t *testing.T) paths {
	t.Helper()
	root := t.TempDir()
	p := paths{
		Home:       root,
		CodexHome:  filepath.Join(root, ".codex"),
		ConfigRoot: filepath.Join(root, "config", "cx"),
		DataRoot:   filepath.Join(root, "data", "cx"),
		CacheRoot:  filepath.Join(root, "cache", "cx"),
	}
	p.AccountsRoot = filepath.Join(p.DataRoot, "accounts")
	if err := p.ensure(); err != nil {
		t.Fatal(err)
	}
	return p
}

func writeTestAccount(t *testing.T, p paths, a Account) {
	t.Helper()
	if err := os.MkdirAll(p.accountDir(a.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.accountAuth(a.ID), testAuthBytes(t, a.AccountID), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(b)
}
