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

func TestSelectionUpdateOnlyTouchesHeaderRows(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	accounts := []Account{{ID: "a", Name: "primary"}, {ID: "b", Name: "backup"}}
	layout := dashboardLayout{headerRows: []int{3, 6}}
	update := selectionUpdateString(p, accounts, 0, 1, layout)
	for _, want := range []string{syncOutputBegin, syncOutputEnd, "\x1b[3;1H", "\x1b[6;1H", "primary", "backup"} {
		if !strings.Contains(update, want) {
			t.Fatalf("selection update missing %q: %q", want, update)
		}
	}
	if strings.Contains(update, "weekly") || strings.Contains(update, clearScreenHome) {
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
	_, layout := renderDashboardFrame(p, accounts, results, 0, "live quota · live banked resets")
	if len(layout.headerRows) != 2 || layout.headerRows[0] != 3 || layout.headerRows[1] <= layout.headerRows[0]+3 {
		t.Fatalf("unexpected header rows: %v", layout.headerRows)
	}
}

func TestTerminalSessionUsesAlternateScreenAndSynchronizedOutput(t *testing.T) {
	for _, seq := range []string{altScreenEnter, altScreenExit, hideCursor, showCursor, disableWrap, enableWrap, syncOutputBegin, syncOutputEnd} {
		if seq == "" {
			t.Fatal("terminal control sequence must not be empty")
		}
	}
}
