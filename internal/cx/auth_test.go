package cx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJWTAndParseAuthFailures(t *testing.T) {
	if _, err := jwtClaims("bad"); err == nil {
		t.Fatal("expected invalid JWT")
	}
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"tokens":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseAuth(p); err == nil {
		t.Fatal("expected missing-token error")
	}
}

func TestEnsureFileStoreConfigAddsReplacesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := "model = \"x\"\ncli_auth_credentials_store = \"keyring\"\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureFileStoreConfig(path, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `cli_auth_credentials_store = "file"`) || strings.Contains(string(b), "keyring") {
		t.Fatalf("config=%s", b)
	}
	bak, err := os.ReadFile(path + ".cx-backup")
	if err != nil || string(bak) != original {
		t.Fatalf("backup=%q err=%v", bak, err)
	}
	if err := ensureFileStoreConfig(path, true); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(dir, "new.toml")
	if err := ensureFileStoreConfig(missing, false); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(missing)
	if string(b) != "cli_auth_credentials_store = \"file\"\n" {
		t.Fatalf("new config=%q", b)
	}
}

func TestInstallAuthValidatesJSON(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installAuth(src, dst); err == nil {
		t.Fatal("expected invalid JSON")
	}
	if err := os.WriteFile(src, []byte(`{"tokens":{"access_token":"x"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installAuth(src, dst); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := readJSON(dst, &got); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchAccountRejectsIdentityMismatchAndUnmanagedSymlink(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "alpha", AccountID: "expected", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := os.MkdirAll(p.accountDir(a.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.accountAuth(a.ID), testAuthBytes(t, "other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		t.Fatal(err)
	}
	if err := switchAccount(p, a); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("expected identity mismatch, got %v", err)
	}

	if err := os.WriteFile(p.accountAuth(a.ID), testAuthBytes(t, "expected"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(p.Home, "outside-auth")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p.sharedAuthPath()); err != nil {
		t.Fatal(err)
	}
	if err := switchAccount(p, a); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("expected unmanaged symlink rejection, got %v", err)
	}
}

func TestParseAuthUsesTokenAccountIDFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	raw := testAuthBytes(t, "acct")
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	id, err := parseAuth(p)
	if err != nil || id.AccountID != "acct" || id.Email != "x@example.com" || id.Plan != "plus" {
		t.Fatalf("id=%+v err=%v", id, err)
	}
}
