package cx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchUsageDirectUsesCodexBackendContract(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct", Subscription: codexPlus}
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
			"plan_type":"free",
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

	five, u, err := fetchUsagePair(p, &a)
	if err != nil {
		t.Fatal(err)
	}
	if a.subscription() != codexPlus || five == nil {
		t.Fatalf("account=%+v five=%+v", a, five)
	}
	var saved Account
	if err := readJSON(p.accountMeta(a.ID), &saved); err != nil || saved.subscription() != codexPlus {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if u.UsedPercent != 37 || u.WindowMinutes != 10080 || u.ResetsAt != 200 || !u.Fresh {
		t.Fatalf("usage=%+v", u)
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

	_, u, err := fetchUsagePair(p, &a)
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedPercent != 4 || u.WindowMinutes != 10080 || u.ResetsAt != 321 {
		t.Fatalf("usage=%+v", u)
	}
}
