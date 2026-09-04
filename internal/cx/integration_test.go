package cx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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
	p := makeTestPaths(t)
	a := Account{ID: "id1", Name: "backup", AccountID: "acct", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)

	var mu sync.Mutex
	primes := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/usage", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		started := primes > 0
		mu.Unlock()
		// An unused rolling window reports zero use and a reset one full
		// window ahead; a started one reports real use.
		used, reset := 0, time.Now().Add(7*24*time.Hour).Unix()
		if started {
			used, reset = 3, time.Now().Add(5*24*time.Hour).Unix()
		}
		fmt.Fprintf(w, `{"rate_limit":{"secondary_window":{"used_percent":%d,"limit_window_seconds":604800,"reset_at":%d}}}`, used, reset)
	})
	mux.HandleFunc("/reset-credits", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"credits":[]}`))
	})
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		primes++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-mini","visibility":"list","priority":20,"supported_in_api":true}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	oldUsage, oldCredits, oldClient := directUsageEndpoint, directResetCreditsEndpoint, directUsageHTTPClient
	oldResponses, oldModels, oldPrimeClient := directResponsesEndpoint, directModelsEndpoint, primeHTTPClient
	directUsageEndpoint = server.URL + "/usage"
	directResetCreditsEndpoint = server.URL + "/reset-credits"
	directUsageHTTPClient = server.Client()
	directResponsesEndpoint = server.URL + "/responses"
	directModelsEndpoint = server.URL + "/models"
	primeHTTPClient = server.Client()
	defer func() {
		directUsageEndpoint, directResetCreditsEndpoint, directUsageHTTPClient = oldUsage, oldCredits, oldClient
		directResponsesEndpoint, directModelsEndpoint, primeHTTPClient = oldResponses, oldModels, oldPrimeClient
	}()
	// Priming must not depend on a codex process being installed.
	t.Setenv("PATH", t.TempDir())

	rs := fetchAllUsageWithPriming(p, []Account{a})
	if len(rs) != 1 || rs[0].Err != "" || rs[0].PrimeErr != "" {
		t.Fatalf("unexpected result: %+v", rs)
	}
	if !rs[0].Usage.WindowStarted || !rs[0].Primed {
		t.Fatalf("weekly window should be started after the priming turn: %+v", rs[0])
	}
	if primes != 1 {
		t.Fatalf("primes=%d", primes)
	}

	accounts, err := listAccounts(p)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts: %+v err=%v", accounts, err)
	}
	rs = fetchAllUsageWithPriming(p, accounts)
	if rs[0].PrimeErr != "" || !rs[0].Usage.WindowStarted {
		t.Fatalf("second result: %+v", rs[0])
	}
	if primes != 1 {
		t.Fatalf("window was primed more than once: primes=%d", primes)
	}
}
