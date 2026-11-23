package cx

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
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
    printf '{"id_token_email":"%s","token":{"refresh_token":"test"}}' "${TEST_AGY_EMAIL:-test@example.com}" > "$token" || exit
  fi
  [ -s "$token" ] || exit 1
fi
printf '{"num_turns":0,"plan_tier":"%s","command":{"data":{"groups":[{"name":"Gemini Models","buckets":[{"window":"weekly","remaining_fraction":%s},{"window":"5h","remaining_fraction":0.5,"reset_time":"%s"}]},{"name":"Claude and GPT models","buckets":[{"window":"weekly","remaining_fraction":0},{"window":"5h","remaining_fraction":1}]}]}}}\n' "${TEST_AGY_TIER:-}" "${TEST_AGY_WEEKLY:-0.25}" "${TEST_AGY_FIVE_RESET:-}"
`
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("ANTIGRAVITY_REMOTE_OOB_AUTH", "")
	installTestAgyKeyring(t)
}

func installTestAgyKeyring(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	keyring := filepath.Join(t.TempDir(), "keyring")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_AGY_KEYRING", keyring)
	for tool, script := range map[string]string{
		"secret-tool": `#!/bin/sh
case "$1" in
lookup) [ -f "$TEST_AGY_KEYRING" ] || exit 1; /bin/cat "$TEST_AGY_KEYRING" ;;
store) [ -z "$TEST_AGY_KEYRING_FAIL" ] || exit 1; /bin/cat > "$TEST_AGY_KEYRING" ;;
clear) /bin/rm "$TEST_AGY_KEYRING" ;;
*) exit 2 ;;
esac
`,
		"security": `#!/bin/sh
case "$1" in
find-generic-password) [ -f "$TEST_AGY_KEYRING" ] || exit 44; /bin/cat "$TEST_AGY_KEYRING" ;;
-i)
  [ -z "$TEST_AGY_KEYRING_FAIL" ] || exit 1
  IFS= read -r line || exit
  case "$line" in
    "add-generic-password -U -s gemini -a antigravity -X "*)
      printf '%s\n' "${line##* }" > "$TEST_AGY_KEYRING" ;;
    *) exit 2 ;;
  esac ;;
delete-generic-password) /bin/rm "$TEST_AGY_KEYRING" ;;
*) exit 2 ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return keyring
}

