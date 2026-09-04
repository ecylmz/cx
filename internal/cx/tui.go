package cx

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

type dashboardLayout struct {
	headerRows []int
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
		printStatus(p, accounts, results)
		return nil
	}

	old, err := beginTerminalSession()
	if err != nil {
		return err
	}
	defer endTerminalSession(old)

	sel := 0
	for {
		results := initialRefreshingResults(p, accounts)
		layout := drawDashboardFrame(p, accounts, results, sel, refreshFooter(0, len(accounts)))
		cache, _ := loadCache(p)
		completed := 0

		for update := range fetchUsageUpdatesWithPriming(p, accounts) {
			r := update.Result
			if update.Final {
				completed++
				applyCachedUsageFallbackResult(cache, &r)
			}
			results[update.Index] = r
			footer := refreshFooter(completed, len(accounts))
			if completed == len(accounts) {
				footer = "live 5-hour + weekly quota · live banked resets"
			}
			layout = drawDashboardFrame(p, accounts, results, sel, footer)
		}
		saveFreshCache(p, results)

		idx, action, err := dashboardInputLoop(p, accounts, sel, layout)
		if err != nil {
			return err
		}
		sel = idx
		switch action {
		case "quit":
			return nil
		case "refresh":
			continue
		case "switch":
			if err := switchAccount(p, accounts[sel]); err != nil {
				return err
			}
			accounts, _ = listAccounts(p)
			sel = 0
			continue
		}
	}
}

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

func drawCachedDashboard(p paths, accounts []Account, sel int) {
	results := initialRefreshingResults(p, accounts)
	frame, _ := renderDashboardFrame(p, accounts, results, sel, refreshFooter(0, len(accounts)))
	writeFullFrame(frame)
}

func dashboardLoop(p paths, accounts []Account, results []UsageResult, sel int) (int, string, error) {
	if !isTerminal() {
		printStatus(p, accounts, results)
		return sel, "quit", nil
	}
	old, err := beginTerminalSession()
	if err != nil {
		return sel, "", err
	}
	defer endTerminalSession(old)
	layout := drawDashboardFrame(p, accounts, results, sel, "live 5-hour + weekly quota · live banked resets")
	return dashboardInputLoop(p, accounts, sel, layout)
}

func dashboardInputLoop(p paths, accounts []Account, sel int, layout dashboardLayout) (int, string, error) {
	for {
		key, err := readKey()
		if err != nil {
			return sel, "", err
		}
		oldSel := sel
		switch key {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(accounts)-1 {
				sel++
			}
		case "enter":
			return sel, "switch", nil
		case "r":
			return sel, "refresh", nil
		case "q", "esc":
			return sel, "quit", nil
		}
		if sel != oldSel {
			writeSelectionUpdate(p, accounts, oldSel, sel, layout)
		}
	}
}

func drawDashboard(p paths, accounts []Account, results []UsageResult, sel int) {
	frame, _ := renderDashboardFrame(p, accounts, results, sel, "live 5-hour + weekly quota · live banked resets")
	fmt.Print(clearScreenHome)
	fmt.Print(frame)
}

func drawDashboardFrame(p paths, accounts []Account, results []UsageResult, sel int, footer string) dashboardLayout {
	frame, layout := renderDashboardFrame(p, accounts, results, sel, footer)
	writeFullFrame(frame)
	return layout
}

