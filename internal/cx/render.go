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

func localDateLayout(locale string) string {
	locale = normalizeLocale(locale)
	switch {
	case strings.HasPrefix(locale, "en_us"):
		return "01/02/2006 3:04 PM"
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

func bankedExpiryText(raw string, now time.Time) string {
	return bankedExpiryTextWidth(raw, now, false)
}

func bankedExpiryTextWidth(raw string, now time.Time, compact bool) string {
	if strings.TrimSpace(raw) == "" {
		return "expires unknown"
	}
	t, ok := bankedExpiry(raw)
	if !ok {
		return "expires " + raw
	}
	d := t.Sub(now)
	if compact {
		if d > 0 {
			return "expires in " + relativeDuration(d)
		}
		return "expired " + shortDuration(-d) + " ago"
	}
	exact := t.Local().Format(localDateLayout(localeName()))
	if d > 0 {
		return "expires " + exact + " · in " + relativeDuration(d)
	}
	return "expired " + exact + " · " + shortDuration(-d) + " ago"
}

func bankedResetLines(r UsageResult, indent string) []string {
	return bankedResetLinesWidth(r, indent, 0)
}

func bankedResetLinesWidth(r UsageResult, indent string, cols int) []string {
	if !r.BankedLoaded {
		return nil
	}
	if r.BankedErr != "" {
		return []string{indent + yellow("banked resets unavailable") + " · " + dim(r.BankedErr)}
	}
	if len(r.BankedResets) == 0 {
		return nil
	}
	lines := []string{indent + fmt.Sprintf("banked resets  %d", len(r.BankedResets))}
	now := time.Now()
	for i, reset := range r.BankedResets {
		label := strings.TrimSpace(reset.Title)
		if label == "" {
			label = fmt.Sprintf("reset %d", i+1)
		}
		compact := cols > 0 && cols < compactBelowCols
		lines = append(lines, indent+"  "+label+" · "+dim(bankedExpiryTextWidth(reset.ExpiresAt, now, compact)))
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
		if r.Err == "" {
			for _, line := range usageLines(r) {
				fmt.Println("  " + line)
			}
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
			for _, line := range usageLines(r) {
				fmt.Println("  " + line)
			}
			fmt.Printf("  %s cached %s ago · %s\n", yellow("stale"), shortDuration(time.Since(r.Usage.FetchedAt)), r.Err)
		} else {
			fmt.Printf("  %s %s\n", red("unavailable"), r.Err)
		}
		for _, line := range bankedResetLines(r, "  ") {
			fmt.Println(line)
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
