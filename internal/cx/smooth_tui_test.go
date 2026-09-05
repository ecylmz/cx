package cx

import (
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
	line := usageLine(u)
	if !strings.Contains(line, "75.0% left") || !strings.Contains(line, "█████████████████░░░░░") {
		t.Fatalf("remaining quota bar mismatch: %q", line)
	}
}

func TestZeroBankedResetsAreHidden(t *testing.T) {
	r := UsageResult{BankedLoaded: true, BankedResets: []BankedReset{}}
	if lines := bankedResetLines(r, "  "); len(lines) != 0 {
		t.Fatalf("zero banked resets should be hidden: %q", lines)
	}
}

func TestSelectionUpdateOnlyTouchesTheTwoAffectedBlocks(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	accounts := []Account{{ID: "a", Name: "primary"}, {ID: "b", Name: "backup"}, {ID: "c", Name: "spare"}}
	results := make([]UsageResult, len(accounts))
	for i, a := range accounts {
		results[i] = UsageResult{Account: a, Err: "offline"}
	}
	layout := dashboardLayout{headerRows: []int{3, 6, 9}, fits: true}
	v := newDashboardView(p, accounts, results, 0)
	update := selectionUpdateString(v, 0, 1, layout)
	for _, want := range []string{syncOutputBegin, syncOutputEnd, "\x1b[3;1H", "\x1b[6;1H", "primary", "backup"} {
		if !strings.Contains(update, want) {
			t.Fatalf("selection update missing %q: %q", want, update)
		}
	}
	// The block that moved gains the cursor bar; the one that did not is left
	// alone, and so is the rest of the screen.
	if !strings.Contains(update, "▌") {
		t.Fatalf("selection update does not draw the cursor bar: %q", update)
	}
	if strings.Contains(update, "spare") || strings.Contains(update, "\x1b[9;1H") || strings.Contains(update, clearScreenHome) {
		t.Fatalf("selection update should not redraw the dashboard: %q", update)
	}
}

func TestDashboardFrameTracksVariableHeightRows(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	accounts := []Account{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}}
	results := []UsageResult{
		{Account: accounts[0], Usage: WeeklyUsage{UsedPercent: 10, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, BankedLoaded: true, BankedResets: []BankedReset{{ID: "r1", Title: "Full reset"}}},
		{Account: accounts[1], Usage: WeeklyUsage{UsedPercent: 20, ResetsAt: time.Now().Add(time.Hour).Unix(), WindowStarted: true}, BankedLoaded: true},
	}
	v := newDashboardView(p, accounts, results, 0)
	v.footer = "live quota · live banked resets"
	_, layout := renderDashboardFrame(v)
	if len(layout.headerRows) != 2 || layout.headerRows[0] != 3 {
		t.Fatalf("unexpected header rows: %v", layout.headerRows)
	}
	// Account a carries a banked line and b does not, so the second header sits
	// exactly as far down as the first block is tall.
	if got, want := layout.headerRows[1]-layout.headerRows[0], len(accountBlock(v, 0)); got != want {
		t.Fatalf("header rows %v do not follow block heights: gap=%d block=%d", layout.headerRows, got, want)
	}
}

func TestTerminalSessionUsesAlternateScreenAndSynchronizedOutput(t *testing.T) {
	for _, seq := range []string{altScreenEnter, altScreenExit, hideCursor, showCursor, disableWrap, enableWrap, syncOutputBegin, syncOutputEnd} {
		if seq == "" {
			t.Fatal("terminal control sequence must not be empty")
		}
	}
}
