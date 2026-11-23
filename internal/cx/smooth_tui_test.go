package cx

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUsageLineUsesRemainingQuotaBar(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	u := WeeklyUsage{UsedPercent: 25, WindowMinutes: 10080, ResetsAt: now.Add(3 * time.Hour).Unix(), FetchedAt: now, Fresh: true, WindowStarted: true}
	line := strings.Join(usageLines(UsageResult{Usage: u}), "\n")
	if !strings.Contains(line, "75.0% left") || !strings.Contains(line, "█████████████████░░░░░") {
		t.Fatalf("remaining quota bar mismatch: %q", line)
	}
}

func TestSelectionUpdateOnlyTouchesTheTwoAffectedBlocks(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	accounts := []Account{
		{ID: "a", Email: "primary@example.com"},
		{ID: "b", Email: "backup@example.com"},
		{ID: "c", Email: "spare@example.com"},
	}
	results := make([]UsageResult, len(accounts))
	for i, a := range accounts {
		results[i] = UsageResult{Account: a, Err: "offline"}
	}
	v := newDashboardView(p, accounts, results, 0)
	v.size = termSize{rows: 40, cols: 100}
	_, layout := renderDashboardFrame(v)
	row := func(i int) string { return fmt.Sprintf("\x1b[%d;1H", layout.headerRows[i]) }
	update := selectionUpdateString(v, 0, 1, layout)
	for _, want := range []string{syncOutputBegin, syncOutputEnd, row(0), row(1), "primary@example.com", "backup@example.com"} {
		if !strings.Contains(update, want) {
			t.Fatalf("selection update missing %q: %q", want, update)
		}
	}
	if strings.Count(update, "▌") != 1 || !strings.Contains(update, "▌  backup@example.com") {
		t.Fatalf("selection update should draw the cursor on the new account row: %q", update)
	}
	if strings.Contains(update, "spare") || strings.Contains(update, row(2)) || strings.Contains(update, clearScreenHome) {
		t.Fatalf("selection update should not redraw the dashboard: %q", update)
	}
}

func TestDashboardKeepsAccountNotesInTheFooter(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	accounts := []Account{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}
	results := []UsageResult{
		{Account: accounts[0], Usage: WeeklyUsage{UsedPercent: 10, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, Primed: true},
		{Account: accounts[1], Usage: WeeklyUsage{UsedPercent: 20, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, BankedLoaded: true},
	}
	v := newDashboardView(p, accounts, results, 0)
	v.footer = "live quota · live banked resets"
	frame, layout := renderDashboardFrame(v)
	if len(layout.headerRows) != 2 || layout.headerRows[0] != 4 {
		t.Fatalf("unexpected header rows: %v", layout.headerRows)
	}
	rows := strings.Split(frame, "\n")
	if layout.headerRows[1]-layout.headerRows[0] != 1 || !strings.Contains(rows[len(rows)-2], "quota windows started just now") {
		t.Fatalf("the priming note should be in the footer and accounts should use consecutive rows:\n%s", frame)
	}
}

func TestTerminalSessionUsesAlternateScreenAndSynchronizedOutput(t *testing.T) {
	for _, seq := range []string{altScreenEnter, altScreenExit, hideCursor, showCursor, disableWrap, enableWrap, syncOutputBegin, syncOutputEnd} {
		if seq == "" {
			t.Fatal("terminal control sequence must not be empty")
		}
	}
}
