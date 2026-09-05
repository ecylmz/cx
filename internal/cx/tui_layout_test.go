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
			for _, selected := range []bool{false, true} {
				line := plain(dashboardAccountHeader(st, a, selected))
				if on {
					withColor = append(withColor, line)
				} else {
					withoutColor = append(withoutColor, line)
				}
			}
		}
	}
	for i := range withColor {
		if withColor[i] != withoutColor[i] {
			t.Fatalf("color changed the layout:\n plain %q\n color %q", withoutColor[i], withColor[i])
		}
	}
	// An account without a plan must not slide its email into the plan column.
	if got, want := cellIndex(withoutColor[4], "three@"), cellIndex(withoutColor[0], "one@"); got != want {
		t.Fatalf("email column moved for a planless account: %d vs %d\n%q\n%q", got, want, withoutColor[0], withoutColor[4])
	}
	// The cursor bar takes the place of the leading spaces, so selecting an
	// account must not shift the columns to its right.
	if got, want := cellIndex(withoutColor[1], "one@"), cellIndex(withoutColor[0], "one@"); got != want {
		t.Fatalf("the cursor bar moved the email column: %d vs %d\n%q\n%q", got, want, withoutColor[0], withoutColor[1])
	}
}

// cellIndex reports the display column a substring starts at, which is not its
// byte offset once a line carries a multi-byte glyph such as the cursor bar.
func cellIndex(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return visibleCells(line[:i])
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
		if action, _ := applyKey(&v, "?", dashboardLayout{}, false); action != "" || v.overlay != overlayKeys {
			t.Fatalf("? should open help: action=%q overlay=%v", action, v.overlay)
		}
	})
	for _, want := range []string{"cx", "keys", "PgUp", "Ctrl+C", "press any key"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help screen missing %q:\n%s", want, out)
		}
	}
	captureStdout(t, func() {
		// Every key leaves help, including one that would otherwise quit.
		if action, _ := applyKey(&v, "q", dashboardLayout{}, false); action != "" || v.overlay != overlayNone {
			t.Fatalf("any key should close help: action=%q overlay=%v", action, v.overlay)
		}
	})
}

