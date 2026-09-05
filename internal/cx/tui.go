package cx

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	altScreenEnter   = "\x1b[?1049h"
	altScreenExit    = "\x1b[?1049l"
	hideCursor       = "\x1b[?25l"
	showCursor       = "\x1b[?25h"
	disableWrap      = "\x1b[?7l"
	enableWrap       = "\x1b[?7h"
	syncOutputBegin  = "\x1b[?2026h"
	syncOutputEnd    = "\x1b[?2026l"
	clearScreenHome  = "\x1b[2J\x1b[H"
	eraseCurrentLine = "\x1b[2K"
)

// dashboardLayout records where the last frame put things. headerRows is the
// absolute terminal row of each account header, or -1 when the account is
// scrolled out of view; fits reports whether the frame stayed inside the screen,
// because absolute-row updates are only valid while nothing has scrolled.
type dashboardLayout struct {
	headerRows []int
	fits       bool
}

type termSize struct{ rows, cols int }

// terminalSize reads the window size through stty, the same mechanism the raw
// mode switch already uses, so measuring the terminal adds no dependency. A zero
// size means unknown, and every caller then treats the frame as unbounded.
func terminalSize() termSize {
	if rows, err := strconv.Atoi(os.Getenv("LINES")); err == nil && rows > 0 {
		if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && cols > 0 {
			return termSize{rows: rows, cols: cols}
		}
	}
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return termSize{}
	}
	var rows, cols int
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &rows, &cols); err != nil {
		return termSize{}
	}
	return termSize{rows: rows, cols: cols}
}

// dashboardView is everything a frame needs: the data, the cursor, and the
// terminal it has to fit into. State is read once per frame here rather than
// once per keypress, which is what a cursor move used to cost.
type dashboardView struct {
	state       State
	accounts    []Account
	results     []UsageResult
	sel         int
	footer      string
	size        termSize
	refreshedAt time.Time
	overlay     overlay
}

// overlay is the full-screen layer drawn instead of the account list. Any key
// dismisses one, so the dashboard only ever has to know which is up.
type overlay int

const (
	overlayNone overlay = iota
	overlayKeys
	overlayBanked
)

func newDashboardView(p paths, accounts []Account, results []UsageResult, sel int) dashboardView {
	st, _ := loadState(p)
	return dashboardView{state: st, accounts: accounts, results: results, sel: sel, size: terminalSize()}
}

func runDashboard(p paths) error {
	accounts, err := listAccounts(p)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return errors.New("no accounts; add one with: cx add NAME")
	}

	if !isTerminal() {
		results := fetchAllUsageWithPriming(p, accounts)
		applyCachedUsageFallback(p, results)
		saveFreshCache(p, results)
		printStatus(p, results)
		return nil
	}

	old, err := beginTerminalSession()
	if err != nil {
		return err
	}
	defer endTerminalSession(old)
	stopSignals := watchTerminalSignals(old)
	defer stopSignals()
	resize, stopResize := watchResize()
	defer stopResize()
	keys := newKeyStream()

	sel := 0
	for {
		v := newDashboardView(p, accounts, initialRefreshingResults(p, accounts), sel)
		v.footer = refreshFooter(0, len(accounts))
		layout := drawDashboardFrame(v)
		cache, _ := loadCache(p)
		completed := 0
		updates := fetchUsageUpdatesWithPriming(p, accounts)
		ticker := time.NewTicker(countdownInterval)

		// One select serves results, input, resizes and the countdown together.
		// Draining the result channel on its own is what used to make the whole
		// screen deaf for a full round of network reads: no cursor, no quit.
		action := ""
		for action == "" {
			select {
			case update, ok := <-updates:
				if !ok {
					updates = nil
					saveFreshCache(p, v.results)
					v.refreshedAt = time.Now()
					v.footer = ""
					layout = drawDashboardFrame(v)
					continue
				}
				r := update.Result
				if update.Final {
					completed++
					applyCachedUsageFallbackResult(cache, &r)
				}
				v.results[update.Index] = r
				v.footer = refreshFooter(completed, len(accounts))
				layout = drawDashboardFrame(v)
			case <-resize:
				v.size = terminalSize()
				layout = drawDashboardFrame(v)
			case <-ticker.C:
				// Every reset time on screen is relative, so a frame that is
				// never redrawn quietly ages into a wrong one.
				layout = drawDashboardFrame(v)
			case ev := <-keys.ch:
				if ev.err != nil {
					// The terminal went away. That is how the session ends, not
					// an error to report back to a shell that is no longer there.
					action = "quit"
					continue
				}
				var next string
				next, layout = applyKey(&v, ev.key, layout, updates != nil)
				if next != "switch" {
					action = next
					continue
				}
				// A switch is applied in place. It rewrites the auth.json
				// symlink and moves the account to the front of the
				// most-recently-used order; no account's quota changes, so the
				// numbers already on screen are carried over rather than
				// fetched again.
				keys.drain()
				rescan, err := applySwitch(p, &v)
				if err != nil {
					ticker.Stop()
					return err
				}
				accounts = v.accounts
				if len(accounts) == 0 {
					ticker.Stop()
					return nil
				}
				if rescan {
					// The account list itself changed under us, so the carried
					// results no longer line up with it.
					action = "refresh"
					continue
				}
				layout = drawDashboardFrame(v)
			}
		}
		ticker.Stop()
		keys.drain()
		sel = v.sel

		switch action {
		case "quit":
			return nil
		case "refresh":
			continue
		}
	}
}

