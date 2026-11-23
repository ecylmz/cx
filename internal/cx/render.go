package cx

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"slices"
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
	case "7":
		return "27"
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

func neutralLine(s string, faint bool) string {
	if !useColor {
		return s
	}
	var plain strings.Builder
	for {
		i := strings.IndexByte(s, '\x1b')
		if i < 0 {
			plain.WriteString(s)
			if faint {
				return dim(plain.String())
			}
			return plain.String()
		}
		plain.WriteString(s[:i])
		s = s[escapeEnd(s, i):]
	}
}

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

// resetTextCompact gives the countdown shown in the dashboard.
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

func labeledUsageLine(label string, u WeeklyUsage) string {
	now := time.Now()
	left := clamp(100-u.UsedPercent, 0, 100)
	pct := quotaColor(left, fmt.Sprintf("%5.1f%% left", left))
	base := fmt.Sprintf("%s  %s  %s", padCell(label, 6), quotaBar(left, defaultBarCells), pct)
	if looksLikeUnstartedWindow(u, now) {
		if label == "5 hour" {
			return base + "   " + dim("not started · starts with Codex use")
		}
		return base + "   " + dim("not started")
	}
	return base + "   " + dim("resets "+resetText(u.ResetsAt, now))
}

func usageLines(r UsageResult) []string {
	if len(r.QuotaGroups) == 0 {
		return quotaWindowLines(r.Usage, r.FiveHour)
	}
	var lines []string
	for _, group := range r.QuotaGroups {
		lines = append(lines, bold(group.Name))
		lines = append(lines, quotaWindowLines(group.Weekly, group.FiveHour)...)
	}
	return lines
}

type quotaColumns struct {
	labelWidth, windowWidth, bankedWidth int
}

const (
	quotaColumnGap      = "  "
	quotaWindowGap      = " "
	quotaWindowMinWidth = len("5 hour")
	quotaWindowMaxWidth = 36
	bankedCountWidth    = len("99 left")
	bankedMinDateWidth  = bankedCountWidth + len(quotaColumnGap) + len("01-02 15:04")
	bankedCountdownMin  = len("   in 29d")
	bankedCountdownMax  = len("   in 29d 23h")
	bankedMaxWidth      = bankedCountWidth + len(quotaColumnGap) + len("2006-01-02 15:04") + bankedCountdownMax
)

func newQuotaColumns(cols, labelWidth int, bankedDates bool) quotaColumns {
	if cols <= 0 {
		cols = 136
	}
	gaps := len(quotaColumnGap) + 2*len(quotaWindowGap)
	bankedWidth := max(8, min(14, cols/5))
	if bankedDates && cols/3+1 >= bankedMinDateWidth {
		bankedWidth = min(bankedMaxWidth, cols/3+1)
	}
	labelWidth = max(1, min(labelWidth, cols/3, cols-bankedWidth-gaps-2*quotaWindowMinWidth))
	windowWidth := min(quotaWindowMaxWidth, max(quotaWindowMinWidth, (cols-labelWidth-bankedWidth-gaps)/2))
	return quotaColumns{labelWidth, windowWidth, bankedWidth}
}

func (c quotaColumns) quotaCells(fiveHour, weekly, banked string) string {
	cell := func(s string) string {
		s = fitCells(s, c.windowWidth)
		return s + strings.Repeat(" ", max(0, c.windowWidth-visibleCells(s)))
	}
	return cell(fiveHour) + quotaWindowGap + cell(weekly) + quotaWindowGap + fitCells(banked, c.bankedWidth)
}

func (c quotaColumns) heading(indent int, label string) []string {
	col := indent + c.labelWidth + len(quotaColumnGap)
	width := visibleCells(label)
	quota := c.quotaCells(bold("5 hour"), bold("weekly"), bold("banked"))
	if width > col-len(quotaColumnGap) {
		return []string{label, strings.Repeat(" ", col) + quota}
	}
	return []string{label + strings.Repeat(" ", col-width) + quota}
}

