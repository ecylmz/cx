package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAccountCommandArgs(t *testing.T) {
	const usage = "cx add [NAME] [--provider PROVIDER] [--expect EMAIL]"
	got, err := parseAccountCommandArgs([]string{"backup", "--expect", "user@example.com"}, "--expect", usage, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.names) != 1 || got.names[0] != "backup" || got.expectedEmail != "user@example.com" || got.provider != nil {
		t.Fatalf("args=%+v", got)
	}

	got, err = parseAccountCommandArgs([]string{"--expect=user@example.com", "backup", "--provider=codex"}, "--expect", usage, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.names) != 1 || got.names[0] != "backup" || got.expectedEmail != "user@example.com" || got.provider == nil || *got.provider != providerCodex {
		t.Fatalf("args=%+v", got)
	}

	for _, args := range [][]string{
		{"--expect"},
		{"--bad"},
		{"--expect", "a@example.com", "--expect", "b@example.com"},
		{"--provider"},
		{"--provider="},
		{"--provider", "unknown"},
		{"--provider=codex", "--provider=agy"},
	} {
		if _, err := parseAccountCommandArgs(args, "--expect", usage, true); err == nil {
			t.Fatalf("expected parse failure for %+v", args)
		}
	}
}

func TestPrintHelpListCurrentAndUse(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct-a", Email: "a@example.com", Subscription: codexPlus, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	b := Account{ID: "b", Name: "backup", AccountID: "acct-b", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	writeTestAccount(t, p, b)
	if err := saveState(p, State{CodexAccountID: "a"}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, printHelp)
	if !strings.Contains(out, "cx update") || !strings.Contains(out, "cx status") || !strings.Contains(out, "cx auto") || !strings.Contains(out, "--expect EMAIL") {
		t.Fatalf("help=%q", out)
	}
	out = captureStdout(t, func() { handleList(p) })
	if !strings.Contains(out, "primary") || !strings.Contains(out, "backup") || !strings.Contains(out, "*") {
		t.Fatalf("list=%q", out)
	}
	out = captureStdout(t, func() { handleCurrent(p) })
	if !strings.Contains(out, "primary") {
		t.Fatalf("current=%q", out)
	}
	out = captureStdout(t, func() {
		if err := useAccount(p, []string{"backup"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "switched to backup") {
		t.Fatalf("use=%q", out)
	}
	st, _ := loadState(p)
	if st.CodexAccountID != "b" {
		t.Fatalf("active=%q", st.CodexAccountID)
	}
}

func installActiveWindowCodex(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = "app-server" ]; then
  IFS= read -r init
  echo '{"id":1,"result":{}}'
  IFS= read -r initialized
  IFS= read -r request
  now=$(date +%s)
  reset=$((now + 432000))
  echo "{\"id\":2,\"result\":{\"rateLimits\":{\"primary\":{\"usedPercent\":10,\"windowDurationMins\":10080,\"resetsAt\":$reset},\"secondary\":null},\"rateLimitsByLimitId\":null}}"
  while :; do sleep 1; done
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestHandleStatusSuccessAndJSON(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	installActiveWindowCodex(t)
	out := captureStdout(t, func() {
		if code := handleStatus(p, []string{"primary"}); code != 0 {
			t.Fatalf("code=%d", code)
		}
	})
	if !strings.Contains(out, "90.0% left") || !strings.Contains(out, "live") {
		t.Fatalf("status=%q", out)
	}
	out = captureStdout(t, func() {
		if code := handleStatus(p, []string{"primary", "--json"}); code != 0 {
			t.Fatalf("code=%d", code)
		}
	})
	if !strings.Contains(out, `"version"`) || !strings.Contains(out, `"primary"`) {
		t.Fatalf("json=%q", out)
	}
}

func TestMainVersion(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	os.Args = []string{"cx", "version"}
	out := captureStdout(t, Main)
	if !strings.Contains(out, "cx "+Version) {
		t.Fatalf("version out=%q", out)
	}
}

func TestHandleInitReportsAdoptionAndNoop(t *testing.T) {
	p := makeTestPaths(t)

	out := captureStdout(t, func() { handleInit(p, "") })
	if !strings.Contains(out, "no existing Codex login") || !strings.Contains(out, "cx add NAME") {
		t.Fatalf("init on unauthorized system=%q", out)
	}

	if err := os.WriteFile(p.sharedAuthPath(), testAuthBytesFor(t, "acct-old", "me@example.com"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { handleInit(p, "") })
	if !strings.Contains(out, "primary") || !strings.Contains(out, "me@example.com") {
		t.Fatalf("init adoption=%q", out)
	}

	out = captureStdout(t, func() { handleInit(p, "") })
	if !strings.Contains(out, "already manages accounts") {
		t.Fatalf("repeat init=%q", out)
	}
	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts=%+v", accounts)
	}
}
