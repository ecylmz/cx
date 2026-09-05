package cx

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

var useColor = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	// A forced setting is how a caller keeps color through a pipe, for example
	// `cx status | less -R`, where the char-device test below says no.
	for _, key := range []string{"FORCE_COLOR", "CLICOLOR_FORCE"} {
		if v := os.Getenv(key); v != "" && v != "0" {
			return true
		}
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	s, _ := os.Stdout.Stat()
	return s != nil && (s.Mode()&os.ModeCharDevice) != 0
}

// ansi closes with the code that undoes just this attribute rather than with a
// blanket reset, so a styled string nested inside another one does not strip the
// style around it. Bold and dim share a closer, so those two still cannot nest.
func ansi(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[" + ansiOff(code) + "m"
}

func ansiOff(code string) string {
	switch code {
	case "1", "2":
		return "22"
	default:
		return "39"
	}
}

func green(s string) string  { return ansi("32", s) }
func yellow(s string) string { return ansi("33", s) }
func red(s string) string    { return ansi("31", s) }
func cyan(s string) string   { return ansi("36", s) }
func bold(s string) string   { return ansi("1", s) }
func dim(s string) string    { return ansi("2", s) }

// visibleCells counts the display cells a rendered line occupies, skipping the
// escape sequences that carry no width.
func visibleCells(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = escapeEnd(s, i)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

// escapeEnd returns the index just past the escape sequence starting at i.
func escapeEnd(s string, i int) int {
	i++ // the Esc byte itself
	if i < len(s) && (s[i] == '[' || s[i] == ']' || s[i] == 'O') {
		i++
	}
	for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
		i++
	}
	if i < len(s) {
		i++
	}
	return i
}

// fitCells truncates a rendered line to cols display cells, keeping every escape
// sequence whole and closing whatever style the cut ran through. The dashboard
// runs with line wrapping disabled, so an over-long line would otherwise be
// chopped at the margin — sometimes mid-sequence. cols <= 0 means unbounded.
func fitCells(s string, cols int) string {
	if cols <= 0 || visibleCells(s) <= cols {
		return s
	}
	var b strings.Builder
	styled := false
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			end := escapeEnd(s, i)
			b.WriteString(s[i:end])
			styled = true
			i = end
			continue
		}
		if n >= cols-1 {
			break
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
		n++
	}
	b.WriteString("…")
	if styled {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// padCell pads s to n display cells. It must be given plain text: a width verb
// applied to an already styled string counts its escape bytes as characters.
func padCell(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// padStyled pads an already styled string to n cells, measuring the plain text
// it was built from. The padding sits outside the styling, so a trailing cell
// stays plain whitespace that can be trimmed off the end of a line.
func padStyled(styled, plain string, n int) string {
	if w := utf8.RuneCountInString(plain); w < n {
		return styled + strings.Repeat(" ", n-w)
	}
	return styled
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func quotaColor(left float64, s string) string {
	if left < 20 {
		return red(s)
	}
	if left < 50 {
		return yellow(s)
	}
	return green(s)
}
func clamp(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

// barCells splits a percentage into the filled and empty halves of a meter, so
// the plain and the dimmed variant stay pixel-identical.
func barCells(percent float64, width int) (filled, empty string) {
	n := int(math.Round(clamp(percent, 0, 100) / 100 * float64(width)))
	return strings.Repeat("█", n), strings.Repeat("░", width-n)
}
func quotaBar(left float64, width int) string {
	filled, empty := barCells(left, width)
	return filled + dim(empty)
}
func normalizeLocale(v string) string {
	v, _, _ = strings.Cut(strings.TrimSpace(v), ".")
	v, _, _ = strings.Cut(v, "@")
	return strings.ToLower(strings.ReplaceAll(v, "-", "_"))
}

func localeName() string {
	for _, key := range []string{"LC_TIME", "LC_ALL"} {
		if v := normalizeLocale(os.Getenv(key)); v != "" && v != "c" && v != "posix" {
			return v
		}
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output(); err == nil {
			if v := normalizeLocale(string(out)); v != "" {
				return v
			}
		}
	}
	if v := normalizeLocale(os.Getenv("LANG")); v != "" && v != "c" && v != "posix" {
		return v
	}
	return ""
}

// localDateLayout picks the date order the locale expects. The clock is 24-hour
// everywhere, including en_US: these timestamps sit next to relative countdowns
// in a dense column, and "3:04 PM" reads slower and takes more room than "15:04"
// without telling the reader anything more.
func localDateLayout(locale string) string {
	locale = normalizeLocale(locale)
	switch {
	case strings.HasPrefix(locale, "en_us"):
		return "01/02/2006 15:04"
	case strings.HasPrefix(locale, "en_ca"):
		return "2006-01-02 15:04"
	case strings.HasPrefix(locale, "ja"), strings.HasPrefix(locale, "ko"), strings.HasPrefix(locale, "zh"):
		return "2006/01/02 15:04"
	case strings.HasPrefix(locale, "tr"), strings.HasPrefix(locale, "de"), strings.HasPrefix(locale, "da"), strings.HasPrefix(locale, "fi"), strings.HasPrefix(locale, "no"), strings.HasPrefix(locale, "ru"):
		return "02.01.2006 15:04"
	case strings.HasPrefix(locale, "en_gb"), strings.HasPrefix(locale, "fr"), strings.HasPrefix(locale, "es"), strings.HasPrefix(locale, "it"), strings.HasPrefix(locale, "pt"), strings.HasPrefix(locale, "nl"):
		return "02/01/2006 15:04"
	default:
		return "2006-01-02 15:04"
	}
}

func exactResetText(epoch int64) string {
	if epoch <= 0 {
		return "unknown"
	}
	return time.Unix(epoch, 0).Local().Format(localDateLayout(localeName()))
}

// resetTextCompact drops the absolute timestamp and keeps the part a narrow
// terminal has room for. Truncating the full text instead cut away the relative
// time, which is the half that answers "when does this come back".
func resetTextCompact(epoch int64, now time.Time) string {
	if epoch <= 0 {
		return "unknown"
	}
	d := time.Unix(epoch, 0).Local().Sub(now)
	if d <= 0 {
		return "now"
	}
	return "in " + relativeDuration(d)
}

func resetText(epoch int64, now time.Time) string {
	if epoch <= 0 {
		return "unknown"
	}
	t := time.Unix(epoch, 0).Local()
	d := t.Sub(now)
	exact := t.Format(localDateLayout(localeName()))
	if d <= 0 {
		return exact + " · now"
	}
	return exact + " · in " + relativeDuration(d)
}

func relativeDuration(d time.Duration) string {
	d = max(d, 0)
	days := int(d / (24 * time.Hour))
	hours := int(d/time.Hour) % 24
	mins := int(d/time.Minute) % 60
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func shortDuration(d time.Duration) string {
	d = max(d, 0)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func looksLikeUnstartedWindow(u WeeklyUsage, now time.Time) bool {
	if u.WindowStarted || u.UsedPercent != 0 || u.WindowMinutes <= 0 || u.ResetsAt <= 0 {
		return false
	}
	ref := now
	if !u.FetchedAt.IsZero() {
		ref = u.FetchedAt
	}
	want := time.Duration(u.WindowMinutes) * time.Minute
	got := time.Unix(u.ResetsAt, 0).Sub(ref)
	// The backend represents an unused rolling window with a reset roughly one
	// full window ahead. Keep the tolerance tight so a real zero-percent window
	// becomes visibly active within seconds instead of looking idle for minutes.
	tolerance := 10 * time.Second
	return got >= want-tolerance && got <= want+tolerance
}

const defaultBarCells = 22

// barWidthFor shrinks the meter on a narrow terminal rather than letting the
// percentage and the reset time run off the right margin.
func barWidthFor(cols int) int {
	if cols <= 0 {
		return defaultBarCells
	}
	return min(max(cols/3, 8), defaultBarCells)
}

// compactBelowCols is the width under which the usage line drops detail rather
// than having it cut off at the margin.
const compactBelowCols = 80

func usageLine(u WeeklyUsage) string {
	return labeledUsageLine("weekly", u, defaultBarCells, false)
}

func fiveHourUsageLine(u WeeklyUsage) string {
	return labeledUsageLine("5 hour", u, defaultBarCells, false)
}

// labeledUsageLine returns the line unindented; each caller owns its own
// indentation instead of re-indenting a string the other one built.
func labeledUsageLine(label string, u WeeklyUsage, barWidth int, compact bool) string {
	now := time.Now()
	left := clamp(100-u.UsedPercent, 0, 100)
	pct := quotaColor(left, fmt.Sprintf("%5.1f%% left", left))
	base := fmt.Sprintf("%s  %s  %s", padCell(label, 6), quotaBar(left, barWidth), pct)
	if looksLikeUnstartedWindow(u, now) {
		if label == "5 hour" && !compact {
			return base + "   " + dim("not started · starts with Codex use")
		}
		return base + "   " + dim("not started")
	}
	if compact {
		return base + "   " + dim(resetTextCompact(u.ResetsAt, now))
	}
	return base + "   " + dim("resets "+resetText(u.ResetsAt, now))
}

// usageLines renders at full width, for output that wraps instead of being
// clipped at the margin.
func usageLines(r UsageResult) []string { return usageLinesWidth(r, 0) }

// usageLinesWidth fits the meter and the trailing detail to cols display cells.
// cols <= 0 means unbounded.
func usageLinesWidth(r UsageResult, cols int) []string {
	barWidth, compact := barWidthFor(cols), cols > 0 && cols < compactBelowCols
	lines := make([]string, 0, 2)
	if r.FiveHour != nil && r.FiveHour.WindowMinutes > 0 {
		lines = append(lines, labeledUsageLine("5 hour", *r.FiveHour, barWidth, compact))
	}
	if r.Usage.WindowMinutes > 0 || !r.Usage.FetchedAt.IsZero() {
		lines = append(lines, labeledUsageLine("weekly", r.Usage, barWidth, compact))
	}
	return lines
}

// bankedHorizon is the life of a banked reset. The axis below spans exactly this
// much time on every account, so a pip's column means the same day down the
// whole list: the axes can be read against each other instead of each carrying
// its own scale.
const bankedHorizon = 30 * 24 * time.Hour

const (
	bankedPip      = "●"
	bankedPipStack = "◉"
	bankedRule     = "─"
	bankedTick     = "┼"
)

// pipStyle is how urgent one cell of the axis is, kept as a value rather than a
// styling function so a run of equally styled cells can be written under a
// single escape sequence instead of one per character.
type pipStyle int

const (
	pipPlain pipStyle = iota
	pipDim
	pipWarn
	pipUrgent
)

func (s pipStyle) apply(text string) string {
	switch s {
	case pipDim:
		return dim(text)
	case pipWarn:
		return yellow(text)
	case pipUrgent:
		return red(text)
	default:
		return text
	}
}

// bankedPipStyle colours a reset the way quotaColor colours a percentage: red
// once it is within a day of being lost, yellow within a week.
func bankedPipStyle(d time.Duration) pipStyle {
	switch {
	case d < 24*time.Hour:
		return pipUrgent
	case d < 7*24*time.Hour:
		return pipWarn
	default:
		return pipPlain
	}
}

// bankedColumn maps a time from now onto the axis. Anything already past sits in
// the first cell and anything beyond the horizon in the last, so a stale reading
// still lands on the axis rather than off the end of it.
func bankedColumn(d time.Duration, width int) int {
	if d <= 0 {
		return 0
	}
	return min(int(float64(d)/float64(bankedHorizon)*float64(width)), width-1)
}

// bankedAxis draws the expiries into the meter column, where position carries
// when a reset goes rather than its place in a list. Ticks mark the week
// boundaries, and a cell holding more than one reset is drawn filled — otherwise
// the axis and the count beside it would disagree about how many there are. It
// returns the styled line and the plain text behind it, for padStyled.
func bankedAxis(resets []BankedReset, now time.Time, width int) (styled, plain string) {
	if width <= 0 {
		return "", ""
	}
	count := make([]int, width)
	soonest := make([]time.Duration, width)
	for _, r := range resets {
		t, ok := bankedExpiry(r.ExpiresAt)
		if !ok {
			continue // An undated reset still counts on the right; it cannot be placed.
		}
		d := t.Sub(now)
		col := bankedColumn(d, width)
		if count[col] == 0 || d < soonest[col] {
			soonest[col] = d
		}
		count[col]++
	}
	ticks := make(map[int]bool)
	for at := 7 * 24 * time.Hour; at < bankedHorizon; at += 7 * 24 * time.Hour {
		if col := bankedColumn(at, width); col > 0 {
			ticks[col] = true
		}
	}

	var out, text strings.Builder
	run, runStyle := "", pipDim
	flush := func() {
		if run != "" {
			out.WriteString(runStyle.apply(run))
			run = ""
		}
	}
	for i := range width {
		glyph, style := bankedRule, pipDim
		switch {
		case count[i] == 1:
			glyph, style = bankedPip, bankedPipStyle(soonest[i])
		case count[i] > 1:
			glyph, style = bankedPipStack, bankedPipStyle(soonest[i])
		case ticks[i]:
			glyph = bankedTick
		}
		if style != runStyle {
			flush()
			runStyle = style
		}
		run += glyph
		text.WriteString(glyph)
	}
	flush()
	return out.String(), text.String()
}

// bankedAvailable drops the resets whose expiry has passed. The backend only
// returns credits it still counts as available, so an expired one is the clock
// on screen having run past the last fetch rather than something the account
// still holds. Every part of the banked display works from this list, so the
// pips, the count and the countdown cannot disagree about what is left.
func bankedAvailable(resets []BankedReset, now time.Time) []BankedReset {
	live := make([]BankedReset, 0, len(resets))
	for _, r := range resets {
		if t, ok := bankedExpiry(r.ExpiresAt); ok && !t.After(now) {
			continue
		}
		live = append(live, r)
	}
	return live
}

// bankedUndated counts the resets the backend gave no expiry for. They cannot be
// placed on the axis, so the count cell has to name them: otherwise the pips
// could be counted to a different total than the number printed beside them.
func bankedUndated(resets []BankedReset) int {
	n := 0
	for _, r := range resets {
		if _, ok := bankedExpiry(r.ExpiresAt); !ok {
			n++
		}
	}
	return n
}

// bankedCountdown is the one banked date that turns into a decision: when the
// next reset goes. The rest are inventory, and live behind `b` and in
// `cx status`. Resets arrive sorted by expiry, but the soonest one can already
// have passed — the list is only refetched on demand, while the clock on screen
// keeps running — so this walks to the first reset still in the future rather
// than reporting the head of the list as gone while later ones are live.
func bankedCountdown(resets []BankedReset, now time.Time, compact bool) string {
	lead := "next in "
	// A narrow terminal gives up the word, not the number: how long the next
	// reset has left is what this line is read for.
	if compact {
		lead = "in "
	}
	dated := false
	for _, r := range resets {
		t, ok := bankedExpiry(r.ExpiresAt)
		if !ok {
			continue // Counted by bankedUndated; it has no countdown to report.
		}
		dated = true
		if d := t.Sub(now); d > 0 {
			return lead + relativeDuration(d)
		}
	}
	if dated {
		return "expired"
	}
	return ""
}

// bankedLine is the third meter line of an account block, laid out in the same
// cells as the two quota lines above it: the axis where the bar goes, the count
// where the percentage goes, the countdown where the reset time goes.
func bankedLine(resets []BankedReset, now time.Time, barWidth int, compact bool) string {
	styled, plain := bankedAxis(resets, now, barWidth)
	line := fmt.Sprintf("%s  %s  %s", padCell("banked", 6), padStyled(styled, plain, barWidth),
		fmt.Sprintf("%11s", bankedCount(resets)))

	if tail := bankedCountdown(resets, now, compact); tail != "" {
		line += "   " + dim(tail)
	}
	return line
}

// bankedCount fills the value cell, where the two quota lines print their
// percentage: how many resets are banked, and how many of those the axis could
// not place. The undated ones are named inside the cell rather than in a note
// after it, because a note is the first thing a narrow terminal cuts — and
// cutting it is what would leave the pips and the count disagreeing on screen
// with nothing left to explain why.
func bankedCount(resets []BankedReset) string {
	count := fmt.Sprintf("%d left", len(resets))
	if n := bankedUndated(resets); n > 0 {
		count += fmt.Sprintf(" · %d?", n)
	}
	return count
}

// bankedDateCell holds the timestamp column of a full listing, wide enough for
// every layout localDateLayout can return.
const bankedDateCell = 22

// bankedDetailRow is one reset in a full listing: a pip carrying the same
// urgency colour the axis gives it, the date it goes, and the countdown.
func bankedDetailRow(reset BankedReset, now time.Time) string {
	t, ok := bankedExpiry(reset.ExpiresAt)
	if !ok {
		return dim(bankedPip) + "  " + dim("expires unknown")
	}
	d := t.Sub(now)
	row := bankedPipStyle(d).apply(bankedPip) + "  " + padCell(t.Local().Format(localDateLayout(localeName())), bankedDateCell)
	if d > 0 {
		return row + dim("in "+relativeDuration(d))
	}
	return row + dim("expired "+shortDuration(-d)+" ago")
}

// bankedUnavailable is the line an account gets when its banked resets could not
// be read at all, laid out in the same cells as the meter it replaces.
func bankedUnavailable(err string) string {
	return padCell("banked", 6) + "  " + yellow("unavailable") + " · " + dim(err)
}

func bankedResetLines(r UsageResult, indent string) []string {
	return bankedResetLinesWidth(r, indent, 0)
}

// bankedResetLinesWidth renders an account's banked resets for the dashboard:
// one line, whatever the count. The dates behind it are a keypress away.
func bankedResetLinesWidth(r UsageResult, indent string, cols int) []string {
	if !r.BankedLoaded {
		return nil
	}
	if r.BankedErr != "" {
		return []string{indent + bankedUnavailable(r.BankedErr)}
	}
	now := time.Now()
	resets := bankedAvailable(r.BankedResets, now)
	if len(resets) == 0 {
		return nil
	}
	return []string{indent + bankedLine(resets, now, barWidthFor(cols), cols > 0 && cols < compactBelowCols)}
}

// bankedAxisIndent lines a detail row up with the first column of the axis
// above it: the label cell plus the gap that follows it.
const bankedAxisIndent = 8

// bankedStatusLines is the complete listing `cx status` prints — the dashboard's
// axis line, then every reset under it. The dashboard is glanceable; status is
// the full record.
func bankedStatusLines(r UsageResult, indent string) []string {
	if !r.BankedLoaded {
		return nil
	}
	if r.BankedErr != "" {
		return []string{indent + bankedUnavailable(r.BankedErr)}
	}
	// One reading of the clock for the axis and the dates under it, so a reset
	// cannot be counted on one line and missing from the next.
	now := time.Now()
	resets := bankedAvailable(r.BankedResets, now)
	if len(resets) == 0 {
		return nil
	}
	lines := []string{indent + bankedLine(resets, now, defaultBarCells, false)}
	for _, reset := range resets {
		lines = append(lines, indent+strings.Repeat(" ", bankedAxisIndent)+bankedDetailRow(reset, now))
	}
	return lines
}

func printStatus(p paths, results []UsageResult) {
	st, _ := loadState(p)
	fmt.Println(bold("cx status"))
	fmt.Println()
	for i, r := range results {
		marker := dim("○")
		if r.Account.ID == st.ActiveID {
			marker = green("●")
		}
		fmt.Printf("%s %s", marker, bold(r.Account.Name))
		if r.Account.Plan != "" {
			fmt.Printf(" · %s", r.Account.Plan)
		}
		if r.Account.Email != "" {
			fmt.Printf("  %s", dim(r.Account.Email))
		}
		fmt.Println()
		banked := func() {
			for _, line := range bankedStatusLines(r, "  ") {
				fmt.Println(line)
			}
		}
		// The banked axis closes the meter group, and its dates hang under it;
		// the note about how fresh the numbers are comes after all of them.
		meters := func() {
			for _, line := range usageLines(r) {
				fmt.Println("  " + line)
			}
			banked()
		}
		if r.Err == "" {
			meters()
			if r.PrimeErr != "" {
				fmt.Printf("  %s · %s\n", red("window start failed"), r.PrimeErr)
			} else if r.PrimeSkipped != "" {
				fmt.Printf("  %s\n", dim("window not started · "+r.PrimeSkipped))
			} else if r.Primed {
				fmt.Println(dim("  live · quota windows started just now"))
			} else {
				fmt.Println(dim("  live · updated just now"))
			}
		} else if !r.Usage.FetchedAt.IsZero() {
			meters()
			fmt.Printf("  %s cached %s ago · %s\n", yellow("stale"), shortDuration(time.Since(r.Usage.FetchedAt)), r.Err)
		} else {
			fmt.Printf("  %s %s\n", red("unavailable"), r.Err)
			banked()
		}
		if i < len(results)-1 {
			fmt.Println()
		}
	}
}

type jsonAccountStatus struct {
	Name             string        `json:"name"`
	Email            string        `json:"email,omitempty"`
	Plan             string        `json:"plan,omitempty"`
	Active           bool          `json:"active"`
	FiveHour         *WeeklyUsage  `json:"five_hour,omitempty"`
	Weekly           WeeklyUsage   `json:"weekly"`
	BankedResets     []BankedReset `json:"banked_resets"`
	BankedResetError string        `json:"banked_reset_error,omitempty"`
}

func statusJSON(p paths, results []UsageResult) any {
	st, _ := loadState(p)
	out := make([]jsonAccountStatus, 0, len(results))
	for _, r := range results {
		weekly := r.Usage
		if r.Err != "" {
			weekly.Err = r.Err
		} else if r.PrimeErr != "" {
			weekly.Err = r.PrimeErr
		}
		banked := r.BankedResets
		if banked == nil && r.BankedLoaded {
			banked = []BankedReset{}
		}
		out = append(out, jsonAccountStatus{
			Name:             r.Account.Name,
			Email:            r.Account.Email,
			Plan:             r.Account.Plan,
			Active:           r.Account.ID == st.ActiveID,
			FiveHour:         r.FiveHour,
			Weekly:           weekly,
			BankedResets:     banked,
			BankedResetError: r.BankedErr,
		})
	}
	return map[string]any{"version": Version, "accounts": out}
}