func isGeminiModels(group quotaGroup) bool {
	return strings.EqualFold(group.Name, "Gemini Models")
}

func hasModelDetails(r UsageResult) bool {
	return r.Account.provider() == providerAgy && slices.ContainsFunc(r.QuotaGroups, func(group quotaGroup) bool {
		return !isGeminiModels(group)
	})
}

func (c quotaColumns) rows(r UsageResult, dateLayout string, expanded bool) (string, []string) {
	groups := r.QuotaGroups
	if len(groups) == 0 {
		groups = []quotaGroup{{Weekly: r.Usage, FiveHour: r.FiveHour}}
	}
	now := time.Now()
	if dateLayout == "" {
		dateLayout = localDateLayout(localeName())
	}
	shortDateLayout := strings.NewReplacer("2006/", "", "2006-", "", "/2006", "", ".2006", "").Replace(dateLayout)
	window := func(u *WeeklyUsage) string {
		if u == nil || u.WindowMinutes <= 0 && u.FetchedAt.IsZero() {
			return dim("—")
		}
		left := clamp(100-u.UsedPercent, 0, 100)
		value := fmt.Sprintf("%.0f%%", left)
		pct := quotaColor(left, value)
		room := c.windowWidth - len(value)
		if looksLikeUnstartedWindow(*u, now) {
			if r.PrimeSkipped != "" {
				for _, status := range []string{"Starts after reset", "After reset", "Wait"} {
					if len(status) <= c.windowWidth {
						return dim(status)
					}
				}
			}
			if room >= len(" · not started") {
				return pct + dim(" · not started")
			}
			return pct
		}
		if u.ResetsAt <= 0 {
			return pct
		}
		countdown := resetTextCompact(u.ResetsAt, now)
		const percentWidth = len("100%")
		const resetMark = " ↻  "
		reset := time.Unix(u.ResetsAt, 0).Local()
		full := reset.Format(dateLayout)
		details := []string{full + " · " + countdown, full, reset.Format(shortDateLayout), countdown}
		for _, detail := range details {
			if percentWidth+visibleCells(resetMark)+visibleCells(detail) <= c.windowWidth {
				return padStyled(pct, value, percentWidth) + dim(resetMark+detail)
			}
		}
		return pct
	}

	banked := dashboardBankedSummary(r, now, c.bankedWidth, dateLayout, shortDateLayout)
	if r.Err != "" && len(r.QuotaGroups) == 0 && r.FiveHour == nil {
		status := red("Unavailable")
		if refreshing, _ := refreshingStatus(r.Err); refreshing {
			status = cyan("Loading")
		} else if !r.Usage.FetchedAt.IsZero() {
			status = yellow("Stale")
		}
		return c.quotaCells(status, window(&r.Usage), banked), nil
	}
	accountQuota := c.quotaCells(dim("—"), dim("—"), banked)
	var lines []string
	for _, group := range groups {
		accountRow := group.Name == "" || r.Account.provider() == providerAgy && isGeminiModels(group)
		if !accountRow && !expanded {
			continue
		}
		cells := c.quotaCells(window(group.FiveHour), window(&group.Weekly), banked)
		if accountRow {
			accountQuota = cells
			continue
		}
		label := padCell(fitCells(group.Name, c.labelWidth), c.labelWidth)
		lines = append(lines, strings.TrimRight(label+quotaColumnGap+cells, " "))
	}
	return accountQuota, lines
}

