package cx

import (
	"testing"
	"time"
)

func TestFiveHourKnownResetMarksZeroUsageWindowStarted(t *testing.T) {
	p := makeTestPaths(t)
	now := time.Now()
	knownReset := now.Add(4 * time.Hour).Unix()
	a := Account{
		ID:              "a",
		Name:            "primary",
		AccountID:       "acct",
		FiveHourResetAt: knownReset,
	}
	writeTestAccount(t, p, a)

	fiveHour := &WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 300,
		ResetsAt:      knownReset + 30,
		FetchedAt:     now,
	}
	r := UsageResult{
		Account:  a,
		Usage:    WeeklyUsage{WindowMinutes: 10080, WindowStarted: true},
		FiveHour: fiveHour,
	}

	annotateFiveHourWindowState(p, &r)
	if !r.FiveHour.WindowStarted {
		t.Fatal("known five-hour reset should keep a zero-percent window marked started")
	}
}

func TestFiveHourStateMigratesFromMatchingWeeklyStart(t *testing.T) {
	p := makeTestPaths(t)
	now := time.Now()
	startedAt := now.Add(-5 * time.Second)
	weeklyReset := startedAt.Add(7 * 24 * time.Hour).Unix()
	fiveHourReset := startedAt.Add(5 * time.Hour).Unix()
	a := Account{
		ID:            "a",
		Name:          "primary",
		AccountID:     "acct",
		WeeklyResetAt: weeklyReset,
	}
	writeTestAccount(t, p, a)

	fiveHour := &WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 300,
		ResetsAt:      fiveHourReset,
		FetchedAt:     now,
	}
	if !looksLikeUnstartedWindow(*fiveHour, now) {
		t.Fatal("test setup should look unstarted before persisted-state inference")
	}
	r := UsageResult{
		Account: a,
		Usage: WeeklyUsage{
			WindowMinutes: 10080,
			ResetsAt:      weeklyReset,
			FetchedAt:     now,
			WindowStarted: true,
		},
		FiveHour: fiveHour,
	}

	annotateFiveHourWindowState(p, &r)
	if !r.FiveHour.WindowStarted {
		t.Fatal("matching weekly/five-hour starts should mark the five-hour window started")
	}
	if r.Account.FiveHourResetAt != fiveHourReset {
		t.Fatalf("five-hour reset was not remembered: %+v", r.Account)
	}

	var persisted Account
	if err := readJSON(p.accountMeta(a.ID), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.FiveHourResetAt != fiveHourReset || persisted.WeeklyResetAt != weeklyReset {
		t.Fatalf("persisted reset state=%+v", persisted)
	}
}

func TestFiveHourStateDoesNotReuseExpiredWeeklyReset(t *testing.T) {
	p := makeTestPaths(t)
	now := time.Now()
	a := Account{
		ID:            "a",
		Name:          "primary",
		AccountID:     "acct",
		WeeklyResetAt: now.Add(-time.Hour).Unix(),
	}
	writeTestAccount(t, p, a)
	fiveHour := &WeeklyUsage{
		UsedPercent:   0,
		WindowMinutes: 300,
		ResetsAt:      now.Add(5 * time.Hour).Unix(),
		FetchedAt:     now,
	}
	r := UsageResult{Account: a, Usage: WeeklyUsage{WindowMinutes: 10080}, FiveHour: fiveHour}

	annotateFiveHourWindowState(p, &r)
	if r.FiveHour.WindowStarted {
		t.Fatal("an expired weekly reset must not make a fresh five-hour window look started")
	}
}
