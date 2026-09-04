package cx

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

func plain(s string) string { return ansiPattern.ReplaceAllString(s, "") }

func layoutTestView(t *testing.T, n int, sel int, size termSize) dashboardView {
	t.Helper()
	p := makeTestPaths(t)
	if err := saveState(p, State{ActiveID: "id0"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	accounts := make([]Account, n)
	results := make([]UsageResult, n)
	for i := range accounts {
		accounts[i] = Account{
			ID:    "id" + string(rune('0'+i)),
			Name:  "account" + string(rune('0'+i)),
			Plan:  "plus",
			Email: "user@example.com",
		}
		results[i] = UsageResult{
			Account: accounts[i],
			Usage:   WeeklyUsage{UsedPercent: 10, WindowMinutes: 10080, ResetsAt: now.Add(time.Hour).Unix(), FetchedAt: now, WindowStarted: true},
		}
	}
	v := newDashboardView(p, accounts, results, sel)
	v.size = size
	return v
}

// A width verb applied to an already colored string counts its escape bytes as
// characters, which used to shrink the name column by exactly the eight bytes
// of the bold sequence as soon as color was on.
func TestAccountHeaderColumnsAlignWhateverTheColorSetting(t *testing.T) {
	old := useColor
	defer func() { useColor = old }()

	st := State{ActiveID: "a"}
	accounts := []Account{
		{ID: "a", Name: "primary", Plan: "plus", Email: "one@example.com"},
		{ID: "b", Name: "a-much-longer-name", Plan: "pro", Email: "two@example.com"},
		{ID: "c", Name: "noplan", Email: "three@example.com"},
	}
	var withColor, withoutColor []string
	for _, on := range []bool{false, true} {
		useColor = on
		for _, a := range accounts {
			line := plain(dashboardAccountHeader(st, a, false))
			if on {
				withColor = append(withColor, line)
			} else {
				withoutColor = append(withoutColor, line)
			}
		}
	}
	for i := range withColor {
		if withColor[i] != withoutColor[i] {
			t.Fatalf("color changed the layout:\n plain %q\n color %q", withoutColor[i], withColor[i])
		}
	}
	// An account without a plan must not slide its email into the plan column.
	if got, want := strings.Index(withoutColor[2], "three@"), strings.Index(withoutColor[0], "one@"); got != want {
		t.Fatalf("email column moved for a planless account: %d vs %d\n%q\n%q", got, want, withoutColor[0], withoutColor[2])
	}
}

func TestMoveSelectionCoversEveryNavigationKey(t *testing.T) {
	const n = 12
	tests := []struct {
		from, want int
		key        string
	}{
		{0, 0, "up"}, {0, 1, "down"}, {5, 4, "k"}, {5, 6, "j"},
		{7, 7 - pageJump, "pgup"}, {0, 0, "pgup"},
		{0, pageJump, "pgdn"}, {n - 1, n - 1, "pgdn"},
		{5, 0, "home"}, {5, 0, "g"}, {5, n - 1, "end"}, {5, n - 1, "G"},
		// Keys the dashboard does not navigate with must leave the cursor alone
		// rather than being read as something else.
		{5, 5, "right"}, {5, 5, "left"}, {5, 5, ""}, {5, 5, "x"},
	}
	for _, tt := range tests {
		if got := moveSelection(tt.from, n, tt.key); got != tt.want {
			t.Fatalf("moveSelection(%d, %q)=%d want %d", tt.from, tt.key, got, tt.want)
		}
	}
	if got := moveSelection(0, 0, "end"); got != 0 {
		t.Fatalf("empty list end=%d", got)
	}
}

func TestAccountIndexKeepsTheCursorOnTheSameAccount(t *testing.T) {
	accounts := []Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if got := accountIndex(accounts, "c"); got != 2 {
		t.Fatalf("accountIndex=%d want 2", got)
	}
	if got := accountIndex(accounts, "gone"); got != 0 {
		t.Fatalf("removed account should fall back to the first, got %d", got)
	}
}

// A frame taller than the terminal scrolls the alternate screen, which makes the
// absolute rows the cursor update writes to point at the wrong lines.
func TestFrameKeepsTheSelectedAccountOnScreen(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	for _, sel := range []int{0, 3, 7} {
		v := layoutTestView(t, 8, sel, termSize{rows: 16, cols: 100})
		frame, layout := renderDashboardFrame(v)
		if !layout.fits {
			t.Fatalf("sel=%d: frame does not fit its own terminal:\n%s", sel, frame)
		}
		if lines := strings.Count(frame, "\n"); lines >= v.size.rows {
			t.Fatalf("sel=%d: frame is %d rows on a %d row terminal:\n%s", sel, lines, v.size.rows, frame)
		}
		if layout.headerRows[sel] <= 0 {
			t.Fatalf("sel=%d: selected account scrolled out of view: %v", sel, layout.headerRows)
		}
		if !strings.Contains(frame, "more") {
			t.Fatalf("sel=%d: a windowed list must say what it is hiding:\n%s", sel, frame)
		}
	}
}

func TestFrameFitsTheTerminalWidth(t *testing.T) {
	old := useColor
	defer func() { useColor = old }()
	for _, on := range []bool{false, true} {
		useColor = on
		for _, cols := range []int{40, 60, 100} {
			v := layoutTestView(t, 3, 0, termSize{rows: 40, cols: cols})
			frame, _ := renderDashboardFrame(v)
			for _, line := range strings.Split(frame, "\n") {
				if w := len([]rune(plain(line))); w > cols {
					t.Fatalf("color=%v cols=%d: line is %d cells: %q", on, cols, w, plain(line))
				}
			}
		}
	}
}

// The cheap two-row cursor update is only correct while the frame sits still.
func TestSelectionUpdateRefusesRowsItCannotTrust(t *testing.T) {
	v := layoutTestView(t, 3, 0, termSize{rows: 40, cols: 100})
	fitting := dashboardLayout{headerRows: []int{3, 9, 15}, fits: true}
	if selectionUpdateString(v, 0, 1, fitting) == "" {
		t.Fatal("a fitting frame should update in place")
	}
	if got := selectionUpdateString(v, 0, 1, dashboardLayout{headerRows: []int{3, 9, 15}}); got != "" {
		t.Fatalf("a scrolled frame must be redrawn in full, got %q", got)
	}
	scrolledOut := dashboardLayout{headerRows: []int{-1, 9, 15}, fits: true}
	if got := selectionUpdateString(v, 0, 1, scrolledOut); got != "" {
		t.Fatalf("an off-screen account has no row to update, got %q", got)
	}
}

func TestApplyKeyDefersActionsUntilTheRefreshIsDone(t *testing.T) {
	v := layoutTestView(t, 3, 0, termSize{rows: 40, cols: 100})
	captureStdout(t, func() {
		// While results are still streaming, only navigation and quit apply:
		// switching and refreshing would act on half-fetched data.
		for _, key := range []string{"enter", "r"} {
			if action, _ := applyKey(&v, key, dashboardLayout{}, true); action != "" {
				t.Fatalf("key %q acted mid-refresh: %q", key, action)
			}
		}
		if action, _ := applyKey(&v, "down", dashboardLayout{}, true); action != "" || v.sel != 1 {
			t.Fatalf("navigation must work mid-refresh: action=%q sel=%d", action, v.sel)
		}
		if action, _ := applyKey(&v, "ctrl-c", dashboardLayout{}, true); action != "quit" {
			t.Fatalf("Ctrl+C mid-refresh=%q", action)
		}
		for _, key := range []string{"enter", "r", "q"} {
			want := map[string]string{"enter": "switch", "r": "refresh", "q": "quit"}[key]
			if action, _ := applyKey(&v, key, dashboardLayout{}, false); action != want {
				t.Fatalf("key %q=%q want %q", key, action, want)
			}
		}
	})
}

func TestHelpScreenOpensAndAnyKeyCloses(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 2, 0, termSize{rows: 40, cols: 100})
	out := captureStdout(t, func() {
		if action, _ := applyKey(&v, "?", dashboardLayout{}, false); action != "" || !v.help {
			t.Fatalf("? should open help: action=%q help=%v", action, v.help)
		}
	})
	for _, want := range []string{"cx", "keys", "PgUp", "Ctrl+C", "press any key"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help screen missing %q:\n%s", want, out)
		}
	}
	captureStdout(t, func() {
		// Every key leaves help, including one that would otherwise quit.
		if action, _ := applyKey(&v, "q", dashboardLayout{}, false); action != "" || v.help {
			t.Fatalf("any key should close help: action=%q help=%v", action, v.help)
		}
	})
}

