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
		{"\t", "tab"},
		{"k", "k"},
		{"\x1b[A", "up"},
		{"\x1b[B", "down"},
		{"\x03", "ctrl-c"},
		{"\x04", "ctrl-d"},
		// Sequences the dashboard does not act on must be consumed and
		// ignored, not reported as Esc, which every input loop treats as quit.
		{"\x1b[C", "right"},
		{"\x1b[D", "left"},
		{"\x1b[5~", "pgup"},
		{"\x1b[6~", "pgdn"},
		{"\x1b[H", "home"},
		{"\x1b[F", "end"},
		{"\x1b[1;5A", "up"},
		{"\x1b[3~", ""},
		{"\x1bx", ""},
	}
	for _, tt := range tests {
		withPipeStdin(t, tt.in, func() {
			got, err := readKeyFrom(keyBytes())
			if err != nil || got != tt.want {
				t.Fatalf("readKey(%q)=%q err=%v want=%q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestReadKeyReturnsOnASingleEsc(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = w.Close()
		_ = r.Close()
	}()
	if _, err := w.Write([]byte{27}); err != nil {
		t.Fatal(err)
	}

	// The writer stays open, so a readKey that waits for a continuation byte
	// would block forever instead of reporting the keypress.
	type result struct {
		key string
		err error
	}
	done := make(chan result, 1)
	go func() {
		key, err := readKeyFrom(keyBytes())
		done <- result{key, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.key != "esc" {
			t.Fatalf("readKey(esc)=%q err=%v", got.key, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readKey blocked on a single esc")
	}
}

func TestDashboardNonTerminalAndEmptyAccountErrors(t *testing.T) {
	p := makeTestPaths(t)
	if err := runDashboard(p); err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Fatalf("expected no-account error, got %v", err)
	}
	if _, err := pickAccount(p, nil, ""); err == nil {
		t.Fatal("expected empty picker error")
	}

	installTestAgy(t)
	writeTestAccount(t, p, Account{ID: agyDefaultID, Name: "primary", Subscription: agyPro})
	withPipeStdin(t, "", func() {
		out := captureStdout(t, func() {
			if err := runDashboard(p); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "cx status") || !strings.Contains(out, "Gemini Models") {
			t.Fatalf("output=%q", out)
		}
	})
}

func TestDrawDashboard(t *testing.T) {
	p := makeTestPaths(t)
	if err := saveState(p, State{CodexAccountID: "a"}); err != nil {
		t.Fatal(err)
	}
	r := UsageResult{Account: Account{ID: "a", Name: "primary", Email: "user@example.com", Subscription: codexPlus}, Usage: WeeklyUsage{UsedPercent: 5, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, Primed: true}
	out := captureStdout(t, func() { drawDashboardFrame(newDashboardView(p, []Account{r.Account}, []UsageResult{r}, 0)) })
	for _, want := range []string{"cx", "user@example.com", "quota windows started just now", "enter switch"} {
		if !strings.Contains(plain(out), want) {
			t.Fatalf("dashboard missing %q: %q", want, out)
		}
	}
}

func TestRawModePreservesTerminalLineEndings(t *testing.T) {
	args := rawModeArgs()
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" raw ", " -echo ", " opost ", " onlcr "} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rawModeArgs()=%q missing %q", args, strings.TrimSpace(want))
		}
	}
}