// applySwitch makes the selected account active and folds the result back into
// the view without refetching. It reports rescan when the account list changed
// under it, which is the one case where the carried-over results no longer line
// up with the accounts they belong to.
func applySwitch(p paths, v *dashboardView) (rescan bool, err error) {
	if v.sel < 0 || v.sel >= len(v.accounts) {
		return false, nil
	}
	target := v.accounts[v.sel]
	if err := switchAccount(p, target); err != nil {
		return false, err
	}
	accounts, err := listAccounts(p)
	if err != nil {
		return false, err
	}
	byID := make(map[string]UsageResult, len(v.results))
	for _, r := range v.results {
		byID[r.Account.ID] = r
	}
	results := make([]UsageResult, len(accounts))
	for i, a := range accounts {
		r, ok := byID[a.ID]
		if !ok {
			rescan = true
		}
		// The reload carries the account's new last-used time with it.
		r.Account = a
		results[i] = r
	}
	v.state, _ = loadState(p)
	v.accounts = accounts
	v.results = results
	v.sel = accountIndex(accounts, target.ID)
	return rescan || len(accounts) != len(byID), nil
}

// countdownInterval redraws the frame often enough that the relative reset times
// stay honest, without costing a network round trip to do it.
const countdownInterval = 30 * time.Second

func refreshFooter(done, total int) string {
	if total <= 0 {
		return "refreshing live quota + banked resets…"
	}
	return fmt.Sprintf("refreshing %d/%d accounts…", done, total)
}

const refreshingErrPrefix = "__cx_refreshing__:"

func refreshingError(note string) string {
	return refreshingErrPrefix + note
}

func refreshingStatus(errText string) (bool, string) {
	if !strings.HasPrefix(errText, refreshingErrPrefix) {
		return false, ""
	}
	note := strings.TrimPrefix(errText, refreshingErrPrefix)
	if note == "" {
		note = "refreshing…"
	}
	return true, note
}

func initialRefreshingResults(p paths, accounts []Account) []UsageResult {
	cache, _ := loadCache(p)
	results := make([]UsageResult, len(accounts))
	for i, a := range accounts {
		results[i] = UsageResult{Account: a, Err: refreshingError("refreshing…")}
		if old, ok := cache[a.ID]; ok {
			old.Fresh = false
			results[i].Usage = old
		}
	}
	return results
}

func applyCachedUsageFallbackResult(cache map[string]WeeklyUsage, r *UsageResult) {
	if r == nil || r.Err == "" {
		return
	}
	if old, ok := cache[r.Account.ID]; ok {
		old.Fresh = false
		old.Err = r.Err
		r.Usage = old
	}
}

func applyCachedUsageFallback(p paths, results []UsageResult) {
	cache, _ := loadCache(p)
	for i := range results {
		applyCachedUsageFallbackResult(cache, &results[i])
	}
}

// dashboardLoop draws one frame and serves input until an action, without the
// refresh round that runDashboard wraps around it.
func dashboardLoop(p paths, accounts []Account, results []UsageResult, sel int) (int, string, error) {
	if !isTerminal() {
		printStatus(p, results)
		return sel, "quit", nil
	}
	old, err := beginTerminalSession()
	if err != nil {
		return sel, "", err
	}
	defer endTerminalSession(old)
	v := newDashboardView(p, accounts, results, sel)
	layout := drawDashboardFrame(v)
	keys := newKeyStream()
	// The action this returns rewrites accounts or leaves the screen. Keys typed
	// ahead of it were meant for the screen being left, not for a repeat of the
	// action, so they are dropped rather than replayed.
	defer keys.drain()
	for {
		ev := <-keys.ch
		if ev.err != nil {
			return v.sel, "quit", nil // The terminal went away.
		}
		action := ""
		action, layout = applyKey(&v, ev.key, layout, false)
		if action != "" {
			return v.sel, action, nil
		}
	}
}