func renderDashboardFrame(p paths, accounts []Account, results []UsageResult, sel int, footer string) (string, dashboardLayout) {
	var b strings.Builder
	layout := dashboardLayout{headerRows: make([]int, 0, len(results))}
	row := 1
	host, _ := os.Hostname()
	fmt.Fprintf(&b, " %s  %s\n\n", bold("cx"), dim(host))
	row += 2
	st, _ := loadState(p)
	for i, r := range results {
		layout.headerRows = append(layout.headerRows, row)
		b.WriteString(dashboardAccountHeader(st, r.Account, i == sel))
		b.WriteByte('\n')
		row++

		if refreshing, note := refreshingStatus(r.Err); refreshing {
			lines := usageLines(r)
			for _, line := range lines {
				b.WriteString("   " + line[2:] + "\n")
			}
			row += len(lines)
			if !r.Usage.FetchedAt.IsZero() && !r.Usage.Fresh {
				note += " · cached " + shortDuration(time.Since(r.Usage.FetchedAt)) + " ago"
			}
			fmt.Fprintf(&b, "   %s\n", cyan(note))
			row++
		} else if r.Err == "" {
			lines := usageLines(r)
			for _, line := range lines {
				b.WriteString("   " + line[2:] + "\n")
			}
			row += len(lines)
			if r.PrimeErr != "" {
				fmt.Fprintf(&b, "   %s %s\n", red("window start failed"), r.PrimeErr)
				row++
			} else if r.PrimeSkipped != "" {
				b.WriteString("   " + dim("window not started · "+r.PrimeSkipped) + "\n")
				row++
			} else if r.Primed {
				b.WriteString("   " + dim("quota windows started just now") + "\n")
				row++
			}
		} else if !r.Usage.FetchedAt.IsZero() {
			lines := usageLines(r)
			for _, line := range lines {
				b.WriteString("   " + line[2:] + "\n")
			}
			fmt.Fprintf(&b, "   %s · cached %s ago\n", yellow("stale"), shortDuration(time.Since(r.Usage.FetchedAt)))
			row += len(lines) + 1
		} else {
			fmt.Fprintf(&b, "   %s %s\n", red("unavailable"), r.Err)
			row++
		}
		for _, line := range bankedResetLines(r, "   ") {
			b.WriteString(line)
			b.WriteByte('\n')
			row++
		}
		b.WriteByte('\n')
		row++
	}
	b.WriteString(dim(" ↑/↓ or j/k select   enter switch   r refresh   q quit"))
	b.WriteByte('\n')
	b.WriteString(dim(" " + footer))
	b.WriteByte('\n')
	return b.String(), layout
}

func dashboardAccountHeader(st State, a Account, selected bool) string {
	cursor := "  "
	if selected {
		cursor = cyan("› ")
	}
	active := dim("○")
	if a.ID == st.ActiveID {
		active = green("●")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s %-18s", cursor, active, bold(a.Name))
	if a.Plan != "" {
		fmt.Fprintf(&b, " %-10s", a.Plan)
	}
	if a.Email != "" {
		fmt.Fprintf(&b, " %s", dim(a.Email))
	}
	return b.String()
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

func selectionUpdateString(p paths, accounts []Account, oldSel, newSel int, layout dashboardLayout) string {
	if oldSel < 0 || newSel < 0 || oldSel >= len(accounts) || newSel >= len(accounts) ||
		oldSel >= len(layout.headerRows) || newSel >= len(layout.headerRows) {
		return ""
	}
	st, _ := loadState(p)
	var b strings.Builder
	b.WriteString(syncOutputBegin)
	for _, idx := range []int{oldSel, newSel} {
		fmt.Fprintf(&b, "\x1b[%d;1H%s%s", layout.headerRows[idx], eraseCurrentLine, dashboardAccountHeader(st, accounts[idx], idx == newSel))
	}
	b.WriteString(syncOutputEnd)
	return b.String()
}

func writeSelectionUpdate(p paths, accounts []Account, oldSel, newSel int, layout dashboardLayout) {
	if update := selectionUpdateString(p, accounts, oldSel, newSel, layout); update != "" {
		_, _ = os.Stdout.WriteString(update)
	}
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
	sel := 0
	for {
		writeFullFrame(renderAccountPicker(accounts, sel))
		k, e := readKey()
		if e != nil {
			return Account{}, e
		}
		switch k {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(accounts)-1 {
				sel++
			}
		case "enter":
			return accounts[sel], nil
		case "esc", "q":
			return Account{}, errors.New("cancelled")
		}
	}
}

func renderAccountPicker(accounts []Account, sel int) string {
	var b strings.Builder
	b.WriteString(bold(" Switch Codex account"))
	b.WriteString("\n\n")
	for i, a := range accounts {
		c := "  "
		if i == sel {
			c = cyan("› ")
		}
		fmt.Fprintf(&b, "%s%-18s %s\n", c, a.Name, dim(emptyDash(a.Email)))
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

func readKey() (string, error) {
	r := bufio.NewReader(os.Stdin)
	b, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case 27:
		b2, _ := r.ReadByte()
		if b2 != '[' {
			return "esc", nil
		}
		b3, _ := r.ReadByte()
		if b3 == 'A' {
			return "up", nil
		}
		if b3 == 'B' {
			return "down", nil
		}
		return "esc", nil
	default:
		return string([]byte{b}), nil
	}
}
