package cx

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestFetchUsageWithPrimingStartsWindowOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "primed-reset")
	count := filepath.Join(root, "prime-count")
	argsLog := filepath.Join(root, "prime-args")
	script := `#!/bin/sh
case "$1" in
  app-server)
    if [ -f "$CX_TEST_MARKER" ]; then
      reset=$(cat "$CX_TEST_MARKER")
    else
      now=$(date +%s)
      reset=$((now + 604800))
    fi
    IFS= read -r init
    echo '{"id":1,"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}'
    IFS= read -r initialized
    IFS= read -r request
    echo "{\"id\":2,\"result\":{\"rateLimits\":{\"primary\":{\"usedPercent\":0,\"windowDurationMins\":10080,\"resetsAt\":$reset},\"secondary\":null},\"rateLimitsByLimitId\":null,\"rateLimitResetCredits\":null}}"
    while :; do sleep 1; done
    ;;
  exec)
    now=$(date +%s)
    echo $((now + 604800)) > "$CX_TEST_MARKER"
    n=0
    [ -f "$CX_TEST_COUNT" ] && n=$(cat "$CX_TEST_COUNT")
    echo $((n + 1)) > "$CX_TEST_COUNT"
    printf '%s\n' "$*" > "$CX_TEST_ARGS"
    echo OK
    ;;
  *) exit 2 ;;
esac
`
	fake := filepath.Join(bin, "codex")
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CX_TEST_MARKER", marker)
	t.Setenv("CX_TEST_COUNT", count)
	t.Setenv("CX_TEST_ARGS", argsLog)

	p := paths{Home: root, CodexHome: filepath.Join(root, ".codex"), ConfigRoot: filepath.Join(root, "cfg"), DataRoot: filepath.Join(root, "data"), CacheRoot: filepath.Join(root, "cache")}
	p.AccountsRoot = filepath.Join(p.DataRoot, "accounts")
	if err := p.ensure(); err != nil {
		t.Fatal(err)
	}
	a := Account{ID: "id1", Name: "backup", AccountID: "acct", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := os.MkdirAll(p.accountDir(a.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		t.Fatal(err)
	}

	rs := fetchAllUsageWithPriming(p, []Account{a})
	if len(rs) != 1 || rs[0].Err != "" || rs[0].PrimeErr != "" {
		t.Fatalf("unexpected result: %+v", rs)
	}
	if !rs[0].Usage.WindowStarted {
		t.Fatal("weekly window should be marked started after real turn")
	}
	b, err := os.ReadFile(count)
	if err != nil || strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("prime count=%q err=%v", b, err)
	}
	args, _ := os.ReadFile(argsLog)
	for _, want := range []string{"exec", "--ephemeral", "--ignore-user-config", "--sandbox read-only"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("prime args missing %q: %s", want, args)
		}
	}

	accounts, err := listAccounts(p)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts: %+v err=%v", accounts, err)
	}
	rs = fetchAllUsageWithPriming(p, accounts)
	if rs[0].PrimeErr != "" || !rs[0].Usage.WindowStarted {
		t.Fatalf("second result: %+v", rs[0])
	}
	b, _ = os.ReadFile(count)
	if strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("window was primed more than once: %s", b)
	}
}