func TestBankedScreenOpensAndAnyKeyCloses(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	now := time.Now()
	v := layoutTestView(t, 3, 1, termSize{rows: 40, cols: 100})
	v.results[0].BankedLoaded = true
	v.results[0].BankedResets = []BankedReset{
		{ID: "a", ExpiresAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
		{ID: "b", ExpiresAt: now.Add(20 * 24 * time.Hour).Format(time.RFC3339)},
	}
	v.results[1].BankedLoaded = true
	v.results[2].BankedLoaded = true
	v.results[2].BankedErr = "401 unauthorized"

	out := captureStdout(t, func() {
		if action, _ := applyKey(&v, "b", dashboardLayout{}, false); action != "" || v.overlay != overlayBanked {
			t.Fatalf("b should open the banked screen: action=%q overlay=%v", action, v.overlay)
		}
	})
	// Every account is listed, whether it has resets, none, or a failed read.
	for _, want := range []string{"banked resets", "account0", "2 left", "account1", "none", "account2", "unavailable", "press any key"} {
		if !strings.Contains(out, want) {
			t.Fatalf("banked screen missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, bankedPip); n != 2 {
		t.Fatalf("want one row per reset, got %d pips:\n%s", n, out)
	}

	captureStdout(t, func() {
		if action, _ := applyKey(&v, "q", dashboardLayout{}, false); action != "" || v.overlay != overlayNone {
			t.Fatalf("any key should close the banked screen: action=%q overlay=%v", action, v.overlay)
		}
	})
}

// The banked axis reads as the third meter of the block, so nothing may come
// between it and the two quota lines above it.
func TestBankedLineStaysWithTheQuotaMeters(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 1, 0, termSize{rows: 40, cols: 100})
	v.results[0].Primed = true // Adds a note that used to be drawn above banked.
	v.results[0].BankedLoaded = true
	v.results[0].BankedResets = []BankedReset{{ID: "a", ExpiresAt: time.Now().Add(9 * 24 * time.Hour).Format(time.RFC3339)}}

	block := accountBlock(v, 0)
	weekly, banked := -1, -1
	for i, line := range block {
		switch {
		case strings.Contains(line, "weekly"):
			weekly = i
		case strings.Contains(line, "banked"):
			banked = i
		}
	}
	if weekly < 0 || banked != weekly+1 {
		t.Fatalf("banked should follow weekly directly, weekly=%d banked=%d:\n%s", weekly, banked, strings.Join(block, "\n"))
	}
}

// The banked screen does not scroll, so a listing too tall for the terminal has
// to be cut deliberately — and the note has to count resets, not rendered lines.
// With no banked resets anywhere, b would open a screen listing nothing, so the
// key is inert and neither the footer nor the help screen mentions it.
func TestBankedKeyIsInertWithoutAnyBankedResets(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 3, 0, termSize{rows: 40, cols: 100})
	for i := range v.results {
		v.results[i].BankedLoaded = true // Fetched, and there is nothing to show.
	}
	captureStdout(t, func() {
		if action, _ := applyKey(&v, "b", dashboardLayout{}, false); action != "" || v.overlay != overlayNone {
			t.Fatalf("b should do nothing: action=%q overlay=%v", action, v.overlay)
		}
	})
	frame, _ := renderDashboardFrame(v)
	if strings.Contains(frame, "b banked") {
		t.Fatalf("the footer should not offer a key that does nothing:\n%s", frame)
	}
	if help := renderHelpScreen(v.size, bankedUIActive(v.results)); strings.Contains(help, "banked reset dates") {
		t.Fatalf("the help screen should not list it either:\n%s", help)
	}

	// One reset on one account is enough to bring the whole thing back.
	v.results[2].BankedResets = []BankedReset{
		{ID: "a", ExpiresAt: time.Now().Add(9 * 24 * time.Hour).Format(time.RFC3339)},
	}
	captureStdout(t, func() {
		if action, _ := applyKey(&v, "b", dashboardLayout{}, false); action != "" || v.overlay != overlayBanked {
			t.Fatalf("b should open: action=%q overlay=%v", action, v.overlay)
		}
	})
	v.overlay = overlayNone
	if frame, _ = renderDashboardFrame(v); !strings.Contains(frame, "b banked") {
		t.Fatalf("the footer should offer the key again:\n%s", frame)
	}
}

func TestBankedScreenCutsToFitAndCountsTheResetsItHid(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	now := time.Now()
	v := layoutTestView(t, 4, 0, termSize{rows: 11, cols: 100})
	for i := range v.results {
		v.results[i].BankedLoaded = true
		v.results[i].BankedResets = []BankedReset{
			{ID: "x", ExpiresAt: now.Add(5 * 24 * time.Hour).Format(time.RFC3339)},
			{ID: "y", ExpiresAt: now.Add(20 * 24 * time.Hour).Format(time.RFC3339)},
		}
	}
	// 4 accounts × (1 header + 2 resets) = 12 rows into 11 - 5 = 6.
	screen := renderBankedScreen(v)
	if got := strings.Count(screen, "\n"); got > v.size.rows-1 {
		t.Fatalf("screen is %d rows and overflows a %d-row terminal:\n%s", got, v.size.rows, screen)
	}
	// Five rows are kept — account0's header and both its resets, then account1's
	// header and its first reset — which leaves five resets unlisted.
	if !strings.Contains(screen, "5 more resets") {
		t.Fatalf("the note should count hidden resets, not lines:\n%s", screen)
	}

	// A terminal with no room at all still must not write past its last row —
	// and still has to say the listing was cut, or it reads as no banked resets.
	v.size = termSize{rows: 4, cols: 100}
	screen = renderBankedScreen(v)
	if got := strings.Count(screen, "\n"); got > v.size.rows-1 {
		t.Fatalf("screen is %d rows on a %d-row terminal:\n%s", got, v.size.rows, screen)
	}
	if !strings.Contains(screen, "8 more resets") {
		t.Fatalf("a screen with no room for the listing still owes the count:\n%s", screen)
	}
}

// The help screen does not scroll either, and it grows every time a key is
// added, so it has to be cut to the terminal the same way.
func TestHelpScreenCutsToFit(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	full := renderHelpScreen(termSize{rows: 40, cols: 100}, true)
	if !strings.Contains(full, "b ") || !strings.Contains(full, "q  ·  Esc") {
		t.Fatalf("a tall terminal should list every key:\n%s", full)
	}

	for _, rows := range []int{13, 8, 4} {
		size := termSize{rows: rows, cols: 100}
		screen := renderHelpScreen(size, true)
		if got := strings.Count(screen, "\n"); got > size.rows-1 {
			t.Fatalf("help screen is %d rows on a %d-row terminal:\n%s", got, size.rows, screen)
		}
		if !strings.Contains(screen, "more keys") {
			t.Fatalf("a cut help screen owes the count of what it dropped:\n%s", screen)
		}
		if !strings.Contains(screen, "cx") || !strings.Contains(screen, "press any key") {
			t.Fatalf("the frame should survive the cut:\n%s", screen)
		}
	}
}

// fitCells trims the tail of the footer, which is where the key that gets you
// out lives, so a narrow terminal has to give up hints deliberately instead.
func TestFooterGivesUpHintsRatherThanTheQuitKey(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 2, 0, termSize{rows: 40, cols: 100})
	v.results[0].BankedLoaded = true
	v.results[0].BankedResets = []BankedReset{
		{ID: "a", ExpiresAt: time.Now().Add(9 * 24 * time.Hour).Format(time.RFC3339)},
	}

	for _, cols := range []int{100, 74, 73, 64, 50, 30, 20} {
		line := dashboardKeys(v.results, cols)
		if got := visibleCells(line); cols >= 20 && got > cols {
			t.Fatalf("cols=%d: footer is %d cells and would be cut: %q", cols, got, line)
		}
		if !strings.Contains(line, "q quit") || !strings.Contains(line, "? keys") {
			t.Fatalf("cols=%d: the way out and the full key list must survive: %q", cols, line)
		}
	}
	// The niche screen is the first hint given up, not the last.
	if wide, tight := dashboardKeys(v.results, 100), dashboardKeys(v.results, 73); !strings.Contains(wide, "b banked") || strings.Contains(tight, "b banked") {
		t.Fatalf("wide=%q tight=%q", wide, tight)
	}
}