// applyKey folds one keypress into the view and reports the action it asks for,
// redrawing whatever the key changed. While a refresh is still streaming, the
// keys that would act on half-fetched data are ignored rather than queued.
func applyKey(v *dashboardView, key string, layout dashboardLayout, refreshing bool) (string, dashboardLayout) {
	if v.overlay != overlayNone {
		v.overlay = overlayNone
		return "", drawDashboardFrame(*v)
	}
	switch key {
	case "enter":
		if refreshing {
			return "", layout
		}
		return "switch", layout
	case "r":
		if refreshing {
			return "", layout
		}
		return "refresh", layout
	case "q", "esc", "ctrl-c", "ctrl-d":
		return "quit", layout
	case "b":
		if !bankedUIActive(v.results) {
			return "", layout
		}
		v.overlay = overlayBanked
		return "", drawDashboardFrame(*v)
	case "?":
		v.overlay = overlayKeys
		return "", drawDashboardFrame(*v)
	}
	oldSel := v.sel
	v.sel = moveSelection(v.sel, len(v.accounts), key)
	if v.sel == oldSel {
		return "", layout
	}
	if update := selectionUpdateString(*v, oldSel, v.sel, layout); update != "" {
		_, _ = os.Stdout.WriteString(update)
		return "", layout
	}
	// The cheap two-line update is only valid while the frame sits still; a
	// scrolled or oversized frame has to be drawn again in full.
	return "", drawDashboardFrame(*v)
}

// moveSelection maps a navigation key to a new cursor position, and returns sel
// unchanged for every key that is not navigation.
func moveSelection(sel, n int, key string) int {
	last := max(n-1, 0)
	switch key {
	case "up", "k":
		return max(sel-1, 0)
	case "down", "j":
		return min(sel+1, last)
	case "pgup":
		return max(sel-pageJump, 0)
	case "pgdn":
		return min(sel+pageJump, last)
	case "home", "g":
		return 0
	case "end", "G":
		return last
	}
	return sel
}

const pageJump = 5

// accountIndex keeps the cursor on one account across a reload of the account
// list, so an action that rewrites the list does not move the selection.
func accountIndex(accounts []Account, id string) int {
	for i, a := range accounts {
		if a.ID == id {
			return i
		}
	}
	return 0
}

func drawDashboard(p paths, accounts []Account, results []UsageResult, sel int) {
	frame, _ := renderDashboardFrame(newDashboardView(p, accounts, results, sel))
	fmt.Print(clearScreenHome)
	fmt.Print(frame)
}

func drawDashboardFrame(v dashboardView) dashboardLayout {
	frame, layout := renderDashboardFrame(v)
	writeFullFrame(frame)
	return layout
}

func renderDashboardFrame(v dashboardView) (string, dashboardLayout) {
	switch v.overlay {
	case overlayKeys:
		return renderHelpScreen(v.size, bankedUIActive(v.results)), dashboardLayout{}
	case overlayBanked:
		return renderBankedScreen(v), dashboardLayout{}
	}

	blocks := make([][]string, len(v.results))
	total := 0
	for i := range v.results {
		blocks[i] = accountBlock(v, i)
		total += len(blocks[i])
	}

	head := []string{dashboardTitle(v), ""}
	// The footer row stays present even when there is nothing to say, so the
	// frame does not lose a line — and jump — the moment a refresh finishes.
	foot := []string{
		dim(dashboardKeys(v.results, v.size.cols)),
		dim(" " + v.footer),
	}

	start, end := 0, len(blocks)
	scroll := false
	if v.size.rows > 0 {
		// One row of headroom: the frame's last newline would otherwise push
		// the screen up by one and invalidate every absolute row below.
		avail := v.size.rows - len(head) - len(foot) - 1
		if scroll = total > avail; scroll {
			avail -= 2 // the two scroll hints
		}
		start, end = blockWindow(blocks, v.sel, avail)
	}

	var b strings.Builder
	layout := dashboardLayout{headerRows: make([]int, len(blocks))}
	for i := range layout.headerRows {
		layout.headerRows[i] = -1
	}
	row := 1
	write := func(line string) {
		b.WriteString(fitCells(line, v.size.cols))
		b.WriteByte('\n')
		row++
	}
	for _, line := range head {
		write(line)
	}
	if scroll {
		write(scrollHint("↑", start))
	}
	for i := start; i < end; i++ {
		layout.headerRows[i] = row
		for _, line := range blocks[i] {
			write(line)
		}
	}
	if scroll {
		write(scrollHint("↓", len(blocks)-end))
	}
	for _, line := range foot {
		write(line)
	}
	layout.fits = v.size.rows <= 0 || row-1 < v.size.rows
	return b.String(), layout
}

