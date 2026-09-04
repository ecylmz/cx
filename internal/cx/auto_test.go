package cx

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func autoResult(account Account, weeklyLeft, fiveHourLeft float64) UsageResult {
	fiveHour := WeeklyUsage{UsedPercent: 100 - fiveHourLeft, WindowMinutes: 300, Fresh: true}
	return UsageResult{
		Account:  account,
		Usage:    WeeklyUsage{UsedPercent: 100 - weeklyLeft, WindowMinutes: 10080, Fresh: true},
		FiveHour: &fiveHour,
	}
}

func TestSelectAutoAccount(t *testing.T) {
	created := time.Unix(1_700_000_000, 0)
	active := Account{ID: "active", Name: "active", CreatedAt: created}
	first := Account{ID: "first", Name: "first", CreatedAt: created.Add(time.Minute)}
	second := Account{ID: "second", Name: "second", CreatedAt: created.Add(2 * time.Minute)}

	tests := []struct {
		name      string
		results   []UsageResult
		activeID  string
		want      string
		wantKeep  bool
		wantErr   string
		exhausted bool
	}{
		{
			name:     "keep usable active account",
			results:  []UsageResult{autoResult(active, 20, 10), autoResult(first, 5, 5)},
			activeID: active.ID,
			want:     active.ID,
			wantKeep: true,
		},
		{
			name:     "switch when active weekly quota is exhausted",
			results:  []UsageResult{autoResult(active, 0, 50), autoResult(first, 40, 40)},
			activeID: active.ID,
			want:     first.ID,
		},
		{
			name:     "treat unused windows as available",
			results:  []UsageResult{autoResult(active, 40, 0), autoResult(first, 100, 100)},
			activeID: active.ID,
			want:     first.ID,
		},
		{
			name:     "prefer least weekly quota remaining",
			results:  []UsageResult{autoResult(active, 40, 0), autoResult(second, 60, 10), autoResult(first, 20, 90)},
			activeID: active.ID,
			want:     first.ID,
		},
		{
			name:     "break weekly tie with 5-hour quota",
			results:  []UsageResult{autoResult(active, 40, 0), autoResult(first, 20, 70), autoResult(second, 20, 30)},
			activeID: active.ID,
			want:     second.ID,
		},
		{
			name:     "break quota tie with creation order",
			results:  []UsageResult{autoResult(active, 40, 0), autoResult(second, 20, 30), autoResult(first, 20, 30)},
			activeID: active.ID,
			want:     first.ID,
		},
		{
			name:      "report exhausted pool",
			results:   []UsageResult{autoResult(active, 40, 0), autoResult(first, 0, 30), autoResult(second, 20, 0)},
			activeID:  active.ID,
			exhausted: true,
		},
		{
			name: "reject unavailable quota",
			results: []UsageResult{
				autoResult(active, 40, 0),
				{Account: first, Err: "offline"},
				autoResult(second, 20, 30),
			},
			activeID: active.ID,
			wantErr:  "quota unavailable for first: offline",
		},
		{
			name: "reject missing quota window",
			results: []UsageResult{
				autoResult(active, 40, 0),
				{Account: first, Usage: WeeklyUsage{UsedPercent: 80, WindowMinutes: 10080, Fresh: true}},
			},
			activeID: active.ID,
			wantErr:  "quota unavailable for first: 5-hour quota is unavailable",
		},
		{
			name:     "reject missing active account",
			results:  []UsageResult{autoResult(first, 20, 30)},
			activeID: active.ID,
			wantErr:  "no active account",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keep, err := selectAutoAccount(tt.results, tt.activeID)
			if tt.exhausted {
				if !errors.Is(err, errAllAccountsExhausted) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if err != nil || got.ID != tt.want || keep != tt.wantKeep {
				t.Fatalf("account=%q keep=%t err=%v", got.ID, keep, err)
			}
		})
	}
}

func TestHandleAutoRequiresManagedActiveAccount(t *testing.T) {
	p := makeTestPaths(t)
	if err := handleAuto(p); err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Fatalf("err=%v", err)
	}

	account := Account{ID: "a", Name: "primary", AccountID: "acct-a"}
	writeTestAccount(t, p, account)
	if err := handleAuto(p); err == nil || !strings.Contains(err.Error(), "no active account") {
		t.Fatalf("err=%v", err)
	}
}

func TestHandleAutoKeepsThenSwitchesWithoutStartingWindows(t *testing.T) {
	p := makeTestPaths(t)
	created := time.Unix(1_700_000_000, 0)
	primary := Account{ID: "a", Name: "primary", AccountID: "acct-a", CreatedAt: created, UpdatedAt: created}
	backup := Account{ID: "b", Name: "backup", AccountID: "acct-b", CreatedAt: created.Add(time.Minute), UpdatedAt: created}
	writeTestAccount(t, p, primary)
	writeTestAccount(t, p, backup)
	if err := saveState(p, State{ActiveID: primary.ID}); err != nil {
		t.Fatal(err)
	}

	type usage struct{ weekly, fiveHour float64 }
	quotas := map[string]usage{
		"acct-a": {weekly: 0, fiveHour: 0},
		"acct-b": {weekly: 95, fiveHour: 90},
	}
	var starts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/usage", func(w http.ResponseWriter, r *http.Request) {
		q := quotas[r.Header.Get("ChatGPT-Account-Id")]
		now := time.Now().Unix()
		_, _ = fmt.Fprintf(w, `{"rate_limit":{"primary_window":{"used_percent":%g,"limit_window_seconds":18000,"reset_at":%d},"secondary_window":{"used_percent":%g,"limit_window_seconds":604800,"reset_at":%d}}}`, q.fiveHour, now+18000, q.weekly, now+604800)
	})
	mux.HandleFunc("/reset-credits", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"credits":[]}`))
	})
	mux.HandleFunc("/responses", func(http.ResponseWriter, *http.Request) {
		starts.Add(1)
	})
	mux.HandleFunc("/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-mini","visibility":"list","priority":20,"supported_in_api":true}]}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	oldUsage, oldCredits, oldResponses, oldModels := directUsageEndpoint, directResetCreditsEndpoint, directResponsesEndpoint, directModelsEndpoint
	oldUsageClient, oldPrimeClient := directUsageHTTPClient, primeHTTPClient
	directUsageEndpoint = server.URL + "/usage"
	directResetCreditsEndpoint = server.URL + "/reset-credits"
	directResponsesEndpoint = server.URL + "/responses"
	directModelsEndpoint = server.URL + "/models"
	directUsageHTTPClient = server.Client()
	primeHTTPClient = server.Client()
	defer func() {
		directUsageEndpoint, directResetCreditsEndpoint, directResponsesEndpoint, directModelsEndpoint = oldUsage, oldCredits, oldResponses, oldModels
		directUsageHTTPClient, primeHTTPClient = oldUsageClient, oldPrimeClient
	}()
	t.Setenv("PATH", t.TempDir())

	var err error
	out := captureStdout(t, func() { err = handleAuto(p) })
	if err != nil || !strings.Contains(out, "keeping primary") {
		t.Fatalf("out=%q err=%v", out, err)
	}

	quotas["acct-a"] = usage{weekly: 100, fiveHour: 80}
	out = captureStdout(t, func() { err = handleAuto(p) })
	if err != nil || !strings.Contains(out, "switched to backup") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	state, err := loadState(p)
	if err != nil || state.ActiveID != backup.ID {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if starts.Load() != 0 {
		t.Fatalf("window starts=%d", starts.Load())
	}
}
