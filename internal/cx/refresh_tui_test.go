package cx

import (
	"strings"
	"testing"
	"time"
)

func TestRefreshingFrameShowsCachedAgeWithoutStale(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary"}
	accounts := []Account{a}
	results := []UsageResult{{
		Account: a,
		Err:     refreshingError("refreshing…"),
		Usage: WeeklyUsage{
			UsedPercent:   20,
			WindowMinutes: 10080,
			ResetsAt:      time.Now().Add(5 * 24 * time.Hour).Unix(),
			FetchedAt:     time.Now().Add(-15 * time.Second),
			Fresh:         false,
			WindowStarted: true,
		},
	}}

	frame, _ := renderDashboardFrame(p, accounts, results, 0, refreshFooter(0, 1))
	if !strings.Contains(frame, "refreshing… · cached 15s ago") {
		t.Fatalf("refreshing state missing cached age:\n%s", frame)
	}
	if strings.Contains(frame, "stale") {
		t.Fatalf("refresh-in-progress should not be labeled stale:\n%s", frame)
	}
}

func TestFiveHourZeroPercentStartedWindowStopsLookingIdleQuickly(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	startedAt := now.Add(-15 * time.Second)
	u := WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 300,
		ResetsAt:      startedAt.Add(5 * time.Hour).Unix(),
		FetchedAt:     now,
	}
	if looksLikeUnstartedWindow(u, now) {
		t.Fatal("a fixed five-hour reset 15 seconds into the window should be treated as started")
	}

	u.ResetsAt = now.Add(5 * time.Hour).Unix()
	if !looksLikeUnstartedWindow(u, now) {
		t.Fatal("a moving full-window reset should still be treated as not started")
	}
}

func TestFiveHourIdleLineExplainsHowItStarts(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now().Truncate(time.Second)
	u := WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 300,
		ResetsAt:      now.Add(5 * time.Hour).Unix(),
		FetchedAt:     now,
	}
	line := fiveHourUsageLine(u)
	if !strings.Contains(line, "not started · starts with Codex use") {
		t.Fatalf("five-hour idle line is ambiguous: %q", line)
	}
}

func TestRefreshFooterShowsAccountProgress(t *testing.T) {
	if got := refreshFooter(3, 7); got != "refreshing 3/7 accounts…" {
		t.Fatalf("refresh footer=%q", got)
	}
}