func TestAgySwitchUpdatesNativeLogin(t *testing.T) {
	p := makeTestPaths(t)
	keyringPath := installTestAgyKeyring(t)
	const primary = `{"id_token_email":"primary@example.com","token":{"refresh_token":"original"}}`
	const refreshed = `{"id_token_email":"primary@example.com","token":{"refresh_token":"refreshed"}}`
	const work = `{"id_token_email":"work@example.com","token":{"refresh_token":"work"}}`
	for _, account := range []struct {
		name, credential string
	}{{"primary", primary}, {"work", work}} {
		a := Account{ID: "agy-" + account.name, Name: account.name, Subscription: agyPro}
		writeTestAccount(t, p, a)
		if err := atomicWrite(agyCredentialPath(p, a), []byte(account.credential), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(keyringPath, []byte(refreshed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := useAccount(p, []string{"work", "--provider", "agy"}); err != nil {
		t.Fatal(err)
	}
	keyring := agyKeyring{platform: runtime.GOOS}
	if got, err := keyring.read(); err != nil || string(got) != work {
		t.Fatal("direct agy would not see the work credential")
	}
	t.Setenv("TEST_AGY_KEYRING_FAIL", "1")
	if err := useAccount(p, []string{"primary", "--provider", "agy"}); err == nil {
		t.Fatal("switch succeeded with a failed keyring write")
	}
	out := captureStdout(t, func() { handleCurrent(p) })
	if !strings.Contains(out, "work") {
		t.Fatalf("active account changed after a failed keyring write: %q", out)
	}
	t.Setenv("TEST_AGY_KEYRING_FAIL", "")
	if err := useAccount(p, []string{"primary", "--provider", "agy"}); err != nil {
		t.Fatal(err)
	}
	if got, err := keyring.read(); err != nil || string(got) != refreshed {
		t.Fatal("switching back did not restore the refreshed credential")
	}
}

func TestAgyMacKeyringReadsAndUpdatesCredentials(t *testing.T) {
	keyringPath := installTestAgyKeyring(t)
	keyring := agyKeyring{platform: "darwin"}
	const original = `{"id_token_email":"first@example.com","token":{"refresh_token":"first"}}`
	encoded := "go-keyring-base64:" + base64.StdEncoding.EncodeToString([]byte(original))
	if err := os.WriteFile(keyringPath, []byte(encoded), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := keyring.read(); err != nil || string(got) != original {
		t.Fatal("could not read the existing Keychain credential")
	}
	const target = `{"id_token_email":"second@example.com","token":{"refresh_token":"second"}}`
	if err := keyring.write([]byte(target)); err != nil {
		t.Fatal(err)
	}
	if got, err := keyring.read(); err != nil || string(got) != target {
		t.Fatal("could not read the updated Keychain credential")
	}
}

func TestAddAgyChecksExplicitSubscription(t *testing.T) {
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
	t.Setenv("TEST_AGY_TIER", "Google AI Pro (Trial)")
	if err := registerAccount(p, "add", []string{"work", "--provider", "agy", "--subscription", "pro"}); err == nil || !strings.Contains(err.Error(), "subscription mismatch") {
		t.Fatalf("expected a mismatch with Pro Trial, got %v", err)
	}
	t.Setenv("TEST_AGY_TIER", "")
	t.Setenv("TEST_AGY_FIVE_RESET", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if err := registerAccount(p, "add", []string{"work", "--provider", "agy", "--subscription", "free"}); err == nil || !strings.Contains(err.Error(), "active five-hour quota") {
		t.Fatalf("expected a mismatch with an active five-hour quota, got %v", err)
	}
	accounts, err := listAccountsFor(p, providerAgy)
	if err != nil || len(accounts) != 0 {
		t.Fatalf("mismatched signup was saved: %v (%v)", accounts, err)
	}
	if err := registerAccount(p, "add", []string{"work", "--provider", "agy"}); err != nil {
		t.Fatal(err)
	}
	a, err := findAccount(p, "work", nil)
	if err != nil || a.subscription() != agyPro {
		t.Fatalf("account=%+v err=%v", a, err)
	}
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
	a, err := addAgyAccount(p, "work", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addAgyAccount(p, "duplicate", "", false); err == nil || !strings.Contains(err.Error(), "already registered as work") {
		t.Fatalf("expected duplicate sign-in to be rejected, got %v", err)
	}
	writeTestAccount(t, p, Account{ID: "codex", Name: "personal", AccountID: "test-codex"})
	if err := useAccount(p, []string{"personal"}); err != nil {
		t.Fatal(err)
	}
	agy := providerAgy
	if err := removeAccount(p, "work", &agy); err == nil || !strings.Contains(err.Error(), "cannot remove the active account") {
		t.Fatalf("expected active Antigravity account to be kept while Codex is the default, got %v", err)
	}
	t.Setenv("TEST_AGY_EMAIL", "personal@example.com")
	b, err := addAgyAccount(p, "personal", "", false)
	if err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { handleCurrent(p) })
	if !strings.Contains(out, "personal") || !strings.Contains(out, "[Antigravity Pro]") {
		t.Fatalf("current after adding the second Antigravity account: %q", out)
	}
	if a.ID == b.ID {
		t.Fatal("account directories overlap")
	}
	accounts, err := dashboardAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if account := accounts[accountIndex(accounts, b.ID)]; account.ID != b.ID || account.subscription() != agyPro || account.Email != "personal@example.com" {
		t.Fatalf("Antigravity dashboard account is missing its subscription or email: %+v", account)
	}
	for _, account := range []Account{a, b} {
		r := fetchAgyUsage(p, account)
		if r.Err != "" || len(r.QuotaGroups) != 2 || r.QuotaGroups[0].Weekly.UsedPercent != 75 {
			t.Fatalf("quota for %s: %+v", account.Name, r)
		}
	}
	if err := useAccount(p, []string{"--provider=codex", "personal"}); err != nil {
		t.Fatal(err)
	}
	if err := useAccount(p, []string{"PERSONAL"}); err == nil || !strings.Contains(err.Error(), "--provider") {
		t.Fatalf("expected a provider choice for personal, got %v", err)
	}
	out = captureStdout(t, func() { handleCurrent(p) })
	if !strings.Contains(out, "personal") || strings.Contains(out, "[Antigravity Pro]") {
		t.Fatalf("current: %q", out)
	}
	for _, args := range [][]string{{"work"}, {"personal", "--provider", "agy"}} {
		if err := useAccount(p, args); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() { handleCurrent(p) })
		if !strings.Contains(out, args[0]) || !strings.Contains(out, "[Antigravity Pro]") {
			t.Fatalf("current: %q", out)
		}
	}
	withPipeStdin(t, "", func() {
		if err := useAccount(p, []string{"--provider", "agy"}); err == nil || !strings.Contains(err.Error(), "pass an account name") {
			t.Fatalf("noninteractive picker: %v", err)
		}
	})
}

func TestDashboardOpensAndFoldsSubscriptions(t *testing.T) {
	oldColor := useColor
	useColor = true
	t.Cleanup(func() { useColor = oldColor })

	accounts := []Account{
		{ID: "primary", Email: "primary@example.com"},
		{ID: "backup", Email: "backup@example.com"},
		{ID: "personal", Email: "personal@example.com", Subscription: agyPro},
		{ID: "work", Email: "work@example.com", Subscription: agyPro},
		{ID: "pro", Email: "pro@example.com", Subscription: newSubscription(providerCodex, "pro-100")},
	}
	v := dashboardView{
		accounts: accounts,
		state:    State{CodexAccountID: "primary", ActiveAccounts: map[provider]string{providerAgy: "personal"}},
		size:     termSize{rows: 30, cols: 120},
	}
	for _, a := range accounts {
		v.results = append(v.results, UsageResult{Account: a})
	}
	frame, layout := renderDashboardFrame(v)
	for _, want := range []string{"▾  Codex  Plus", "▾  Antigravity Pro", "▾  Codex Pro 100", "personal@example.com", "pro@example.com"} {
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
	if v.sel != 0 || !strings.Contains(plain(frame), "▸  Codex  Plus · default · primary@example.com · 2 accounts") || strings.Contains(plain(frame), "backup@example.com") {
		t.Fatalf("left did not fold Codex: cursor=%d, frame=%q", v.sel, frame)
	}
	_, rest, _ := strings.Cut(plain(frame), "Antigravity Pro")
	if !strings.Contains(rest, "5 hour") || strings.Count(frame, "5 hour") != 1 {
		t.Fatalf("the first open group should carry the shared headings:\n%s", frame)
	}
	press("right")
	frame, _ = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "▾  Codex") || !strings.Contains(plain(frame), "backup@example.com") {
		t.Fatalf("right did not open Codex: %q", frame)
	}
	press("tab")
	frame, _ = renderDashboardFrame(v)
	focused := "\x1b[7m " + bold("Antigravity") + " \x1b[27m"
	if v.sel != 2 || !strings.Contains(frame, "▾ "+focused) {
		t.Fatalf("Tab did not focus the Antigravity heading: sel=%d, frame=%q", v.sel, frame)
	}
	press("left")
	frame, layout = renderDashboardFrame(v)
	folded := strings.Split(frame, "\n")[layout.headerRows[v.sel]-1]
	if !strings.Contains(folded, focused) || !strings.Contains(plain(folded), "personal@example.com") || strings.Contains(folded, "▌") {
		t.Fatalf("folded Antigravity heading should highlight its name and show its active account: %q", folded)
	}
	press("right")
	frame, layout = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "▾  Antigravity") || !strings.Contains(plain(frame), "personal@example.com") || !strings.Contains(plain(frame), "work@example.com") {
		t.Fatalf("right did not open Antigravity: %q", frame)
	}
	rows := strings.Split(frame, "\n")
	accountRow := layout.headerRows[v.sel] - 1
	labelColumn := cellIndex(rows[accountRow-2], "Antigravity")
	if cursorColumn := cellIndex(rows[accountRow], "▌"); labelColumn < 0 || cursorColumn != labelColumn-1 {
		t.Fatalf("heading label column=%d, expanded cursor column=%d", labelColumn, cursorColumn)
	}
	if action, _ := applyKey(&v, "enter", layout, false); action != "switch" {
		t.Fatalf("selecting an expanded account: %s", action)
	}
	press("left")
	for _, want := range []int{4, 0, 2} {
		press("tab")
		if v.sel != want {
			t.Fatalf("Tab selected row %d, want %d", v.sel, want)
		}
	}
	if !v.folded(agyPro) {
		t.Fatal("Tab opened a folded group")
	}
	press("enter")
	frame, _ = renderDashboardFrame(v)
	if !strings.Contains(plain(frame), "work@example.com") {
		t.Fatalf("enter did not reopen the subscription: %q", frame)
	}
}

func TestDashboardTogglesExtraAntigravityModels(t *testing.T) {
	t.Setenv("LC_TIME", "tr_TR")
	a := Account{ID: "agy", Name: "primary", Email: "primary@example.com", Subscription: agyPro}
	gemini := autoResult(a, 75, 37)
	reset := time.Now().Add(2 * time.Hour)
	gemini.FiveHour.ResetsAt = reset.Unix()
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
	backup := r
	backup.Account.ID, backup.Account.Email = "backup", "backup@example.com"
	v := dashboardView{
		state:    State{DefaultAccountID: a.ID},
		accounts: []Account{a, backup.Account},
		results:  []UsageResult{r, backup},
		size:     termSize{cols: 100},
	}
	frame, layout := renderDashboardFrame(v)
	if layout.headerRows[1]-layout.headerRows[0] != 1 || strings.Contains(frame, "Claude and GPT models") || !strings.Contains(frame, "m models") {
		t.Fatalf("Antigravity accounts should start compact with a models shortcut:\n%s", frame)
	}
	toggle := func() {
		t.Helper()
		captureStdout(t, func() {
			action, next := applyKey(&v, "m", layout, false)
			if action != "" {
				t.Fatalf("m should only change the displayed model groups, got %q", action)
			}
			layout = next
		})
	}
	toggle()
	frame, layout = renderDashboardFrame(v)
	if layout.headerRows[1]-layout.headerRows[0] != 3 {
		t.Fatalf("only two detail rows should follow the selected account:\n%s", frame)
	}
	plainFrame := plain(frame)
	rows := strings.Split(plainFrame, "\n")
	for _, want := range [][]string{
		{"5 hour", "weekly", "banked"},
		{a.Email, "37%  ↻  " + reset.Local().Format("02.01.2006 15:04"), "75%"},
		{"Claude and GPT models", "90%", "42%"},
		{"Gemini 4 Argon", "—", "100%"},
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
	if !strings.Contains(plain(narrow), "42%") || strings.Contains(plain(narrow), "↻") {
		t.Fatalf("narrow dashboard quota: %q", narrow)
	}
	v.size.cols = 100
	toggle()
	frame, layout = renderDashboardFrame(v)
	if layout.headerRows[1]-layout.headerRows[0] != 1 || strings.Contains(frame, "Claude and GPT models") {
		t.Fatalf("m should close the model rows again:\n%s", frame)
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
	agy := Account{ID: "agy-account", Name: "agy-account", Subscription: agyPro}
	backup := Account{ID: "agy-backup", Name: "agy-backup", Subscription: agyPro}
	v := dashboardView{
		state:    State{CodexAccountID: codex.ID, ActiveAccounts: map[provider]string{providerAgy: agy.ID}},
		accounts: []Account{codex, agy, backup},
		results:  []UsageResult{autoResult(codex, 50, 80), autoResult(agy, 50, 80), autoResult(backup, 30, 60)},
		sel:      2,
		size:     termSize{cols: 100},
	}
	for i := 1; i < len(v.results); i++ {
		r := &v.results[i]
		r.QuotaGroups = []quotaGroup{{Name: "Gemini Models", Weekly: r.Usage, FiveHour: r.FiveHour}}
	}
	for _, p := range []provider{providerCodex, providerAgy} {
		v.state.DefaultAccountID = v.state.activeAccountID(p)
		for sel := range v.accounts {
			v.sel = sel
			frame, layout := renderDashboardFrame(v)
			rows := strings.Split(frame, "\n")
			for i, a := range v.accounts[:2] {
				if row := rows[layout.headerRows[i]-1]; !strings.Contains(row, green(bold(a.Name))) || !strings.Contains(row, green("80%")) {
					t.Fatalf("the active account and its quota should be green: %q", row)
				}
			}
			row := rows[layout.headerRows[sel]-1]
			if !strings.Contains(row, cyan("▌")) || !strings.Contains(plain(row), "▌  "+v.accounts[sel].Name) {
				t.Fatalf("the selected row should have a separate cyan cursor: %q", row)
			}
		}
	}
}

func TestDashboardKeepsAccountAndDefaultSelectionsSeparate(t *testing.T) {
	keyring := installTestAgyKeyring(t)
	if err := os.WriteFile(keyring, []byte(`{"id_token_email":"personal@example.com","token":{"refresh_token":"personal"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldColor := useColor
	useColor = false
	t.Cleanup(func() { useColor = oldColor })

	p := makeTestPaths(t)
	codex := Account{ID: "codex", Name: "work", AccountID: "codex-work", Email: "x@example.com"}
	personal := Account{ID: agyDefaultID, Name: "personal", Subscription: agyPro}
	work := Account{ID: "agy-work", Name: "work", Subscription: agyPro}
	for _, a := range []Account{codex, personal, work} {
		writeTestAccount(t, p, a)
	}
	if err := atomicWrite(agyCredentialPath(p, work), []byte(`{"id_token_email":"work@example.com","token":{"refresh_token":"work"}}`), 0o600); err != nil {
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
	v.foldedGroups = map[subscription]bool{codexPlus: true}
	if _, err := applySwitch(p, &v); err != nil {
		t.Fatal(err)
	}
	if v.state.defaultAccountID() != codex.ID || v.state.activeAccountID(providerAgy) != work.ID {
		t.Fatalf("unexpected state: %+v", v.state)
	}
	if v.accounts[0].provider() != providerAgy || !v.folded(codexPlus) || v.accounts[v.sel].ID != work.ID {
		t.Fatalf("the dashboard changed group order, folding, or cursor: %+v", v)
	}
	before, layout := renderDashboardFrame(v)
	if !strings.Contains(plain(before), "▸  Codex Plus · default · x@example.com · 1 account") {
		t.Fatalf("missing the default marker on folded Codex: %q", before)
	}
	if action, _ := applyKey(&v, "d", layout, false); action != "default" {
		t.Fatalf("d action = %q", action)
	}
	if err := applyDefaultSubscription(p, &v); err != nil {
		t.Fatal(err)
	}
	st, err := loadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.defaultAccountID() != work.ID || st.activeAccountID(providerCodex) != codex.ID {
		t.Fatalf("unexpected saved state: %+v", st)
	}
	after, layout := renderDashboardFrame(v)
	agyHeading := strings.Index(after, "▾  [Antigravity] Pro · default")
	codexHeading := strings.Index(plain(after), "▸  Codex Plus · x@example.com · 1 account")
	if agyHeading < 0 || codexHeading < agyHeading || v.accounts[v.sel].ID != work.ID {
		t.Fatalf("unexpected frame or cursor after setting the default: %q", after)
	}
	v.sel = accountIndex(v.accounts, codex.ID)
	if action, _ := applyKey(&v, "d", layout, false); action != "default" {
		t.Fatalf("d on folded group = %q", action)
	}
	if err := applyDefaultSubscription(p, &v); err != nil {
		t.Fatal(err)
	}
	if v.state.defaultAccountID() != codex.ID || !v.folded(codexPlus) {
		t.Fatalf("setting the default should preserve folding: %+v", v)
	}
}

func TestSingleSubscriptionDashboardIsFlat(t *testing.T) {
	for _, p := range []provider{providerCodex, providerAgy} {
		a := Account{ID: "primary", Name: "primary", Subscription: newSubscription(p, "")}
		b := Account{ID: "backup", Name: "backup", Subscription: newSubscription(p, "")}
		v := dashboardView{
			state:        State{},
			accounts:     []Account{a, b},
			results:      []UsageResult{autoResult(a, 50, 80), autoResult(b, 45, 65)},
			foldedGroups: map[subscription]bool{newSubscription(p, ""): true},
		}
		v.state.setActiveAccount(a)
		frame, layout := renderDashboardFrame(v)
		if strings.ContainsAny(frame, "▾▸") || strings.Contains(frame, "set default") || strings.Contains(frame, "fold/unfold") {
			t.Fatalf("unexpected subscription controls: %q", frame)
		}
		row := strings.Split(frame, "\n")[layout.headerRows[0]-1]
		if cellIndex(row, "▌") != 1 || strings.Contains(row, p.label()) || layout.headerRows[1] < 1 {
			t.Fatalf("unexpected single-subscription account rows: %q", frame)
		}
		for _, key := range []string{"d", "left", "right", "tab"} {
			if action, _ := applyKey(&v, key, layout, false); action != "" || v.sel != 0 {
				t.Fatalf("single subscription handled group key %q as %q, sel=%d", key, action, v.sel)
			}
		}
		if picker := renderAccountPicker(v.accounts, 0); !strings.Contains(plain(picker), a.subscription().label()) {
			t.Fatalf("subscription label missing from filtered picker: %q", picker)
		}
	}
}

func TestDashboardDefaultHintFollowsFoldedFocus(t *testing.T) {
	agy := Account{ID: "agy", Name: "work", Subscription: agyPro}
	codex := Account{ID: "codex", Name: "primary"}
	v := dashboardView{
		accounts:     []Account{agy, codex},
		results:      []UsageResult{{Account: agy}, {Account: codex}},
		foldedGroups: map[subscription]bool{codexPlus: true},
		size:         termSize{rows: 30, cols: 120},
	}
	frame, layout := renderDashboardFrame(v)
	if strings.Contains(frame, "d set default") {
		t.Fatalf("the hint appeared while focused on an account: %q", frame)
	}
	for _, move := range []struct {
		key  string
		show bool
	}{{"down", true}, {"up", false}, {"left", true}, {"right", false}} {
		frame := captureStdout(t, func() {
			action, next := applyKey(&v, move.key, layout, false)
			if action != "" {
				t.Fatalf("%s action = %q", move.key, action)
			}
			layout = next
		})
		if !strings.Contains(frame, "q quit") || strings.Contains(frame, "d set default") != move.show {
			t.Fatalf("%s should redraw the footer with default hint=%v: %q", move.key, move.show, frame)
		}
	}
}
