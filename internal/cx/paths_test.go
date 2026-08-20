package cx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsHonorsEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "custom-codex"))
	p, err := resolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	if p.CodexHome != filepath.Join(home, "custom-codex") {
		t.Fatalf("CodexHome=%q", p.CodexHome)
	}
	if p.ConfigRoot != filepath.Join(home, "cfg", "cx") || p.DataRoot != filepath.Join(home, "data", "cx") || p.CacheRoot != filepath.Join(home, "cache", "cx") {
		t.Fatalf("unexpected XDG paths: %+v", p)
	}
}

func TestEnsureCreatesPrivateDirectories(t *testing.T) {
	p := makeTestPaths(t)
	for _, dir := range []string{p.ConfigRoot, p.DataRoot, p.CacheRoot, p.AccountsRoot, p.CodexHome} {
		st, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0077 != 0 {
			t.Fatalf("%s permissions=%o", dir, st.Mode().Perm())
		}
	}
	if p.cachePath() != filepath.Join(p.CacheRoot, "status.json") {
		t.Fatalf("cache path=%s", p.cachePath())
	}
}
