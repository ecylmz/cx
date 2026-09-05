package cx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchBankedResetsDirectUsesCodexBackendContract(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access" {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct" {
			t.Errorf("ChatGPT-Account-Id=%q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "codex-cli" {
			t.Errorf("User-Agent=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"available_count": 2,
			"credits": [
				{"id":"later","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-08-01T00:00:00Z","expires_at":"2026-09-01T00:00:00Z","title":"Later reset"},
				{"id":"used","reset_type":"codex_rate_limits","status":"redeemed","granted_at":"2026-08-01T00:00:00Z","expires_at":"2026-08-25T00:00:00Z","title":"Used reset"},
				{"id":"soon","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-08-01T00:00:00Z","expires_at":"2026-08-24T00:00:00Z","title":"Soon reset"}
			]
		}`))
	}))
	defer server.Close()

	oldEndpoint, oldClient := directResetCreditsEndpoint, directUsageHTTPClient
	directResetCreditsEndpoint, directUsageHTTPClient = server.URL, server.Client()
	defer func() {
		directResetCreditsEndpoint, directUsageHTTPClient = oldEndpoint, oldClient
	}()

	resets, err := fetchBankedResetsDirect(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(resets) != 2 {
		t.Fatalf("resets=%+v", resets)
	}
	if resets[0].ID != "soon" || resets[1].ID != "later" {
		t.Fatalf("banked resets should be sorted by expiry: %+v", resets)
	}
}

func TestFetchAllUsageReadsUsageAndBankedResetsWithoutCodex(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"rate_limit":{"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_at":321}}}`))
	}))
	defer usageServer.Close()
	creditsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"available_count":1,"credits":[{"id":"credit-1","status":"available","expires_at":"2026-09-01T00:00:00Z"}]}`))
	}))
	defer creditsServer.Close()

	oldUsageEndpoint, oldCreditsEndpoint, oldClient := directUsageEndpoint, directResetCreditsEndpoint, directUsageHTTPClient
	directUsageEndpoint, directResetCreditsEndpoint, directUsageHTTPClient = usageServer.URL, creditsServer.URL, usageServer.Client()
	defer func() {
		directUsageEndpoint, directResetCreditsEndpoint, directUsageHTTPClient = oldUsageEndpoint, oldCreditsEndpoint, oldClient
	}()
	t.Setenv("PATH", t.TempDir())

	rs := fetchAllUsage(p, []Account{a})
	if len(rs) != 1 || rs[0].Err != "" || rs[0].BankedErr != "" || !rs[0].BankedLoaded {
		t.Fatalf("result=%+v", rs)
	}
	if rs[0].Usage.UsedPercent != 10 || len(rs[0].BankedResets) != 1 || rs[0].BankedResets[0].ID != "credit-1" {
		t.Fatalf("result=%+v", rs[0])
	}
}

func TestBankedDetailRowShowsExpiry(t *testing.T) {
	t.Setenv("LC_TIME", "tr_TR.UTF-8")
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	row := bankedDetailRow(BankedReset{ID: "a", ExpiresAt: "2026-08-24T12:00:00Z"}, now)
	if !strings.Contains(row, "24.08.2026 15:00") && !strings.Contains(row, "24.08.2026 12:00") {
		t.Fatalf("detail row=%q", row)
	}
	if !strings.Contains(row, "in 3d") {
		t.Fatalf("detail row=%q", row)
	}
	if past := bankedDetailRow(BankedReset{ID: "a", ExpiresAt: "2026-08-20T12:00:00Z"}, now); !strings.Contains(past, "expired") {
		t.Fatalf("a reset already gone should say so: %q", past)
	}
	if undated := bankedDetailRow(BankedReset{ID: "a"}, now); !strings.Contains(undated, "expires unknown") {
		t.Fatalf("undated row=%q", undated)
	}
}

// The dashboard spends one line on banked resets however many there are; the
// dates behind it belong to `cx status` and the `b` screen.
func TestBankedDashboardLineStaysOneLine(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	resets := make([]BankedReset, 0, 12)
	for i := range 12 {
		resets = append(resets, BankedReset{
			ID:        fmt.Sprint(i),
			ExpiresAt: now.Add(time.Duration(i+1) * 48 * time.Hour).Format(time.RFC3339),
		})
	}
	lines := bankedResetLines(UsageResult{BankedLoaded: true, BankedResets: resets}, "  ")
	if len(lines) != 1 {
		t.Fatalf("dashboard should spend one line, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "banked") || !strings.Contains(lines[0], "12 left") {
		t.Fatalf("line=%q", lines[0])
	}
	if strings.Contains(lines[0], "Reset") {
		t.Fatalf("the per-reset title should not be repeated: %q", lines[0])
	}
}

// cx status is the full record: the same axis line, then every date under it.
func TestBankedStatusLinesListEveryReset(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	resets := []BankedReset{
		{ID: "a", ExpiresAt: now.Add(6 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "b", ExpiresAt: now.Add(13 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "c", ExpiresAt: now.Add(20 * 24 * time.Hour).Format(time.RFC3339)},
	}
	lines := bankedStatusLines(UsageResult{BankedLoaded: true, BankedResets: resets}, "  ")
	if len(lines) != 1+len(resets) {
		t.Fatalf("status should list every reset, got %d lines: %q", len(lines), lines)
	}
	// Each date lands on its own row, in the order the resets will be spent.
	for i, reset := range resets {
		at, _ := bankedExpiry(reset.ExpiresAt)
		want := at.Local().Format(localDateLayout(localeName()))
		if !strings.Contains(lines[i+1], want) {
			t.Fatalf("line %d = %q, want %q", i+1, lines[i+1], want)
		}
	}
}

// A pip's column is a date, so the same expiry has to land in the same cell on
// every account's line rather than being scaled to that account's own spread.
func TestBankedAxisIsAFixedThirtyDayScale(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	at := func(d time.Duration) BankedReset {
		return BankedReset{ID: d.String(), ExpiresAt: now.Add(d).Format(time.RFC3339)}
	}
	day := 24 * time.Hour

	_, lone := bankedAxis([]BankedReset{at(15 * day)}, now, defaultBarCells)
	_, spread := bankedAxis([]BankedReset{at(15 * day), at(29 * day)}, now, defaultBarCells)
	if strings.IndexRune(lone, []rune(bankedPip)[0]) != strings.IndexRune(spread, []rune(bankedPip)[0]) {
		t.Fatalf("the 15-day pip moved when a later reset was added:\n%q\n%q", lone, spread)
	}

	_, near := bankedAxis([]BankedReset{at(time.Hour)}, now, defaultBarCells)
	if !strings.HasPrefix(near, bankedPip) {
		t.Fatalf("a reset about to go belongs in the first cell: %q", near)
	}
	_, far := bankedAxis([]BankedReset{at(29 * day)}, now, defaultBarCells)
	if !strings.HasSuffix(far, bankedPip) {
		t.Fatalf("a reset at the horizon belongs in the last cell: %q", far)
	}
	if n := strings.Count(far, bankedTick); n != 4 {
		t.Fatalf("want week ticks at day 7, 14, 21 and 28, got %d: %q", n, far)
	}
}

// Two resets in one cell are drawn filled, so the axis cannot be counted to a
// different total than the number printed beside it.
func TestBankedAxisMarksCrowdedCells(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	resets := []BankedReset{
		{ID: "a", ExpiresAt: now.Add(25 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "b", ExpiresAt: now.Add(25*24*time.Hour + 2*time.Hour).Format(time.RFC3339)},
	}
	_, axis := bankedAxis(resets, now, defaultBarCells)
	if !strings.Contains(axis, bankedPipStack) {
		t.Fatalf("two resets in one cell should be drawn filled: %q", axis)
	}
	if strings.Contains(axis, bankedPip) {
		t.Fatalf("the crowded cell should not also render as a single pip: %q", axis)
	}
}

// Urgency reads off the pips, which is what lets the dashboard drop the dates.
func TestBankedPipCarriesUrgency(t *testing.T) {
	oldColor := useColor
	useColor = true
	defer func() { useColor = oldColor }()

	now := time.Now()
	urgent := []BankedReset{{ID: "a", ExpiresAt: now.Add(3 * time.Hour).Format(time.RFC3339)}}
	styled, _ := bankedAxis(urgent, now, defaultBarCells)
	if !strings.Contains(styled, red(bankedPip)) {
		t.Fatalf("a reset going within the day should be red: %q", styled)
	}
	soon := []BankedReset{{ID: "a", ExpiresAt: now.Add(3 * 24 * time.Hour).Format(time.RFC3339)}}
	styled, _ = bankedAxis(soon, now, defaultBarCells)
	if !strings.Contains(styled, yellow(bankedPip)) {
		t.Fatalf("a reset going within the week should be yellow: %q", styled)
	}
}

// The clock on screen keeps running between refetches, so the soonest reset can
// pass while later ones are still live. Calling the line expired then would
// contradict the count printed right beside it.
func TestBankedCountdownSkipsResetsAlreadyGone(t *testing.T) {
	// RFC3339 carries no sub-second part, so the reference has none either and
	// the round trip lands on exactly the durations below.
	now := time.Now().Truncate(time.Second)
	at := func(d time.Duration) BankedReset {
		return BankedReset{ID: d.String(), ExpiresAt: now.Add(d).Format(time.RFC3339)}
	}
	if got := bankedCountdown([]BankedReset{at(-2 * time.Hour), at(10 * 24 * time.Hour)}, now, false); got != "next in 10d" {
		t.Fatalf("countdown should name the next live reset, got %q", got)
	}
	if got := bankedCountdown([]BankedReset{at(-2 * time.Hour), at(-time.Minute)}, now, false); got != "expired" {
		t.Fatalf("with nothing left in the future the line should say so, got %q", got)
	}
	if got := bankedCountdown(nil, now, false); got != "" {
		t.Fatalf("no resets, no countdown: %q", got)
	}
}

// A reset the backend gave no expiry for never reaches the axis, so the count
// cell has to name it or the pips and the count beside them cannot be reconciled.
func TestBankedLineNamesUndatedResets(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	resets := []BankedReset{
		{ID: "a", ExpiresAt: now.Add(9 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "b"},
	}
	line := bankedLine(resets, now, defaultBarCells, false)
	if !strings.Contains(line, "2 left · 1?") {
		t.Fatalf("line=%q", line)
	}
	// The count sits in a fixed cell, so it survives the trim at a width that
	// leaves no room for a trailing note at all. The countdown does not.
	const cols = 40
	narrow := bankedResetLinesWidth(UsageResult{BankedLoaded: true, BankedResets: resets}, "   ", cols)
	if len(narrow) != 1 {
		t.Fatalf("expected one banked line, got %d", len(narrow))
	}
	trimmed := fitCells(narrow[0], cols)
	if !strings.Contains(trimmed, "2 left · 1?") || strings.Contains(trimmed, "next in") {
		t.Fatalf("narrow line=%q", trimmed)
	}
}

// How long the next reset has left is what the line is read for, so it survives
// every width — shortened on a narrow terminal, never dropped.
func TestBankedLineAlwaysKeepsTheCountdown(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	r := UsageResult{BankedLoaded: true, BankedResets: []BankedReset{
		{ID: "a", ExpiresAt: now.Add(6*24*time.Hour + 15*time.Hour).Format(time.RFC3339)},
		{ID: "b", ExpiresAt: now.Add(20 * 24 * time.Hour).Format(time.RFC3339)},
	}}
	for _, cols := range []int{40, 60, 79, 100, 0} {
		lines := bankedResetLinesWidth(r, "   ", cols)
		if len(lines) != 1 {
			t.Fatalf("cols=%d lines=%q", cols, lines)
		}
		if !strings.Contains(lines[0], "6d") {
			t.Fatalf("cols=%d: the countdown to the next reset went missing: %q", cols, lines[0])
		}
		// Below compactBelowCols the whole block runs past the margin — the quota
		// meters included — so only the widths the rest of the block fits in are
		// held to fitting here.
		if cols >= compactBelowCols {
			if got := visibleCells(lines[0]); got > cols {
				t.Fatalf("cols=%d: line is %d cells and would be cut: %q", cols, got, lines[0])
			}
		}
	}

	// The narrow form spends its room on the number, not on the word.
	wide := bankedLine(r.BankedResets, now, defaultBarCells, false)
	narrow := bankedLine(r.BankedResets, now, defaultBarCells, true)
	if !strings.Contains(wide, "next in 6d") {
		t.Fatalf("wide=%q", wide)
	}
	if strings.Contains(narrow, "next") || !strings.Contains(narrow, "in 6d") {
		t.Fatalf("narrow=%q", narrow)
	}
	if visibleCells(narrow) >= visibleCells(wide) {
		t.Fatalf("the narrow form should be shorter: %d vs %d", visibleCells(narrow), visibleCells(wide))
	}
}

// A reset whose expiry has passed is gone: the dashboard's ticker keeps redrawing
// with a fresh clock between fetches, and the pips, the count and the countdown
// all have to agree about that rather than one of them still claiming it.
func TestExpiredResetsLeaveTheWholeBankedLine(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	at := func(d time.Duration) BankedReset {
		return BankedReset{ID: d.String(), ExpiresAt: now.Add(d).Format(time.RFC3339)}
	}
	r := UsageResult{BankedLoaded: true, BankedResets: []BankedReset{at(-2 * time.Hour), at(10 * 24 * time.Hour)}}

	lines := bankedResetLinesWidth(r, "   ", 100)
	if len(lines) != 1 {
		t.Fatalf("lines=%q", lines)
	}
	if !strings.Contains(lines[0], "1 left") || !strings.Contains(lines[0], "next in 9d") {
		t.Fatalf("the gone reset should be off the count: %q", lines[0])
	}
	if n := strings.Count(lines[0], bankedPip); n != 1 {
		t.Fatalf("want one pip for the one reset left, got %d: %q", n, lines[0])
	}
	// Cell 0 is where an expired reset used to land, drawn in the same red as one
	// about to go.
	if strings.HasPrefix(strings.TrimPrefix(lines[0], "   banked  "), bankedPip) {
		t.Fatalf("a gone reset should not still sit at the head of the axis: %q", lines[0])
	}

	// The full listing under it agrees, and so does the status block.
	if got := len(bankedStatusLines(r, "  ")); got != 2 {
		t.Fatalf("status should list the axis and the one live reset, got %d lines", got)
	}

	// With nothing left the line goes altogether, like an account that never had any.
	gone := UsageResult{BankedLoaded: true, BankedResets: []BankedReset{at(-2 * time.Hour)}}
	if lines := bankedResetLinesWidth(gone, "   ", 100); len(lines) != 0 {
		t.Fatalf("nothing left should render nothing: %q", lines)
	}
	if lines := bankedStatusLines(gone, "  "); len(lines) != 0 {
		t.Fatalf("nothing left should render nothing in status: %q", lines)
	}
}
