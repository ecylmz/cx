package cx

import "testing"

func TestQuotaPrimingNeededStartsFiveHourWhenWeeklyAlreadyStarted(t *testing.T) {
	r := UsageResult{
		Usage: WeeklyUsage{WindowMinutes: 10080, WindowStarted: true},
		FiveHour: &WeeklyUsage{WindowMinutes: 300, WindowStarted: false},
	}
	needed, status := quotaPrimingNeeded(r)
	if !needed {
		t.Fatal("five-hour window should be primed when dashboard sees it unstarted")
	}
	if status != "starting 5-hour quota window…" {
		t.Fatalf("status=%q", status)
	}
}

func TestQuotaPrimingNeededStartsBothWindows(t *testing.T) {
	r := UsageResult{
		Usage: WeeklyUsage{WindowMinutes: 10080, WindowStarted: false},
		FiveHour: &WeeklyUsage{WindowMinutes: 300, WindowStarted: false},
	}
	needed, status := quotaPrimingNeeded(r)
	if !needed || status != "starting 5-hour + weekly quota windows…" {
		t.Fatalf("needed=%v status=%q", needed, status)
	}
}

func TestQuotaPrimingNeededSkipsActiveWindows(t *testing.T) {
	r := UsageResult{
		Usage: WeeklyUsage{WindowMinutes: 10080, WindowStarted: true},
		FiveHour: &WeeklyUsage{WindowMinutes: 300, WindowStarted: true},
	}
	needed, status := quotaPrimingNeeded(r)
	if needed || status != "" {
		t.Fatalf("needed=%v status=%q", needed, status)
	}
}

func TestQuotaPrimingNeededSkipsMissingFiveHourWhenWeeklyActive(t *testing.T) {
	r := UsageResult{Usage: WeeklyUsage{WindowMinutes: 10080, WindowStarted: true}}
	needed, status := quotaPrimingNeeded(r)
	if needed || status != "" {
		t.Fatalf("needed=%v status=%q", needed, status)
	}
}
