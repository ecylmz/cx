package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewIDLooksRandomAndStableLength(t *testing.T) {
	a, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 16 || len(b) != 16 || a == b {
		t.Fatalf("ids=%q %q", a, b)
	}
}

func TestAtomicWriteAndJSONRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "x.json")
	want := map[string]string{"a": "b"}
	if err := writeJSON(path, want); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := readJSON(path, &got); err != nil {
		t.Fatal(err)
	}
	if got["a"] != "b" {
		t.Fatalf("got=%v", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", st.Mode().Perm())
	}
}

func TestValidateAndFindAccountSelectors(t *testing.T) {
	for _, good := range []string{"primary", "backup_2", "work.test"} {
		if err := validateName(good); err != nil {
			t.Fatalf("%q rejected: %v", good, err)
		}
	}
	for _, bad := range []string{"", "bad name", "a/b", "ç"} {
		if err := validateName(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	as := []Account{
		{ID: "aaa111", Name: "primary", Email: "a@example.com"},
		{ID: "bbb222", Name: "backup-one", Email: "b@example.com"},
		{ID: "ccc333", Name: "backup-two", Email: "c@example.com"},
	}
	for _, sel := range []string{"primary", "AAA111", "a@example.com", "bbb"} {
		if _, err := findIn(as, sel); err != nil {
			t.Fatalf("selector %q: %v", sel, err)
		}
	}
	if _, err := findIn(as, "backup"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous error, got %v", err)
	}
	if _, err := findIn(as, "missing"); err == nil {
		t.Fatal("expected missing error")
	}
}

func TestListAccountsSortingStateAndCache(t *testing.T) {
	p := makeTestPaths(t)
	old := time.Now().Add(-time.Hour)
	newer := time.Now()
	writeTestAccount(t, p, Account{ID: "a", Name: "alpha", AccountID: "acct-a", LastUsedAt: &old})
	writeTestAccount(t, p, Account{ID: "b", Name: "beta", AccountID: "acct-b", LastUsedAt: &newer})
	writeTestAccount(t, p, Account{ID: "c", Name: "charlie", AccountID: "acct-c"})
	if err := os.MkdirAll(filepath.Join(p.AccountsRoot, ".staging"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.AccountsRoot, "broken"), 0700); err != nil {
		t.Fatal(err)
	}
	as, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 3 || as[0].ID != "b" || as[1].ID != "a" || as[2].ID != "c" {
		t.Fatalf("order=%+v", as)
	}
	if err := saveState(p, State{ActiveID: "b"}); err != nil {
		t.Fatal(err)
	}
	st, err := loadState(p)
	if err != nil || st.ActiveID != "b" {
		t.Fatalf("state=%+v err=%v", st, err)
	}
	missing := makeTestPaths(t)
	st, err = loadState(missing)
	if err != nil || st.ActiveID != "" {
		t.Fatalf("missing state=%+v err=%v", st, err)
	}

	now := time.Now()
	rs := []UsageResult{{Account: as[0], Usage: WeeklyUsage{UsedPercent: 12, FetchedAt: now}, Err: ""}, {Account: as[1], Err: "boom"}}
	saveFreshCache(p, rs)
	cache, err := loadCache(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache) != 1 || cache["b"].UsedPercent != 12 {
		t.Fatalf("cache=%+v", cache)
	}
}

func TestRenameAndRemoveAccount(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "alpha", AccountID: "acct-a", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	b := Account{ID: "b", Name: "beta", AccountID: "acct-b", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	writeTestAccount(t, p, b)
	if err := renameAccount(p, "alpha", "renamed"); err != nil {
		t.Fatal(err)
	}
	got, err := findAccount(p, "renamed")
	if err != nil || got.ID != "a" {
		t.Fatalf("renamed=%+v err=%v", got, err)
	}
	if err := renameAccount(p, "renamed", "beta"); err == nil {
		t.Fatal("expected name collision")
	}
	if err := saveState(p, State{ActiveID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := removeAccount(p, "renamed"); err == nil {
		t.Fatal("expected active-account removal rejection")
	}
	if err := removeAccount(p, "beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.accountDir("b")); !os.IsNotExist(err) {
		t.Fatalf("account dir still exists: %v", err)
	}
}