func dashboardBankedSummary(r UsageResult, now time.Time, width int, dateLayout, shortDateLayout string) string {
	if r.BankedErr != "" {
		return yellow("error")
	}
	if !r.BankedLoaded {
		return dim("—")
	}
	resets := bankedAvailable(r.BankedResets, now)
	count := bankedCount(resets)
	if visibleCells(count) > width {
		count = strings.Replace(count, " left", "", 1)
	}
	earliest := nextBankedExpiry(resets, now)
	if earliest.IsZero() {
		return count
	}

	room := width - bankedCountWidth - len(quotaColumnGap)
	if room < len(shortDateLayout) {
		return count
	}
	if visibleCells(count) > bankedCountWidth {
		shortCount := strings.Replace(count, " left", "", 1)
		if visibleCells(shortCount) > bankedCountWidth {
			return count
		}
		count = shortCount
	}
	if room < len(dateLayout) || room < len(dateLayout)+bankedCountdownMax && room >= len(shortDateLayout)+bankedCountdownMin {
		dateLayout = shortDateLayout
	}
	detail := earliest.Local().Format(dateLayout)
	remaining := relativeDuration(earliest.Sub(now))
	const lead = " · in "
	if len(detail)+visibleCells(lead)+len(remaining) > room {
		remaining, _, _ = strings.Cut(remaining, " ")
	}
	if len(detail)+visibleCells(lead)+len(remaining) <= room {
		detail += lead + remaining
	}
	return padCell(count, bankedCountWidth) + quotaColumnGap + dim(detail)
}

func nextBankedExpiry(resets []BankedReset, now time.Time) time.Time {
	var earliest time.Time
	for _, reset := range resets {
		if expiry, ok := bankedExpiry(reset.ExpiresAt); ok && expiry.After(now) && (earliest.IsZero() || expiry.Before(earliest)) {
			earliest = expiry
		}
	}
	return earliest
}

func quotaWindowLines(weekly WeeklyUsage, fiveHour *WeeklyUsage) []string {
	lines := make([]string, 0, 2)
	if fiveHour != nil && fiveHour.WindowMinutes > 0 {
		lines = append(lines, labeledUsageLine("5 hour", *fiveHour))
	}
	if weekly.WindowMinutes > 0 || !weekly.FetchedAt.IsZero() {
		lines = append(lines, labeledUsageLine("weekly", weekly))
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

func bankedCountdown(resets []BankedReset, now time.Time) string {
	if next := nextBankedExpiry(resets, now); !next.IsZero() {
		return "next in " + relativeDuration(next.Sub(now))
	}
	if bankedUndated(resets) < len(resets) {
		return "expired"
	}
	return ""
}

// bankedLine aligns the banked axis, count and countdown with the status meters.
func bankedLine(resets []BankedReset, now time.Time, barWidth int) string {
	styled, plain := bankedAxis(resets, now, barWidth)
	line := fmt.Sprintf("%s  %s  %s", padCell("banked", 6), padStyled(styled, plain, barWidth),
		fmt.Sprintf("%11s", bankedCount(resets)))

	if tail := bankedCountdown(resets, now); tail != "" {
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
	lines := []string{indent + bankedLine(resets, now, defaultBarCells)}
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
		if r.Account.ID == st.defaultAccountID() {
			marker = green("●")
		}
		fmt.Printf("%s %s", marker, bold(r.Account.Name))
		fmt.Printf("  %s", subscriptionLabel(r.Account.subscription()))
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
	Subscription     subscription  `json:"subscription"`
	Active           bool          `json:"active"`
	FiveHour         *WeeklyUsage  `json:"five_hour,omitempty"`
	Weekly           *WeeklyUsage  `json:"weekly,omitempty"`
	QuotaGroups      []quotaGroup  `json:"quota_groups,omitempty"`
	Error            string        `json:"error,omitempty"`
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
		account := jsonAccountStatus{
			Name:             r.Account.Name,
			Email:            r.Account.Email,
			Subscription:     r.Account.subscription(),
			Active:           r.Account.ID == st.defaultAccountID(),
			FiveHour:         r.FiveHour,
			QuotaGroups:      r.QuotaGroups,
			BankedResets:     banked,
			BankedResetError: r.BankedErr,
		}
		if r.Account.provider() == providerCodex {
			account.Weekly = &weekly
		} else {
			account.Error = r.Err
		}
		out = append(out, account)
	}
	return map[string]any{"version": Version, "accounts": out}
}