// Both overlays keep the row of headroom they document, however short the
// terminal — the frame's last newline would otherwise scroll the screen.
func TestOverlaysKeepTheirHeadroomOnAShortTerminal(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	now := time.Now()
	v := layoutTestView(t, 3, 0, termSize{cols: 100})
	for i := range v.results {
		v.results[i].BankedLoaded = true
		v.results[i].BankedResets = []BankedReset{
			{ID: "x", ExpiresAt: now.Add(5 * 24 * time.Hour).Format(time.RFC3339)},
		}
	}
	for _, rows := range []int{3, 4, 5, 6, 8, 13, 40} {
		v.size = termSize{rows: rows, cols: 100}
		for name, screen := range map[string]string{
			"keys":   renderHelpScreen(v.size, true),
			"banked": renderBankedScreen(v),
		} {
			if got := strings.Count(screen, "\n"); got > rows-1 {
				t.Fatalf("%s screen is %d rows on a %d-row terminal:\n%s", name, got, rows, screen)
			}
		}
	}
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

// The dashboard indents one space deeper than cx status does, and the selected
// account spends the first of those cells on the cursor bar instead — the two
// indents are the same width, so the columns line up down the whole list.
func TestDashboardIndentsUsageLinesOneSpaceDeeperThanStatus(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 2, 1, termSize{rows: 40, cols: 120})
	frame, _ := renderDashboardFrame(v)
	if !strings.Contains(frame, "\n   weekly  ") {
		t.Fatalf("dashboard usage line indentation changed:\n%s", frame)
	}
	if !strings.Contains(frame, "\n ▌ weekly  ") {
		t.Fatalf("the selected account's block is not marked down its lines:\n%s", frame)
	}
}

// The cursor marks every line of the selected account, so the selection cannot
// be mistaken for a neighbouring block.
func TestCursorBarSpansTheSelectedBlock(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 3, 1, termSize{rows: 40, cols: 120})
	for i := range v.results {
		marked := 0
		for _, line := range accountBlock(v, i) {
			if strings.HasPrefix(line, " ▌") {
				marked++
			}
		}
		if i == v.sel && marked < 2 {
			t.Fatalf("selected block %d marked on %d lines", i, marked)
		}
		if i != v.sel && marked != 0 {
			t.Fatalf("unselected block %d carries the cursor bar", i)
		}
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