// blockWindow picks the run of account blocks to show. The selected account is
// always inside it, which is what keeps the cursor on screen and the absolute
// rows in dashboardLayout meaningful when the list is taller than the terminal.
func blockWindow(blocks [][]string, sel, avail int) (start, end int) {
	if len(blocks) == 0 {
		return 0, 0
	}
	sel = min(max(sel, 0), len(blocks)-1)
	start, end = sel, sel+1
	used := len(blocks[sel])
	for {
		grew := false
		if end < len(blocks) && used+len(blocks[end]) <= avail {
			used += len(blocks[end])
			end++
			grew = true
		}
		if start > 0 && used+len(blocks[start-1]) <= avail {
			used += len(blocks[start-1])
			start--
			grew = true
		}
		if !grew {
			return start, end
		}
	}
}

func scrollHint(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return dim(fmt.Sprintf(" %s %d more", arrow, n))
}

func dashboardTitle(v dashboardView) string {
	// The hostname used to sit here; which account is live and how old the
	// numbers are is what the screen is actually about.
	name := "no active account"
	for _, a := range v.accounts {
		if a.ID == v.state.ActiveID {
			name = a.Name
			break
		}
	}
	line := " " + bold("cx") + "  " + name
	if !v.refreshedAt.IsZero() {
		line += "  " + dim("· updated "+shortDuration(time.Since(v.refreshedAt))+" ago")
	}
	return line
}

// accountBlock renders one account as the lines it occupies, so the frame can be
// windowed by account instead of being cut at an arbitrary row.
func accountBlock(v dashboardView, i int) []string {
	selected := i == v.sel
	indent := gutter(selected, 3)
	r := v.results[i]
	lines := []string{dashboardAccountHeader(v.state, r.Account, selected)}
	banked := func() {
		lines = append(lines, bankedResetLinesWidth(r, indent, v.size.cols)...)
	}
	// The banked axis is the block's third meter line, so it is drawn with the
	// other two rather than below whatever note follows them.
	meters := func() {
		for _, line := range usageLinesWidth(r, v.size.cols) {
			lines = append(lines, indent+line)
		}
		banked()
	}

	if refreshing, note := refreshingStatus(r.Err); refreshing {
		meters()
		if !r.Usage.FetchedAt.IsZero() && !r.Usage.Fresh {
			note += " · cached " + shortDuration(time.Since(r.Usage.FetchedAt)) + " ago"
		}
		lines = append(lines, indent+cyan(note))
	} else if r.Err == "" {
		meters()
		switch {
		case r.PrimeErr != "":
			lines = append(lines, indent+red("window start failed")+" "+r.PrimeErr)
		case r.PrimeSkipped != "":
			lines = append(lines, indent+dim("window not started · "+r.PrimeSkipped))
		case r.Primed:
			lines = append(lines, indent+dim("quota windows started just now"))
		}
	} else if !r.Usage.FetchedAt.IsZero() {
		meters()
		lines = append(lines, indent+yellow("stale")+" · cached "+shortDuration(time.Since(r.Usage.FetchedAt))+" ago")
	} else {
		lines = append(lines, indent+red("unavailable")+" "+r.Err)
		banked()
	}
	return append(lines, "")
}

// keyHint is one entry of the footer. rank orders which hints are given up on a
// terminal too narrow for the whole line; rank 0 is never given up.
type keyHint struct {
	text string
	rank int
}

// dashboardKeys builds the footer. Letting fitCells trim it would cut the tail,
// which is where the key that gets you out lives, so hints are dropped from the
// line deliberately: the niche screen first, then the keys a terminal user tries
// anyway. What survives always ends with the full key list and quit.
func dashboardKeys(results []UsageResult, cols int) string {
	hints := []keyHint{
		{"↑/↓ or j/k select", 2},
		{"enter switch", 1},
		{"r refresh", 3},
	}
	// No banked resets anywhere, no key advertised — the same gate the key itself
	// and the help screen use.
	if bankedUIActive(results) {
		hints = append(hints, keyHint{"b banked", 4})
	}
	hints = append(hints, keyHint{"? keys", 0}, keyHint{"q quit", 0})

	render := func() string {
		parts := make([]string, len(hints))
		for i, h := range hints {
			parts[i] = h.text
		}
		return " " + strings.Join(parts, "   ")
	}
	line := render()
	for cols > 0 && visibleCells(line) > cols {
		worst, at := 0, -1
		for i, h := range hints {
			if h.rank > worst {
				worst, at = h.rank, i
			}
		}
		if at < 0 {
			break // Only the hints that never go are left; fitCells takes it from here.
		}
		hints = append(hints[:at], hints[at+1:]...)
		line = render()
	}
	return line
}

