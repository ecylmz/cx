package cx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchUsagePairDirectReturnsFiveHourAndWeekly(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"rate_limit":{
				"primary_window":{"used_percent":31,"limit_window_seconds":18000,"reset_at":100},
				"secondary_window":{"used_percent":47,"limit_window_seconds":604800,"reset_at":200}
			}
		}`))
	}))
	defer server.Close()

	oldEndpoint, oldClient := directUsageEndpoint, directUsageHTTPClient
	directUsageEndpoint, directUsageHTTPClient = server.URL, server.Client()
	defer func() {
		directUsageEndpoint, directUsageHTTPClient = oldEndpoint, oldClient
	}()

	fiveHour, weekly, err := fetchUsagePairDirect(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if fiveHour == nil {
		t.Fatal("five-hour window missing")
	}
	if fiveHour.WindowMinutes != 300 || fiveHour.UsedPercent != 31 || fiveHour.ResetsAt != 100 {
		t.Fatalf("five-hour=%+v", fiveHour)
	}
	if weekly.WindowMinutes != 10080 || weekly.UsedPercent != 47 || weekly.ResetsAt != 200 {
		t.Fatalf("weekly=%+v", weekly)
	}
}

func TestSelectCodexRateWindowsUsesFiveHourAndWeeklyDurations(t *testing.T) {
	fiveMins := int64(300)
	weekMins := int64(10080)
	fiveReset := int64(100)
	weekReset := int64(200)
	rr := rateResponse{
		RateLimits: rateSnapshot{
			Primary:   &rateWindow{UsedPercent: 12, WindowDurationMins: &fiveMins, ResetsAt: &fiveReset},
			Secondary: &rateWindow{UsedPercent: 34, WindowDurationMins: &weekMins, ResetsAt: &weekReset},
		},
	}
	fiveHour, weekly, err := selectCodexRateWindows(rr)
	if err != nil {
		t.Fatal(err)
	}
	if fiveHour == nil || weekly == nil {
		t.Fatalf("fiveHour=%+v weekly=%+v", fiveHour, weekly)
	}
	if fiveHour.UsedPercent != 12 || weekly.UsedPercent != 34 {
		t.Fatalf("fiveHour=%+v weekly=%+v", fiveHour, weekly)
	}
}

func TestUsageLinesShowFiveHourBeforeWeekly(t *testing.T) {
	oldColor := useColor
	useColor = false
	defer func() { useColor = oldColor }()

	now := time.Now()
	fiveHour := WeeklyUsage{
		UsedPercent:   20,
		WindowMinutes: 300,
		ResetsAt:      now.Add(3 * time.Hour).Unix(),
		FetchedAt:     now,
		Fresh:         true,
		WindowStarted: true,
	}
	weekly := WeeklyUsage{
		UsedPercent:   40,
		WindowMinutes: 10080,
		ResetsAt:      now.Add(4 * 24 * time.Hour).Unix(),
		FetchedAt:     now,
		Fresh:         true,
		WindowStarted: true,
	}
	lines := usageLines(UsageResult{FiveHour: &fiveHour, Usage: weekly})
	if len(lines) != 2 {
		t.Fatalf("lines=%q", lines)
	}
	if !strings.Contains(lines[0], "5 hour") || !strings.Contains(lines[0], "80.0% left") {
		t.Fatalf("five-hour line=%q", lines[0])
	}
	if !strings.Contains(lines[1], "weekly") || !strings.Contains(lines[1], "60.0% left") {
		t.Fatalf("weekly line=%q", lines[1])
	}
}
