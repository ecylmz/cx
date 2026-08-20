package main

import (
	"testing"
	"time"
)

func TestLocalDateLayout(t *testing.T) {
	tests := map[string]string{
		"tr_TR":       "02.01.2006 15:04",
		"tr_TR.UTF-8": "02.01.2006 15:04",
		"en_US":       "01/02/2006 3:04 PM",
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