func renderHelpScreen(size termSize, banked bool) string {
	keys := [][2]string{
		{"↑ ↓  ·  k j", "move the cursor"},
		{"PgUp PgDn", "jump " + fmt.Sprint(pageJump) + " accounts"},
		{"g  ·  Home", "first account"},
		{"G  ·  End", "last account"},
		{"enter", "switch to the selected account"},
		{"r", "refresh quota and banked resets"},
	}
	if banked {
		keys = append(keys, [2]string{"b", "banked reset dates, every account"})
	}
	keys = append(keys,
		[2]string{"?", "this screen"},
		[2]string{"q  ·  Esc  ·  Ctrl+C", "quit"},
	)
	rows := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		rows = append(rows, "   "+padCell(key[0], 22)+dim(key[1]))
	}
	fit, spacing := overlayFit(len(rows), size.rows)
	if fit < len(rows) {
		kept := max(fit-1, 0)
		hidden := len(rows) - kept
		rows = rows[:kept:kept]
		// A budget of no rows has no room for the note about them either.
		if fit > 0 {
			rows = append(rows, dim(fmt.Sprintf("   … %d more keys", hidden)))
		}
	}
	return overlayFrame(" "+bold("cx")+"  keys", rows, size, spacing)
}

// overlayFit reports how many listing rows an overlay screen can show and
// whether the blank lines around the listing still fit. Neither overlay
// scrolls, so what does not fit has to be cut deliberately. The frame spends
// four rows on its title, its footer and those two blanks, and one more is left
// as the same headroom the dashboard keeps: the frame's last newline would
// otherwise push the screen up by one. A terminal too short even for that gives
// the blanks up first — they are spacing, and the listing is the screen.
func overlayFit(rowCount, termRows int) (fit int, spacing bool) {
	if termRows <= 0 {
		return rowCount, true
	}
	if avail := termRows - 5; avail >= 1 {
		return min(avail, rowCount), true
	}
	return min(max(termRows-3, 0), rowCount), false
}

// overlayFrame draws an overlay screen: a title, the listing, and the footer
// every overlay shares, spaced apart when overlayFit says there is room.
func overlayFrame(title string, rows []string, size termSize, spacing bool) string {
	var b strings.Builder
	b.WriteString(fitCells(title, size.cols) + "\n")
	if spacing {
		b.WriteString("\n")
	}
	for _, row := range rows {
		b.WriteString(fitCells(row, size.cols) + "\n")
	}
	if spacing {
		b.WriteString("\n")
	}
	b.WriteString(fitCells(dim(" press any key to go back"), size.cols) + "\n")
	return b.String()
}

// renderBankedScreen is the detail behind the axis on every account block: the
// dates the pips stand for. It lists every account rather than the selected one,
// because the question it answers — which account's reserve do I spend before it
// goes — is asked across the list, not within one row of it.
// bankedUIActive reports whether banked resets are worth a key at all. With none
// anywhere, `b` opens a screen that says "none" five times over, so the key does
// nothing and neither the footer nor the help screen offers it.
func bankedUIActive(results []UsageResult) bool {
	now := time.Now()
	for _, r := range results {
		if len(bankedAvailable(r.BankedResets, now)) > 0 {
			return true
		}
	}
	return false
}

