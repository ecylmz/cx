package cx

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectWeeklyWindowUsesLongest(t *testing.T) {
	a := int64(300)
	b := int64(10080)
	ra := int64(10)
	rb := int64(20)
	rr := rateResponse{RateLimits: rateSnapshot{Primary: &rateWindow{UsedPercent: 5, WindowDurationMins: &a, ResetsAt: &ra}, Secondary: &rateWindow{UsedPercent: 60, WindowDurationMins: &b, ResetsAt: &rb}}}
	w, err := selectWeeklyWindow(rr)
	if err != nil {
		t.Fatal(err)
	}
	if w.UsedPercent != 60 {
		t.Fatalf("got %v", w.UsedPercent)
	}
}
func TestParseAuthIdentity(t *testing.T) {
	dir := t.TempDir()
	payload := map[string]any{"email": "a@example.com", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct", "chatgpt_plan_type": "plus"}}
	b, _ := json.Marshal(payload)
	tok := "x." + base64.RawURLEncoding.EncodeToString(b) + ".y"
	auth := map[string]any{"tokens": map[string]any{"id_token": tok, "access_token": "access", "refresh_token": "refresh"}}
	raw, _ := json.Marshal(auth)
	p := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(p, raw, 0600)
	id, err := parseAuth(p)
	if err != nil {
		t.Fatal(err)
	}
	if id.AccountID != "acct" || id.Email != "a@example.com" || id.Plan != "plus" {
		t.Fatalf("bad identity: %+v", id)
	}
}

func TestSameIDAndReadResponse(t *testing.T) {
	if !sameID(float64(2), 2) || !sameID(json.Number("2"), 2) || !sameID("2", 2) || sameID(true, 2) {
		t.Fatal("sameID variants")
	}
	s := bufio.NewScanner(strings.NewReader("junk\n{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{\"ok\":true}}\n"))
	e, err := readResponse(s, 2)
	if err != nil || len(e.Result) == 0 {
		t.Fatalf("response=%+v err=%v", e, err)
	}
	s = bufio.NewScanner(strings.NewReader("{\"id\":2,\"error\":{\"code\":401,\"message\":\"nope\"}}\n"))
	if _, err := readResponse(s, 2); err == nil || !strings.Contains(err.Error(), "401: nope") {
		t.Fatalf("expected rpc error, got %v", err)
	}
	s = bufio.NewScanner(strings.NewReader(""))
	if _, err := readResponse(s, 2); err == nil {
		t.Fatal("expected EOF")
	}
}

func TestSelectWeeklyWindowFallbacks(t *testing.T) {
	if _, err := selectWeeklyWindow(rateResponse{}); err == nil {
		t.Fatal("expected no-window error")
	}
	mins := int64(10080)
	reset := int64(123)
	rr := rateResponse{RateLimitsByLimitID: map[string]rateSnapshot{"codex": {Primary: &rateWindow{UsedPercent: 42, WindowDurationMins: &mins, ResetsAt: &reset}}}}
	w, err := selectWeeklyWindow(rr)
	if err != nil || w.UsedPercent != 42 {
		t.Fatalf("window=%+v err=%v", w, err)
	}
}

func TestAnnotateAndRememberWeeklyReset(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	now := time.Now()
	r := UsageResult{Account: a, Usage: WeeklyUsage{UsedPercent: 10, ResetsAt: now.Add(24 * time.Hour).Unix(), FetchedAt: now}}
	annotateWindowState(p, &r)
	if !r.Usage.WindowStarted || r.Account.WeeklyResetAt != r.Usage.ResetsAt {
		t.Fatalf("annotated=%+v", r)
	}
	before := r.Account.UpdatedAt
	got, err := rememberWeeklyReset(p, r.Account, r.Account.WeeklyResetAt)
	if err != nil || got.WeeklyResetAt != r.Account.WeeklyResetAt || !got.UpdatedAt.Equal(before) {
		t.Fatalf("same reset=%+v err=%v", got, err)
	}
	got, err = rememberWeeklyReset(p, got, 0)
	if err != nil || got.WeeklyResetAt == 0 {
		t.Fatalf("zero reset changed account: %+v err=%v", got, err)
	}

	fixed := now.Add(5 * 24 * time.Hour).Unix()
	r = UsageResult{Account: Account{ID: "a", Name: "primary", AccountID: "acct", WeeklyResetAt: fixed}, Usage: WeeklyUsage{UsedPercent: 0, WindowMinutes: 10080, ResetsAt: fixed + 30, FetchedAt: now}}
	annotateWindowState(p, &r)
	if !r.Usage.WindowStarted {
		t.Fatal("known fixed reset should mark window started")
	}
}

func TestFetchAllUsageReportsMissingCodex(t *testing.T) {
	p := makeTestPaths(t)
	t.Setenv("PATH", t.TempDir())
	rs := fetchAllUsage(p, []Account{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}})
	if len(rs) != 2 || rs[0].Err == "" || rs[1].Err == "" {
		t.Fatalf("results=%+v", rs)
	}
}

func TestUsageConcurrencyKeepsSmallAccountSetsParallel(t *testing.T) {
	if usageConcurrency < 5 {
		t.Fatalf("usageConcurrency=%d; five-account dashboard should fetch in one wave", usageConcurrency)
	}
	if primeConcurrency < 4 {
		t.Fatalf("primeConcurrency=%d; first-time weekly-window starts should not serialize four accounts", primeConcurrency)
	}
	if usageTimeout > 6*time.Second {
		t.Fatalf("usageTimeout=%s; one stalled account should not block status too long", usageTimeout)
	}
}
