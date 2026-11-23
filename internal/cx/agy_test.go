package cx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installTestAgy(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '1.5.14\n'
  exit
fi
[ "$1" = "-p" ] && [ "$2" = "/usage" ] || exit 2
if [ "$SSH_CONNECTION" = "127.0.0.1 1 127.0.0.1 22" ]; then
  dir="$HOME/.gemini/antigravity-cli"
  token="$dir/antigravity-oauth-token"
  if [ "$ANTIGRAVITY_REMOTE_OOB_AUTH" != "true" ]; then
    /bin/mkdir -p "$dir" || exit
    printf 'test credential' > "$token" || exit
  fi
  [ -s "$token" ] || exit 1
fi
printf '{"num_turns":0,"command":{"data":{"groups":[{"name":"Gemini Models","buckets":[{"window":"weekly","remaining_fraction":%s},{"window":"5h","remaining_fraction":0.5}]},{"name":"Claude and GPT models","buckets":[{"window":"weekly","remaining_fraction":0},{"window":"5h","remaining_fraction":1}]}]}}}\n' "${TEST_AGY_WEEKLY:-0.25}"
`
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ANTIGRAVITY_REMOTE_OOB_AUTH", "")
}

func TestAgyProfilesRemainSeparate(t *testing.T) {
	p := makeTestPaths(t)
	installTestAgy(t)
	stdin := os.Stdin
	terminal, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = terminal
	t.Cleanup(func() {
		os.Stdin = stdin
		terminal.Close()
	})
	a, err := addAgyAccount(p, "work", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := addAgyAccount(p, "personal", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("account directories overlap")
	}
	for _, account := range []Account{a, b} {
		r := fetchAgyUsage(p, account)
		if r.Err != "" || len(r.QuotaGroups) != 2 || r.QuotaGroups[0].Weekly.UsedPercent != 75 {
			t.Fatalf("quota for %s: %+v", account.Name, r)
		}
	}
	writeTestAccount(t, p, Account{ID: "codex", Name: "personal", AccountID: "test-codex"})
	if err := useAccount(p, []string{"codex/personal"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := useAccount(p, []string{"agy/personal"}, nil); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { handleCurrent(p) })
	if !strings.Contains(out, "personal") || !strings.Contains(out, "[Antigravity]") {
		t.Fatalf("current: %q", out)
	}
	withPipeStdin(t, "", func() {
		if err := useAccount(p, []string{"agy/"}, nil); err == nil || !strings.Contains(err.Error(), "pass an account name") {
			t.Fatalf("noninteractive picker: %v", err)
		}
	})
}

func TestDashboardOpensAndFoldsProviderGroups(t *testing.T) {
	oldColor := useColor
	useColor = true
	t.Cleanup(func() { useColor = oldColor })

	accounts := []Account{
		{ID: "primary", Name: "primary"},
		{ID: "backup", Name: "codex-backup"},
		{ID: "personal", Name: "personal", Provider: providerAgy},
		{ID: "work", Name: "work", Provider: providerAgy},
		{ID: "future", Name: "future-account", Provider: provider("claude")},
	}
	v := dashboardView{
		accounts: accounts,
		state:    State{ActiveID: "primary", ProviderAccounts: map[provider]string{providerAgy: "personal"}},
		size:     termSize{rows: 30, cols: 100},
	}
	for _, a := range accounts {
		v.results = append(v.results, UsageResult{Account: a})
	}
	frame, layout := renderDashboardFrame(v)
	for _, want := range []string{"▾ Codex", "▾ Antigravity", "▾ claude", "personal", "future-account"} {
		if !strings.Contains(plain(frame), want) {
			t.Fatalf("missing %q in %q", want, frame)
		}
	}
	if layout.headerRows[3] < 1 {
		t.Fatalf("open account is missing: %v", layout.headerRows)
	}
	press := func(key string) {
		t.Helper()
		captureStdout(t, func() {
			action, next := applyKey(&v, key, layout, true)
			if action != "" {
				t.Fatalf("%s started a switch: %s", key, action)
			}
			layout = next
		})
	}
	v.sel = 1
	press("left")
	frame, _ = renderDashboardFrame(v)
	if v.sel != 0 || !strings.Contains(plain(frame), "▸ Codex · default · primary · 2 accounts") || strings.Contains(frame, "codex-backup") {
		t.Fatalf("left did not fold Codex: cursor=%d, frame=%q", v.sel, frame)
	}
	press("right")
	frame, _ = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "▾ Codex") || !strings.Contains(frame, "codex-backup") {
		t.Fatalf("right did not open Codex: %q", frame)
	}
	v.sel = 2
	press("left")
	frame, layout = renderDashboardFrame(v)
	folded := strings.Split(frame, "\n")[layout.headerRows[v.sel]-1]
	if !strings.Contains(folded, cyan("▌")) || !strings.Contains(folded, bold("Antigravity")) || !strings.Contains(folded, green("personal")) {
		t.Fatalf("folded Antigravity heading should show its active account and cursor: %q", folded)
	}
	press("right")
	frame, layout = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "▾ Antigravity") || !strings.Contains(frame, "personal") || !strings.Contains(frame, "work") {
		t.Fatalf("right did not open Antigravity: %q", frame)
	}
	rows := strings.Split(frame, "\n")
	accountRow := layout.headerRows[v.sel] - 1
	labelColumn := cellIndex(rows[accountRow-1], "Antigravity")
	if cursorColumn := cellIndex(rows[accountRow], "▌"); labelColumn < 0 || cursorColumn != labelColumn {
		t.Fatalf("heading label column=%d, expanded cursor column=%d", labelColumn, cursorColumn)
	}
	if action, _ := applyKey(&v, "enter", layout, false); action != "switch" {
		t.Fatalf("selecting an expanded account: %s", action)
	}
	press("left")
	press("enter")
	frame, _ = renderDashboardFrame(v)
	if !strings.Contains(frame, "work") {
		t.Fatalf("enter did not reopen the provider: %q", frame)
	}
}

func TestDashboardShowsEachAgyQuotaGroupOnOneRow(t *testing.T) {
	a := Account{ID: "agy", Name: "primary", Provider: providerAgy}
	gemini := autoResult(a, 75, 37)
	gemini.FiveHour.ResetsAt = time.Now().Add(2 * time.Hour).Unix()
	claude := autoResult(a, 42, 90)
	argon := autoResult(a, 100, 100)
	r := UsageResult{
		Account: a,
		QuotaGroups: []quotaGroup{
			{Name: "Gemini Models", Weekly: gemini.Usage, FiveHour: gemini.FiveHour},
			{Name: "Claude and GPT models", Weekly: claude.Usage, FiveHour: claude.FiveHour},
			{Name: "Gemini 4 Argon", Weekly: argon.Usage},
		},
	}
	v := dashboardView{
		state:    State{DefaultProvider: providerAgy},
		accounts: []Account{a},
		results:  []UsageResult{r},
		size:     termSize{cols: 100},
	}
	frame, _ := renderDashboardFrame(v)
	plainFrame := plain(frame)
	rows := strings.Split(plainFrame, "\n")
	for _, want := range [][]string{
		{"5 hour", "weekly"},
		{"Gemini Models", "37.0% · in ", "75.0%"},
		{"Claude and GPT models", "90.0%", "42.0%"},
		{"Gemini 4 Argon", "—", "100.0%"},
	} {
		for _, line := range rows {
			if !strings.Contains(line, want[0]) {
				continue
			}
			for _, cell := range want[1:] {
				if !strings.Contains(line, cell) {
					t.Fatalf("missing %q in %q", cell, line)
				}
			}
			break
		}
		if strings.Count(plainFrame, want[0]) != 1 {
			t.Fatalf("expected one row with %q in %q", want[0], frame)
		}
	}
	v.size.cols = 45
	narrow, _ := renderDashboardFrame(v)
	if !strings.Contains(plain(narrow), "42.0%") || strings.Contains(plain(narrow), " · in ") {
		t.Fatalf("narrow dashboard quota: %q", narrow)
	}
	if lines := strings.Join(usageLines(r), "\n"); !strings.Contains(lines, "Gemini 4 Argon") || !strings.Contains(lines, "Gemini Models") {
		t.Fatalf("detailed quota is missing: %q", lines)
	}
}

func TestDashboardHighlightsActiveAccountsIndependently(t *testing.T) {
	oldColor := useColor
	useColor = true
	t.Cleanup(func() { useColor = oldColor })
	codex := Account{ID: "codex-account", Name: "codex-account"}
	agy := Account{ID: "agy-account", Name: "agy-account", Provider: providerAgy}
	backup := Account{ID: "agy-backup", Name: "agy-backup", Provider: providerAgy}
	v := dashboardView{
		state:    State{ActiveID: codex.ID, ProviderAccounts: map[provider]string{providerAgy: agy.ID}},
		accounts: []Account{codex, agy, backup},
		results:  []UsageResult{autoResult(codex, 50, 80), autoResult(agy, 50, 80), autoResult(backup, 30, 60)},
		sel:      2,
		size:     termSize{cols: 100},
	}
	for _, p := range []provider{providerCodex, providerAgy} {
		v.state.DefaultProvider = p
		frame, _ := renderDashboardFrame(v)
		if strings.Count(frame, green("●")) != 2 || strings.Count(frame, green(" 80.0% left")) != 2 {
			t.Fatalf("both active accounts should be colored: %q", frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if strings.Contains(line, "agy-backup") && (!strings.Contains(line, cyan("▌")) || !strings.Contains(line, "○ agy-backup")) {
				t.Fatalf("focused inactive account should have a cyan cursor and neutral text: %q", line)
			}
		}
	}
}

func TestDashboardKeepsAccountAndDefaultSelectionsSeparate(t *testing.T) {
	p := makeTestPaths(t)
	codex := Account{ID: "codex", Name: "work", AccountID: "codex-work"}
	personal := Account{ID: agyDefaultID, Name: "personal", Provider: providerAgy}
	work := Account{ID: "agy-work", Name: "work", Provider: providerAgy}
	for _, a := range []Account{codex, personal, work} {
		writeTestAccount(t, p, a)
	}
	if err := atomicWrite(agyCredentialPath(p, work), []byte("test credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range []Account{personal, codex} {
		if err := switchAccount(p, a); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := dashboardAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	var results []UsageResult
	for _, a := range accounts {
		results = append(results, UsageResult{Account: a})
	}
	v := newDashboardView(p, accounts, results, accountIndex(accounts, work.ID))
	v.foldedGroups = map[provider]bool{providerCodex: true}
	if _, err := applySwitch(p, &v); err != nil {
		t.Fatal(err)
	}
	if v.state.defaultAccountID() != codex.ID || v.state.selectedAccountID(providerAgy) != work.ID {
		t.Fatalf("unexpected state: %+v", v.state)
	}
	if v.accounts[0].Provider != providerAgy || !v.folded(providerCodex) || v.accounts[v.sel].ID != work.ID {
		t.Fatalf("the dashboard changed group order, folding, or cursor: %+v", v)
	}
	before, layout := renderDashboardFrame(v)
	if !strings.Contains(plain(before), "▸ Codex · default · work · 1 account") {
		t.Fatalf("missing the default marker on folded Codex: %q", before)
	}
	if action, _ := applyKey(&v, "d", layout, true); action != "default" {
		t.Fatalf("d action = %q", action)
	}
	if err := applyDefaultProvider(p, &v); err != nil {
		t.Fatal(err)
	}
	st, err := loadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.defaultAccountID() != work.ID || st.selectedAccountID(providerCodex) != codex.ID {
		t.Fatalf("unexpected saved state: %+v", st)
	}
	after, layout := renderDashboardFrame(v)
	agyHeading := strings.Index(plain(after), "▾ Antigravity · default")
	codexHeading := strings.Index(plain(after), "▸ Codex · work · 1 account")
	if agyHeading < 0 || codexHeading < agyHeading || v.accounts[v.sel].ID != work.ID {
		t.Fatalf("unexpected frame or cursor after setting the default: %q", after)
	}
	v.sel = accountIndex(v.accounts, codex.ID)
	if action, _ := applyKey(&v, "d", layout, false); action != "default" {
		t.Fatalf("d on folded group = %q", action)
	}
	if err := applyDefaultProvider(p, &v); err != nil {
		t.Fatal(err)
	}
	if v.state.defaultAccountID() != codex.ID || !v.folded(providerCodex) {
		t.Fatalf("setting the default should preserve folding: %+v", v)
	}
}

func TestSingleProviderDashboardIsFlat(t *testing.T) {
	for _, p := range []provider{providerCodex, providerAgy} {
		a := Account{ID: "primary", Name: "primary", Provider: p}
		b := Account{ID: "backup", Name: "backup", Provider: p}
		v := dashboardView{
			state:        State{DefaultProvider: p},
			accounts:     []Account{a, b},
			results:      []UsageResult{autoResult(a, 50, 80), autoResult(b, 45, 65)},
			foldedGroups: map[provider]bool{p: true},
		}
		v.state.selectAccount(a)
		frame, layout := renderDashboardFrame(v)
		if strings.ContainsAny(frame, "▾▸") || strings.Contains(frame, "set default") || strings.Contains(frame, "fold/open") {
			t.Fatalf("unexpected provider controls: %q", frame)
		}
		row := strings.Split(frame, "\n")[layout.headerRows[0]-1]
		if cellIndex(row, "▌") != 1 || strings.Contains(row, p.label()) || layout.headerRows[1] < 1 {
			t.Fatalf("unexpected single-provider account rows: %q", frame)
		}
		for _, key := range []string{"d", "left", "right"} {
			if action, _ := applyKey(&v, key, layout, false); action != "" {
				t.Fatalf("single provider handled group key %q as %q", key, action)
			}
		}
		if picker := renderAccountPicker(v.accounts, 0); strings.Contains(picker, p.label()) {
			t.Fatalf("unexpected provider label in filtered picker: %q", picker)
		}
	}
}
