package cx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchUsageDirectUsesCodexBackendContract(t *testing.T) {
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
			"plan_type":"plus",
			"rate_limit":{
				"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_at":100},
				"secondary_window":{"used_percent":37,"limit_window_seconds":604800,"reset_at":200}
			}
		}`))
	}))
	defer server.Close()

	oldEndpoint, oldClient := directUsageEndpoint, directUsageHTTPClient
	directUsageEndpoint, directUsageHTTPClient = server.URL, server.Client()
	defer func() {
		directUsageEndpoint, directUsageHTTPClient = oldEndpoint, oldClient
	}()

	u, err := fetchUsageDirect(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedPercent != 37 || u.WindowMinutes != 10080 || u.ResetsAt != 200 || !u.Fresh {
		t.Fatalf("usage=%+v", u)
	}
}

func TestSelectDirectWeeklyWindowIncludesAdditionalLimits(t *testing.T) {
	payload := directUsagePayload{
		RateLimit: &directRateLimit{PrimaryWindow: &directRateWindow{UsedPercent: 1, LimitWindowSeconds: 300}},
		AdditionalRateLimits: []directAdditionalRateLimit{{
			RateLimit: &directRateLimit{SecondaryWindow: &directRateWindow{UsedPercent: 9, LimitWindowSeconds: 604800, ResetAt: 123}},
		}},
	}
	w, err := selectDirectWeeklyWindow(payload)
	if err != nil {
		t.Fatal(err)
	}
	if w.UsedPercent != 9 || w.ResetAt != 123 {
		t.Fatalf("window=%+v", w)
	}
}

func TestFetchUsageDoesNotRequireCodexWhenDirectReadWorks(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":4,"limit_window_seconds":604800,"reset_at":321}}}`))
	}))
	defer server.Close()

	oldEndpoint, oldClient := directUsageEndpoint, directUsageHTTPClient
	directUsageEndpoint, directUsageHTTPClient = server.URL, server.Client()
	defer func() {
		directUsageEndpoint, directUsageHTTPClient = oldEndpoint, oldClient
	}()
	t.Setenv("PATH", t.TempDir())

	u, err := fetchUsage(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedPercent != 4 || u.WindowMinutes != 10080 || u.ResetsAt != 321 {
		t.Fatalf("usage=%+v", u)
	}
}