func renderBankedScreen(v dashboardView) string {
	// A row remembers whether it is a reset, so a listing that has to be cut can
	// say how many resets went missing rather than how many lines did.
	type row struct {
		text  string
		reset bool
	}

	now := time.Now()
	rows := make([]row, 0, len(v.results)*2)
	for i, r := range v.results {
		name := bold(r.Account.Name)
		if i == v.sel {
			name = cyan(bold(r.Account.Name))
		}
		head := "   " + padStyled(name, r.Account.Name, accountNameCell)
		switch {
		case r.BankedErr != "":
			rows = append(rows, row{text: head + yellow("unavailable") + " · " + dim(r.BankedErr)})
		case !r.BankedLoaded:
			rows = append(rows, row{text: head + dim("not loaded")})
		default:
			resets := bankedAvailable(r.BankedResets, now)
			if len(resets) == 0 {
				rows = append(rows, row{text: head + dim("none")})
				break
			}
			rows = append(rows, row{text: head + dim(bankedCount(resets))})
			for _, reset := range resets {
				rows = append(rows, row{text: "     " + bankedDetailRow(reset, now), reset: true})
			}
		}
	}

	// An overflowing listing is cut with a note rather than losing its tail off
	// the bottom of the terminal. The note is the last line the listing gives
	// up: a screen cut down to nothing reads as accounts with no banked resets
	// at all, which is the one thing this screen must not say.
	fit, spacing := overlayFit(len(rows), v.size.rows)
	if fit < len(rows) {
		kept := max(fit-1, 0)
		hidden := 0
		for _, r := range rows[kept:] {
			if r.reset {
				hidden++
			}
		}
		note := "   … see cx status for the rest"
		if hidden > 0 {
			note = fmt.Sprintf("   … %d more resets · see cx status", hidden)
		}
		rows = rows[:kept:kept]
		// A budget of no rows has no room for the note about them either.
		if fit > 0 {
			rows = append(rows, row{text: dim(note)})
		}
	}

	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.text
	}
	return overlayFrame(" "+bold("cx")+"  banked resets", texts, v.size, spacing)
}

// Account rows are laid out in fixed cells so the plan and email columns line
// up down the list.
const (
	accountNameCell = 18
	accountPlanCell = 10
)

// The cursor is a bar drawn down the whole selected block rather than a caret on
// its header row: every account is several lines tall, so a single marked row
// left the eye guessing where the selection started and ended. gutter returns
// the leading cells of one line of a block — the bar sits inside the frame's
// left margin and is padded back out to width, so the columns to its right stay
// aligned whether or not the account is selected.
func gutter(selected bool, width int) string {
	if !selected {
		return strings.Repeat(" ", width)
	}
	return " " + cyan("▌") + strings.Repeat(" ", max(width-2, 0))
}

func dashboardAccountHeader(st State, a Account, selected bool) string {
	active := dim("○")
	if a.ID == st.ActiveID {
		active = green("●")
	}
	// The selected account's name carries the highlight too, so the cursor is
	// still readable where a terminal renders the bar faintly.
	name := bold(a.Name)
	if selected {
		name = cyan(bold(a.Name))
	}
	var b strings.Builder
	// Pad before styling: a width verb counts the escape bytes of an already
	// colored string as characters, which silently shrinks the column.
	fmt.Fprintf(&b, "%s%s %s", gutter(selected, 2), active, padStyled(name, a.Name, accountNameCell))
	// The plan cell keeps its width even when the account has no plan, so an
	// account without one does not slide its email into the plan column.
	if a.Plan != "" || a.Email != "" {
		b.WriteString(" " + padCell(a.Plan, accountPlanCell))
	}
	if a.Email != "" {
		b.WriteString(" " + dim(a.Email))
	}
	return strings.TrimRight(b.String(), " ")
}

func writeFullFrame(frame string) {
	var b strings.Builder
	b.Grow(len(frame) + 64)
	b.WriteString(syncOutputBegin)
	b.WriteString(clearScreenHome)
	b.WriteString(frame)
	b.WriteString(syncOutputEnd)
	_, _ = os.Stdout.WriteString(b.String())
}

// selectionUpdateString repaints just the two account blocks a cursor move
// touched — the one that lost the gutter bar and the one that gained it. A block
// keeps its line count whichever way it is drawn, so the rows below it do not
// move. It returns "" whenever the absolute rows cannot be trusted — a frame
// taller than the screen has scrolled, and a scrolled-out account has no row at
// all — and the caller then redraws the whole frame instead.
func selectionUpdateString(v dashboardView, oldSel, newSel int, layout dashboardLayout) string {
	rows := layout.headerRows
	if !layout.fits || oldSel < 0 || newSel < 0 ||
		oldSel >= len(v.results) || newSel >= len(v.results) ||
		oldSel >= len(rows) || newSel >= len(rows) ||
		rows[oldSel] < 0 || rows[newSel] < 0 {
		return ""
	}
	v.sel = newSel
	var b strings.Builder
	b.WriteString(syncOutputBegin)
	for _, idx := range []int{oldSel, newSel} {
		row := rows[idx]
		for _, line := range accountBlock(v, idx) {
			fmt.Fprintf(&b, "\x1b[%d;1H%s%s", row, eraseCurrentLine, fitCells(line, v.size.cols))
			row++
		}
	}
	b.WriteString(syncOutputEnd)
	return b.String()
}

