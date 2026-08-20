package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testAuthBytes(t *testing.T, accountID string) []byte {
	t.Helper()
	payload := map[string]any{"email": "x@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID, "chatgpt_plan_type": "plus"}}
	b, _ := json.Marshal(payload)
	tok := "x." + base64.RawURLEncoding.EncodeToString(b) + ".y"
	raw, _ := json.Marshal(map[string]any{"tokens": map[string]any{"id_token": tok, "access_token": "access", "refresh_token": "refresh", "account_id": accountID}})
	return raw
}

func TestSwitchAccountCreatesManagedSymlink(t *testing.T) {
	root := t.TempDir()
	p := paths{Home: root, CodexHome: filepath.Join(root, ".codex"), ConfigRoot: filepath.Join(root, "cfg"), DataRoot: filepath.Join(root, "data"), CacheRoot: filepath.Join(root, "cache")}
	p.AccountsRoot = filepath.Join(p.DataRoot, "accounts")
	if err := p.ensure(); err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "id1", Name: "main", AccountID: "acct"}
	if err := os.MkdirAll(p.accountDir(a.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.accountAuth(a.ID), testAuthBytes(t, "acct"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.sharedAuthPath(), []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := switchAccount(p, a); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("shared auth is not a symlink")
	}
	target, err := os.Readlink(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if target != p.accountAuth(a.ID) {
		t.Fatalf("target=%s", target)
	}
	if _, err := os.Stat(p.sharedAuthPath() + ".cx-backup"); err != nil {
		t.Fatalf("legacy backup missing: %v", err)
	}
}

func TestFetchUsageViaAppServerProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
IFS= read -r init
echo '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}'
IFS= read -r initialized
IFS= read -r request
echo '{"id":2,"result":{"rateLimits":{"primary":{"usedPercent":37,"windowDurationMins":10080,"resetsAt":2000000000},"secondary":null},"rateLimitsByLimitId":null,"rateLimitResetCredits":null}}'
while :; do sleep 1; done
`
	fake := filepath.Join(bin, "codex")
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("PATH")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+old)
	p := paths{Home: root, CodexHome: filepath.Join(root, ".codex"), ConfigRoot: filepath.Join(root, "cfg"), DataRoot: filepath.Join(root, "data"), CacheRoot: filepath.Join(root, "cache")}
	p.AccountsRoot = filepath.Join(p.DataRoot, "accounts")
	if err := p.ensure(); err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "id1", Name: "main", AccountID: "acct"}
	if err := os.MkdirAll(p.accountDir(a.ID), 0700); err != nil {
		t.Fatal(err)
	}
	u, err := fetchUsage(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedPercent != 37 || u.WindowMinutes != 10080 || u.ResetsAt != 2000000000 {
		t.Fatalf("bad usage: %+v", u)
	}
}
