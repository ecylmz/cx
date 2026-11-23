package cx

import (
	"os"
	"strings"
	"testing"
)

func TestDashboardMovesAccountBetweenSubscriptions(t *testing.T) {
	p := makeTestPaths(t)
	work := Account{ID: "work", Name: "work", Subscription: codexPlus}
	backup := Account{ID: "backup", Name: "backup", Subscription: newSubscription(providerCodex, "pro-200")}
	for _, a := range []Account{work, backup} {
		writeTestAccount(t, p, a)
	}
	if err := saveState(p, State{CodexAccountID: work.ID}); err != nil {
		t.Fatal(err)
	}
	accounts, err := dashboardAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	v := dashboardView{
		state: State{CodexAccountID: work.ID}, accounts: accounts,
		results: []UsageResult{{Account: accounts[0]}, {Account: accounts[1]}},
		size:    termSize{rows: 26, cols: 120},
	}
	frame, layout := renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "Codex") || !strings.Contains(plain(frame), "Plus") || !strings.Contains(plain(frame), "Pro 200") {
		t.Fatalf("expected two groups for Codex: %q", frame)
	}
	press := func(key string) string {
		t.Helper()
		var action string
		captureStdout(t, func() { action, layout = applyKey(&v, key, layout, false) })
		return action
	}
	if press("s") != "" {
		t.Fatal("opening the menu changed the account")
	}
	menu, _ := renderDashboardFrame(v)
	for _, want := range []string{"Plus", "Pro 100", "Pro 500", "Business Premium", "enter save"} {
		if !strings.Contains(plain(menu), want) {
			t.Fatalf("missing %q in the subscription menu: %q", want, menu)
		}
	}
	press("down")
	if press("enter") != "subscription" {
		t.Fatal("selecting a subscription did not request an update")
	}
	if _, err := applySubscription(p, &v); err != nil {
		t.Fatal(err)
	}
	frame, _ = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "Pro 100 · default") || v.accounts[v.sel].ID != work.ID {
		t.Fatalf("the account did not move to the default Pro 100 group: %q", frame)
	}
	updated, err := findAccount(p, "work", nil)
	if err != nil || updated.subscription().tier() != "pro-100" {
		t.Fatalf("saved account=%+v err=%v", updated, err)
	}
}

func TestLoadExistingAccountSubscription(t *testing.T) {
	p := makeTestPaths(t)
	const old = `{"id":"work","name":"work","provider":"agy","plan":"pro"}`
	if err := os.MkdirAll(p.accountDir("work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.accountMeta("work"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	account, err := findAccount(p, "work", nil)
	if err != nil || account.subscription() != agyPro {
		t.Fatalf("legacy account=%+v err=%v", account, err)
	}
}

func TestLoadExistingDefaultSubscription(t *testing.T) {
	p := makeTestPaths(t)
	const old = `{"active_id":"codex-work","active_provider":"agy","provider_accounts":{"agy":"agy-work"}}`
	if err := os.WriteFile(p.statePath(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := loadState(p)
	if err != nil || st.defaultAccountID() != "agy-work" || st.activeAccountID(providerCodex) != "codex-work" {
		t.Fatalf("legacy selection=%+v err=%v", st, err)
	}
}
