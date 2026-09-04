package cx

import (
	"strings"
	"testing"
	"time"
)

func TestLocalDateLayout(t *testing.T) {
	tests := map[string]string{
		"tr_TR":       "02.01.2006 15:04",
		"tr_TR.UTF-8": "02.01.2006 15:04",
		"en_US":       "01/02/2006 15:04",
		"en_GB":       "02/01/2006 15:04",
		"ja_JP":       "2006/01/02 15:04",
		"":            "2006-01-02 15:04",
	}
	for locale, want := range tests {
		normalized := locale
		if locale == "tr_TR.UTF-8" {
			normalized = "tr_tr"
		}
		if got := localDateLayout(normalized); got != want {
			t.Errorf("localDateLayout(%q)=%q want %q", locale, got, want)
		}
	}
	// Only the date order is locale specific: every layout keeps a 24-hour clock.
	for _, locale := range []string{"en_us", "en_ca", "en_gb", "ja_jp", "ko_kr", "zh_cn", "tr_tr", "de_de", "fr_fr", "pt_br", "ru_ru", ""} {
		if layout := localDateLayout(locale); strings.Contains(layout, "PM") || !strings.Contains(layout, "15:04") {
			t.Errorf("localDateLayout(%q)=%q is not a 24-hour clock", locale, layout)
		}
	}
}

func TestRelativeDuration(t *testing.T) {
	if got := relativeDuration(6*24*time.Hour + 3*time.Hour + 14*time.Minute); got != "6d 3h" {
		t.Fatalf("got %q", got)
	}
	if got := relativeDuration(2*time.Hour + 7*time.Minute); got != "2h 07m" {
		t.Fatalf("got %q", got)
	}
}

func TestLooksLikeUnstartedWindow(t *testing.T) {
	now := time.Date(2026, 8, 20, 20, 56, 0, 0, time.Local)
	u := WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 7 * 24 * 60,
		ResetsAt:      now.Add(7 * 24 * time.Hour).Unix(),
		FetchedAt:     now,
	}
	if !looksLikeUnstartedWindow(u, now) {
		t.Fatal("expected unused moving weekly window to be treated as not started")
	}
	u.ResetsAt = now.Add(5 * 24 * time.Hour).Unix()
	if looksLikeUnstartedWindow(u, now) {
		t.Fatal("expected fixed active reset to be treated as started")
	}
	u.ResetsAt = now.Add(7 * 24 * time.Hour).Unix()
	u.UsedPercent = 1
	if looksLikeUnstartedWindow(u, now) {
		t.Fatal("used window must be treated as started")
	}
}

func TestRenderHelpers(t *testing.T) {
	oldColor := useColor
	defer func() { useColor = oldColor }()
	useColor = false
	if got := ansi("31", "x"); got != "x" {
		t.Fatalf("ansi disabled=%q", got)
	}
	useColor = true
	// A style closes with the code that undoes just itself, so a styled string
	// nested in another one does not strip the style around it.
	if got := ansi("31", "x"); got != "\x1b[31mx\x1b[39m" {
		t.Fatalf("ansi enabled=%q", got)
	}
	if got := dim("x"); got != "\x1b[2mx\x1b[22m" {
		t.Fatalf("dim enabled=%q", got)
	}
	if got := green("a" + red("b") + "c"); got != "\x1b[32ma\x1b[31mb\x1b[39mc\x1b[39m" {
		t.Fatalf("nested color=%q", got)
	}
	useColor = false
	if emptyDash("") != "-" || emptyDash("x") != "x" {
		t.Fatal("emptyDash")
	}
	if clamp(-1, 0, 100) != 0 || clamp(101, 0, 100) != 100 || clamp(50, 0, 100) != 50 {
		t.Fatal("clamp")
	}
	if filled, empty := barCells(50, 4); filled+empty != "██░░" {
		t.Fatalf("barCells=%q%q", filled, empty)
	}
	if got := shortDuration(2*time.Hour + 3*time.Minute); got != "2h 03m" {
		t.Fatalf("short=%q", got)
	}
	if got := shortDuration(3 * time.Minute); got != "3m" {
		t.Fatalf("short=%q", got)
	}
}

