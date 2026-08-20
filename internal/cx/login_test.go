package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func installFakeLoginCodex(t *testing.T, authSource string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "codex-test 1.0"; exit 0; fi
if [ "$1" = "login" ] && [ "$2" = "--device-auth" ]; then
  mkdir -p "$CODEX_HOME"
  cp "$CX_TEST_AUTH_SOURCE" "$CODEX_HOME/auth.json"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CX_TEST_AUTH_SOURCE", authSource)
}

func TestRunDeviceAuthAndAddAccount(t *testing.T) {
	p := makeTestPaths(t)
	authSource := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authSource, testAuthBytes(t, "acct-1"), 0600); err != nil {
		t.Fatal(err)
	}
	installFakeLoginCodex(t, authSource)

	home := filepath.Join(t.TempDir(), "login-home")
	if err := runDeviceAuth(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if !strings.Contains(string(cfg), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("config=%s", cfg)
	}

	a, err := addAccount(p, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "primary" || a.AccountID != "acct-1" {
		t.Fatalf("account=%+v", a)
	}
	st, err := loadState(p)
	if err != nil || st.ActiveID != a.ID {
		t.Fatalf("state=%+v err=%v", st, err)
	}
	info, err := os.Lstat(p.sharedAuthPath())
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("active auth not symlink: %v %v", info, err)
	}

	again, err := addAccount(p, "ignored-name")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != a.ID {
		t.Fatalf("duplicate account created: %s != %s", again.ID, a.ID)
	}
	accounts, _ := listAccounts(p)
	if len(accounts) != 1 {
		t.Fatalf("accounts=%+v", accounts)
	}
}

func TestAddAccountAutoNamesCollisions(t *testing.T) {
	p := makeTestPaths(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(source, testAuthBytes(t, "acct-1"), 0600); err != nil {
		t.Fatal(err)
	}
	installFakeLoginCodex(t, source)
	a, err := addAccount(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "x" {
		t.Fatalf("auto name=%q", a.Name)
	}
	if err := os.WriteFile(source, testAuthBytes(t, "acct-2"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := addAccount(p, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "x-2" {
		t.Fatalf("collision name=%q", b.Name)
	}
}

func TestReloginRequiresSameAccount(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct-1"}
	writeTestAccount(t, p, a)
	source := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(source, testAuthBytes(t, "acct-2"), 0600); err != nil {
		t.Fatal(err)
	}
	installFakeLoginCodex(t, source)
	if _, err := reloginAccount(p, "primary"); err == nil || !strings.Contains(err.Error(), "account mismatch") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if err := os.WriteFile(source, testAuthBytes(t, "acct-1"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := reloginAccount(p, "primary")
	if err != nil || got.AccountID != "acct-1" {
		t.Fatalf("relogin=%+v err=%v", got, err)
	}
}

func TestRunDeviceAuthWithoutCodex(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := runDeviceAuth(filepath.Join(t.TempDir(), "home")); err == nil {
		t.Fatal("expected missing codex error")
	}
}