func pickAccount(p paths) (Account, error) {
	accounts, err := listAccounts(p)
	if err != nil {
		return Account{}, err
	}
	if len(accounts) == 0 {
		return Account{}, errors.New("no accounts")
	}
	if !isTerminal() {
		return Account{}, errors.New("interactive selection requires a terminal")
	}
	old, err := beginTerminalSession()
	if err != nil {
		return Account{}, err
	}
	defer endTerminalSession(old)
	bytes := keyBytes()
	defer discardPendingKeys(bytes)
	sel := 0
	for {
		writeFullFrame(renderAccountPicker(accounts, sel))
		k, e := readKeyFrom(bytes)
		if e != nil {
			return Account{}, e
		}
		switch k {
		case "enter":
			return accounts[sel], nil
		case "esc", "q", "ctrl-c", "ctrl-d":
			return Account{}, errors.New("cancelled")
		default:
			sel = moveSelection(sel, len(accounts), k)
		}
	}
}

func renderAccountPicker(accounts []Account, sel int) string {
	var b strings.Builder
	b.WriteString(bold(" Switch Codex account"))
	b.WriteString("\n\n")
	for i, a := range accounts {
		selected := i == sel
		name := a.Name
		if selected {
			name = cyan(bold(a.Name))
		}
		fmt.Fprintf(&b, "%s%s %s\n", gutter(selected, 2), padStyled(name, a.Name, 18), dim(emptyDash(a.Email)))
	}
	b.WriteString("\n")
	b.WriteString(dim(" ↑/↓ or j/k · enter switch · esc cancel"))
	b.WriteByte('\n')
	return b.String()
}

func isTerminal() bool { s, _ := os.Stdin.Stat(); return s != nil && (s.Mode()&os.ModeCharDevice) != 0 }

func beginTerminalSession() (string, error) {
	old, err := rawMode()
	if err != nil {
		return "", err
	}
	_, _ = os.Stdout.WriteString(altScreenEnter + hideCursor + disableWrap + clearScreenHome)
	return old, nil
}

func endTerminalSession(old string) {
	_, _ = os.Stdout.WriteString("\x1b[0m" + enableWrap + showCursor + altScreenExit)
	restoreMode(old)
}

func rawMode() (string, error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	b, err := get.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return "", fmt.Errorf("read terminal mode: %w: %s", err, msg)
		}
		return "", fmt.Errorf("read terminal mode: %w", err)
	}
	old := strings.TrimSpace(string(b))
	cmd := exec.Command("stty", rawModeArgs()...)
	cmd.Stdin = os.Stdin
	b, err = cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return "", fmt.Errorf("enable terminal raw mode: %w: %s", err, msg)
		}
		return "", fmt.Errorf("enable terminal raw mode: %w", err)
	}
	return old, nil
}

func rawModeArgs() []string {
	// `stty raw` disables output post-processing, which makes `\n` move the
	// cursor down without returning it to column zero. Keep raw input while
	// restoring normal terminal line endings for buffered frame output.
	return []string{"raw", "-echo", "opost", "onlcr"}
}

