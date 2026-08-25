package cx

import (
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

func TestBankedResetLinesShowExpiry(t *testing.T) {
	t.Setenv("LC_TIME", "tr_TR.UTF-8")
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	line := bankedExpiryText("2026-08-24T12:00:00Z", now)
	if !strings.Contains(line, "24.08.2026 15:00") && !strings.Contains(line, "24.08.2026 12:00") {
		t.Fatalf("expiry line=%q", line)
	}
	if !strings.Contains(line, "in 3d") {
		t.Fatalf("expiry line=%q", line)
	}

	lines := bankedResetLines(UsageResult{
		BankedLoaded: true,
		BankedResets: []BankedReset{{ID: "a", Title: "Full reset", ExpiresAt: "2026-08-24T12:00:00Z"}},
	}, "  ")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "banked resets  1") || !strings.Contains(joined, "Full reset") || (!strings.Contains(joined, "expires") && !strings.Contains(joined, "expired")) {
		t.Fatalf("lines=%q", joined)
	}
}