func TestTitleNamesTheActiveAccountAndTheDataAge(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 2, 0, termSize{rows: 40, cols: 100})
	if got := dashboardTitle(v); !strings.Contains(got, "account0") {
		t.Fatalf("title should name the active account: %q", got)
	}
	v.refreshedAt = time.Now().Add(-90 * time.Second)
	if got := dashboardTitle(v); !strings.Contains(got, "updated 1m ago") {
		t.Fatalf("title should date the numbers: %q", got)
	}
	v.state = State{}
	if got := dashboardTitle(v); !strings.Contains(got, "no active account") {
		t.Fatalf("title with no active account: %q", got)
	}
}

// The footer row stays present when it is empty, so the frame does not lose a
// line and jump the moment a refresh finishes.
func TestFrameHeightIsStableAcrossTheEndOfARefresh(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 2, 0, termSize{rows: 40, cols: 100})
	v.footer = refreshFooter(1, 2)
	during, _ := renderDashboardFrame(v)
	v.footer = ""
	after, _ := renderDashboardFrame(v)
	if a, b := strings.Count(during, "\n"), strings.Count(after, "\n"); a != b {
		t.Fatalf("frame height changed from %d to %d rows when the refresh ended", a, b)
	}
}

// A key typed while the dashboard is busy must survive until the dashboard asks
// for it, and the bytes of one keypress must not be split across two reads.
func TestKeyStreamDeliversKeysTypedWhileBusy(t *testing.T) {
	withPipeStdin(t, "j\x1b[Bq", func() {
		keys := newKeyStream()
		for _, want := range []string{"j", "down", "q"} {
			ev := <-keys.ch
			if ev.err != nil || ev.key != want {
				t.Fatalf("key=%q err=%v want %q", ev.key, ev.err, want)
			}
		}
	})
}