func restoreMode(old string) {
	if old == "" {
		return
	}
	cmd := exec.Command("stty", old)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

// watchTerminalSignals restores the terminal when the process is killed. A
// signal skips the deferred cleanup, which would hand the user back a shell
// still in raw mode, on the alternate screen, with no cursor.
func watchTerminalSignals(old string) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			endTerminalSession(old)
			os.Exit(1)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// watchResize reports terminal size changes. Without it a resized window leaves
// both the frame and the absolute rows the cursor writes to describing a screen
// that no longer exists.
func watchResize() (<-chan struct{}, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				select {
				case out <- struct{}{}:
				default: // A redraw is already pending; one is enough.
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		signal.Stop(ch)
		close(done)
	}
}

type keyEvent struct {
	key string
	err error
}

// keyStream reads keys on a goroutine so a screen can serve input while it is
// still waiting on something else. The reader outlives any single wait, so a key
// it has already pulled off the terminal is delivered to the next receive
// instead of being lost between them.
type keyStream struct {
	ch    chan keyEvent
	bytes chan byteRead
}

func newKeyStream() *keyStream {
	// Resolved here, on the caller's goroutine: the reader below then never
	// touches os.Stdin or the stream globals.
	bytes := keyBytes()
	s := &keyStream{ch: make(chan keyEvent, 16), bytes: bytes}
	go func() {
		for {
			key, err := readKeyFrom(bytes)
			s.ch <- keyEvent{key: key, err: err}
			if err != nil {
				return
			}
		}
	}()
	return s
}

// drain drops the keys typed ahead of an action, so that holding a key down
// cannot replay it: switching rewrites the auth.json symlink and refreshing
// costs a full round of network reads.
func (s *keyStream) drain() {
	discardPendingKeys(s.bytes)
	for {
		select {
		case <-s.ch:
		default:
			return
		}
	}
}

// Terminal bytes are read by exactly one goroutine, whose channel every reader
// then shares. A single owner is what makes a bounded wait safe: the byte the
// wait gave up on stays queued for the next read instead of being lost, and no
// second reader is left racing on the buffer.
type byteRead struct {
	b   byte
	err error
}

var (
	byteMu     sync.Mutex
	byteSource *os.File
	byteChan   chan byteRead
)

// keyBytes returns the channel of terminal bytes, starting the reader on first
// use and rebinding it when os.Stdin itself changes. Callers resolve the channel
// once and then work with it directly, so the globals here are only ever touched
// from the goroutine that asked for the stream.
func keyBytes() chan byteRead {
	byteMu.Lock()
	defer byteMu.Unlock()
	if byteChan == nil || byteSource != os.Stdin {
		byteSource = os.Stdin
		ch := make(chan byteRead, 64)
		byteChan = ch
		r := bufio.NewReader(os.Stdin)
		go func() {
			for {
				b, err := r.ReadByte()
				ch <- byteRead{b: b, err: err}
				if err != nil {
					return
				}
			}
		}()
	}
	return byteChan
}

func nextByte(ch chan byteRead) (byte, error) {
	got := <-ch
	return got.b, got.err
}

// nextByteWithin waits a bounded time for the next byte. A wait that expires
// leaves the byte queued, so giving up costs nothing.
func nextByteWithin(ch chan byteRead, d time.Duration) (byte, bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case got := <-ch:
		return got.b, got.err == nil
	case <-timer.C:
		return 0, false
	}
}

// discardPendingKeys drops terminal input that arrived but has not been read
// yet. It is called when leaving an input loop, so that keys typed ahead of an
// action cannot replay it — switching writes the auth.json symlink and
// refreshing costs a full round of network reads.
func discardPendingKeys(ch chan byteRead) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// escContinuationWait bounds the wait for the byte after Esc. It has to outlast
// a fragmented arrow-key sequence without making a bare Esc keypress feel slow.
// Over a high-latency link the two halves of an arrow key can land tens of
// milliseconds apart, and a window that expires first reads the arrow as Esc.
const escContinuationWait = 150 * time.Millisecond

func readEscapeSequence(ch chan byteRead) (string, error) {
	// A terminal sends a whole sequence in one burst, so only a bare Esc
	// actually waits here, and only briefly.
	intro, ok := nextByteWithin(ch, escContinuationWait)
	if !ok {
		return "esc", nil
	}
	if intro != '[' && intro != 'O' {
		return "", nil // Alt+key: consumed, not acted on.
	}
	// CSI and SS3 both run parameter and intermediate bytes up to one final
	// byte in 0x40-0x7e.
	var params []byte
	var final byte
	for {
		c, err := nextByte(ch)
		if err != nil {
			return "", err
		}
		if c >= 0x40 && c <= 0x7e {
			final = c
			break
		}
		if len(params) >= 16 {
			return "", nil // Malformed; stop rather than read without bound.
		}
		params = append(params, c)
	}
	switch final {
	case 'A':
		return "up", nil
	case 'B':
		return "down", nil
	case 'C':
		return "right", nil
	case 'D':
		return "left", nil
	case 'H':
		return "home", nil
	case 'F':
		return "end", nil
	case '~':
		switch string(params) {
		case "1", "7":
			return "home", nil
		case "4", "8":
			return "end", nil
		case "5":
			return "pgup", nil
		case "6":
			return "pgdn", nil
		}
	}
	return "", nil
}

func readKey() (string, error) { return readKeyFrom(keyBytes()) }

func readKeyFrom(ch chan byteRead) (string, error) {
	b, err := nextByte(ch)
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case 3:
		// Raw mode clears ISIG, so Ctrl+C arrives as a byte and never as a
		// signal. Left unnamed it matched no case and did nothing at all.
		return "ctrl-c", nil
	case 4:
		return "ctrl-d", nil
	case 27:
		return readEscapeSequence(ch)
	default:
		return string([]byte{b}), nil
	}
}