func TestLocaleAndResetFormatting(t *testing.T) {
	t.Setenv("LC_TIME", "tr_TR.UTF-8")
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "en_US.UTF-8")
	if got := localeName(); got != "tr_tr" {
		t.Fatalf("locale=%q", got)
	}
	now := time.Now().Truncate(time.Second)
	future := now.Add(2*time.Hour + 5*time.Minute).Unix()
	got := resetText(future, now)
	if !strings.Contains(got, " · in 2h 05m") {
		t.Fatalf("reset=%q", got)
	}
	if resetText(0, now) != "unknown" || exactResetText(0) != "unknown" {
		t.Fatal("unknown reset")
	}
	past := now.Add(-time.Minute).Unix()
	if !strings.Contains(resetText(past, now), " · now") {
		t.Fatal("past reset should say now")
	}
}

func TestUsageLinePrintStatusAndJSON(t *testing.T) {
	p := makeTestPaths(t)
	if err := saveState(p, State{ActiveID: "a"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	u := WeeklyUsage{UsedPercent: 25, WindowMinutes: 10080, ResetsAt: now.Add(3 * time.Hour).Unix(), FetchedAt: now, Fresh: true, WindowStarted: true}
	line := usageLine(u)
	if !strings.Contains(line, "75.0% left") || !strings.Contains(line, "resets") {
		t.Fatalf("usage=%q", line)
	}
	accounts := []Account{{ID: "a", Name: "primary", Plan: "plus", Email: "a@example.com"}, {ID: "b", Name: "backup"}}
	results := []UsageResult{{Account: accounts[0], Usage: u}, {Account: accounts[1], Usage: WeeklyUsage{FetchedAt: now, WindowStarted: true}, Err: "offline"}}
	out := captureStdout(t, func() { printStatus(p, results) })
	// The usage line is rendered unindented and each caller adds its own
	// indentation, so pin the one cx status uses.
	if !strings.Contains(out, "\n  weekly  ") {
		t.Fatalf("status usage line lost its indentation:\n%s", out)
	}
	for _, want := range []string{"cx status", "primary", "75.0% left", "stale", "offline"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	payload := statusJSON(p, results).(map[string]any)
	if payload["version"] != Version {
		t.Fatalf("version=%v", payload["version"])
	}
	rows := payload["accounts"].([]jsonAccountStatus)
	if len(rows) != 2 || !rows[0].Active || rows[1].Weekly.Err != "offline" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestExhaustedWindowStartStaysOneQuietLine(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "backup", AccountID: "acct"}
	writeTestAccount(t, p, a)
	_, skipped := classifyPrimeFailure(errQuotaExhausted)
	r := UsageResult{
		Account:      a,
		Usage:        WeeklyUsage{WindowMinutes: 10080, ResetsAt: time.Now().Add(7 * 24 * time.Hour).Unix(), FetchedAt: time.Now()},
		PrimeSkipped: skipped,
	}

	out := captureStdout(t, func() { printStatus(p, []UsageResult{r}) })
	if strings.Contains(out, "window start failed") {
		t.Fatalf("an exhausted account must not be reported as a failure:\n%s", out)
	}
	notes := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "window not started") {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("want exactly one skip note, got %d:\n%s", notes, out)
	}

	frame, _ := renderDashboardFrame(newDashboardView(p, []Account{a}, []UsageResult{r}, 0))
	if strings.Contains(frame, "window start failed") {
		t.Fatalf("dashboard reported a failure:\n%s", frame)
	}
	if !strings.Contains(frame, "window not started") {
		t.Fatalf("dashboard dropped the skip note:\n%s", frame)
	}
}

func TestSingleLineCollapsesRepeatedBackendErrors(t *testing.T) {
	raw := "exit status 1: lse.\nERROR: You've hit your usage limit.\nERROR: You've hit your usage limit.\n"
	got := singleLine(raw)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("still multi-line: %q", got)
	}
	if n := len([]rune(got)); n > 160 {
		t.Fatalf("%d runes: %q", n, got)
	}
}
