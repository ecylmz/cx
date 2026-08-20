package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