// Switching rewrites the auth.json symlink and refreshing costs a full round of
// network reads, so keys typed ahead of an action are dropped, not replayed.
func TestKeyStreamDrainsTypeAhead(t *testing.T) {
	withPipeStdin(t, "jjjjjjjj", func() {
		keys := newKeyStream()
		if ev := <-keys.ch; ev.key != "j" {
			t.Fatalf("first key=%q", ev.key)
		}
		// Let the reader pull the rest off the terminal before draining.
		deadline := time.Now().Add(2 * time.Second)
		for len(keys.ch) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		keys.drain()
		select {
		case ev := <-keys.ch:
			t.Fatalf("type-ahead survived the drain: %q", ev.key)
		case <-time.After(50 * time.Millisecond):
		}
	})
}

// A wait that gives up on the byte after Esc must not eat the next keypress.
func TestBareEscKeepsTheKeyThatFollowsIt(t *testing.T) {
	r, w, err := osPipeForTest()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = w.Close()
		_ = r.Close()
	}()

	if _, err := w.Write([]byte{27}); err != nil {
		t.Fatal(err)
	}
	if key, err := readKey(); err != nil || key != "esc" {
		t.Fatalf("readKey=%q err=%v want esc", key, err)
	}
	if _, err := w.WriteString("q"); err != nil {
		t.Fatal(err)
	}
	if key, err := readKey(); err != nil || key != "q" {
		t.Fatalf("the keypress after a bare Esc was swallowed: %q err=%v", key, err)
	}
}

func osPipeForTest() (*os.File, *os.File, error) { return os.Pipe() }

// The dashboard indents one space deeper than cx status does.
func TestDashboardIndentsUsageLinesOneSpaceDeeperThanStatus(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 1, 0, termSize{rows: 40, cols: 120})
	frame, _ := renderDashboardFrame(v)
	if !strings.Contains(frame, "\n   weekly  ") {
		t.Fatalf("dashboard usage line indentation changed:\n%s", frame)
	}
}

// Switching rewrites the auth.json symlink and reorders the most-recently-used
// list. No account's quota changes, so the numbers already on screen are carried
// over instead of being fetched again.
func TestSwitchCarriesTheQuotaNumbersInsteadOfRefetching(t *testing.T) {
	p := makeTestPaths(t)
	accounts := []Account{
		{ID: "id1", Name: "primary", AccountID: "acct1", Email: "x@example.com"},
		{ID: "id2", Name: "backup", AccountID: "acct2", Email: "x@example.com"},
	}
	for _, a := range accounts {
		writeTestAccount(t, p, a)
	}
	if err := saveState(p, State{ActiveID: "id1"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	results := []UsageResult{
		{Account: accounts[0], Usage: WeeklyUsage{UsedPercent: 11, WindowMinutes: 10080, ResetsAt: now.Add(time.Hour).Unix(), FetchedAt: now, WindowStarted: true}},
		{Account: accounts[1], Usage: WeeklyUsage{UsedPercent: 77, WindowMinutes: 10080, ResetsAt: now.Add(time.Hour).Unix(), FetchedAt: now, WindowStarted: true}},
	}
	v := newDashboardView(p, accounts, results, 1) // cursor on "backup"

	rescan, err := applySwitch(p, &v)
	if err != nil {
		t.Fatal(err)
	}
	if rescan {
		t.Fatal("an unchanged account list must not force a refetch")
	}
	if v.state.ActiveID != "id2" {
		t.Fatalf("active account=%q want id2", v.state.ActiveID)
	}
	// Most-recently-used ordering puts the switched account first, and the
	// cursor stays on it rather than snapping back to the top of the list.
	if v.accounts[0].ID != "id2" || v.sel != 0 {
		t.Fatalf("order=%v sel=%d", []string{v.accounts[0].ID, v.accounts[1].ID}, v.sel)
	}
	for i, r := range v.results {
		if r.Account.ID != v.accounts[i].ID {
			t.Fatalf("result %d belongs to %q but sits under %q", i, r.Account.ID, v.accounts[i].ID)
		}
	}
	want := map[string]float64{"id1": 11, "id2": 77}
	for _, r := range v.results {
		if r.Usage.UsedPercent != want[r.Account.ID] {
			t.Fatalf("%s carried %.0f%% want %.0f%%", r.Account.ID, r.Usage.UsedPercent, want[r.Account.ID])
		}
		if r.Usage.FetchedAt.IsZero() {
			t.Fatalf("%s lost its fetch time, which is what marks the data as real", r.Account.ID)
		}
	}
}
