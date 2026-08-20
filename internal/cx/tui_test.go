package cx

import (
	"os"
	"strings"
	"testing"
	"time"
)

func withPipeStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(data); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()
	fn()
}

func TestReadKeyVariants(t *testing.T) {
	tests := []struct{ in, want string }{
		{"\n", "enter"},
		{"k", "k"},
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
		{"\x1bx", "esc"},
	}
	for _, tt := range tests {
		withPipeStdin(t, tt.in, func() {
			got, err := readKey()
			if err != nil || got != tt.want {
				t.Fatalf("readKey(%q)=%q err=%v want=%q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestDashboardNonTerminalAndEmptyAccountErrors(t *testing.T) {
	p := makeTestPaths(t)
	if err := runDashboard(p); err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Fatalf("expected no-account error, got %v", err)
	}
	if _, err := pickAccount(p); err == nil {
		t.Fatal("expected empty picker error")
	}

	accounts := []Account{{ID: "a", Name: "primary"}}
	results := []UsageResult{{Account: accounts[0], Usage: WeeklyUsage{UsedPercent: 20, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}}}
	withPipeStdin(t, "", func() {
		out := captureStdout(t, func() {
			sel, action, err := dashboardLoop(p, accounts, results, 0)
			if err != nil || sel != 0 || action != "quit" {
				t.Fatalf("sel=%d action=%q err=%v", sel, action, err)
			}
		})
		if !strings.Contains(out, "cx status") {
			t.Fatalf("output=%q", out)
		}
	})
}

func TestDrawDashboard(t *testing.T) {
	p := makeTestPaths(t)
	if err := saveState(p, State{ActiveID: "a"}); err != nil {
		t.Fatal(err)
	}
	r := UsageResult{Account: Account{ID: "a", Name: "primary", Plan: "plus"}, Usage: WeeklyUsage{UsedPercent: 5, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, Primed: true}
	out := captureStdout(t, func() { drawDashboard(p, []Account{r.Account}, []UsageResult{r}, 0) })
	for _, want := range []string{"cx", "primary", "weekly window started just now", "enter switch"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q: %q", want, out)
		}
	}
}
