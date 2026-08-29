package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLoginCommandArgs(t *testing.T) {
	got, err := parseLoginCommandArgs([]string{"backup", "--expect", "user@example.com"}, false, "cx add [NAME] [--expect EMAIL]")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "backup" || got.ExpectedEmail != "user@example.com" {
		t.Fatalf("args=%+v", got)
	}

	got, err = parseLoginCommandArgs([]string{"--expect=user@example.com", "backup"}, true, "cx relogin NAME [--expect EMAIL]")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "backup" || got.ExpectedEmail != "user@example.com" {
		t.Fatalf("args=%+v", got)
	}

	for _, args := range [][]string{{"--expect"}, {"--bad"}, {"backup", "other"}, {"--expect", "a@example.com", "--expect", "b@example.com"}} {
		if _, err := parseLoginCommandArgs(args, false, "cx add [NAME] [--expect EMAIL]"); err == nil {
			t.Fatalf("expected parse failure for %+v", args)
		}
	}
	if _, err := parseLoginCommandArgs([]string{"--expect", "a@example.com"}, true, "cx relogin NAME [--expect EMAIL]"); err == nil {
		t.Fatal("expected relogin name requirement")
	}
}

func TestPrintHelpListCurrentAndUse(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct-a", Email: "a@example.com", Plan: "plus", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	b := Account{ID: "b", Name: "backup", AccountID: "acct-b", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	writeTestAccount(t, p, b)
	if err := saveState(p, State{ActiveID: "a"}); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, printHelp)
	if !strings.Contains(out, "cx update") || !strings.Contains(out, "cx status") || !strings.Contains(out, "--expect EMAIL") {
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
	out = captureStdout(t, func() { handleUse(p, []string{"backup"}) })
	if !strings.Contains(out, "switched to backup") {
		t.Fatalf("use=%q", out)
	}
	st, _ := loadState(p)
	if st.ActiveID != "b" {
		t.Fatalf("active=%q", st.ActiveID)
	}
}

func installActiveWindowCodex(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
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
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0755); err != nil {
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
