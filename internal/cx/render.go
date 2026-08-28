package cx

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var useColor = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	s, _ := os.Stdout.Stat()
	return s != nil && (s.Mode()&os.ModeCharDevice) != 0
}()

func ansi(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func green(s string) string  { return ansi("32", s) }
func yellow(s string) string { return ansi("33", s) }
func red(s string) string    { return ansi("31", s) }
func cyan(s string) string   { return ansi("36", s) }
func bold(s string) string   { return ansi("1", s) }
func dim(s string) string    { return ansi("2", s) }
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
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func bar(percent float64, width int) string {
	percent = clamp(percent, 0, 100)
	n := int(math.Round(percent / 100 * float64(width)))
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}
func quotaBar(left float64, width int) string {
	left = clamp(left, 0, 100)
	n := int(math.Round(left / 100 * float64(width)))
	return strings.Repeat("█", n) + dim(strings.Repeat("░", width-n))
}
func normalizeLocale(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '@'); i >= 0 {
		v = v[:i]
	}
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
	if d < 0 {
		d = 0
	}
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
	if d < 0 {
		d = 0
	}
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

func usageLine(u WeeklyUsage) string {
	return labeledUsageLine("weekly", u)
}

func fiveHourUsageLine(u WeeklyUsage) string {
	return labeledUsageLine("5 hour", u)
}

func labeledUsageLine(label string, u WeeklyUsage) string {
	left := clamp(100-u.UsedPercent, 0, 100)
	pct := quotaColor(left, fmt.Sprintf("%5.1f%% left", left))
	base := fmt.Sprintf("  %-6s  %s  %s", label, quotaBar(left, 22), pct)
	if looksLikeUnstartedWindow(u, time.Now()) {
		if label == "5 hour" {
			return base + "   " + dim("not started · starts with Codex use")
		}
		return base + "   " + dim("not started")
	}
	return base + "   " + dim("resets "+resetText(u.ResetsAt, time.Now()))
}

func usageLines(r UsageResult) []string {
	lines := make([]string, 0, 2)
	if r.FiveHour != nil && r.FiveHour.WindowMinutes > 0 {
		lines = append(lines, fiveHourUsageLine(*r.FiveHour))
	}
	if r.Usage.WindowMinutes > 0 || !r.Usage.FetchedAt.IsZero() {
		lines = append(lines, usageLine(r.Usage))
	}
	return lines
}

func bankedExpiryText(raw string, now time.Time) string {
	if strings.TrimSpace(raw) == "" {
		return "expires unknown"
	}
	t, ok := bankedExpiry(raw)
	if !ok {
		return "expires " + raw
	}
	exact := t.Local().Format(localDateLayout(localeName()))
	d := t.Sub(now)
	if d > 0 {
		return "expires " + exact + " · in " + relativeDuration(d)
	}
	return "expired " + exact + " · " + shortDuration(-d) + " ago"
}

func bankedResetLines(r UsageResult, indent string) []string {
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
		lines = append(lines, indent+"  "+label+" · "+dim(bankedExpiryText(reset.ExpiresAt, now)))
	}
	return lines
}

func printStatus(p paths, accounts []Account, results []UsageResult) {
	st, _ := loadState(p)
	fmt.Println(bold("cx status"))
	fmt.Println()
	for i, r := range results {
		marker := " "
		if r.Account.ID == st.ActiveID {
			marker = green("●")
		} else {
			marker = dim("○")
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
				fmt.Println(line)
			}
			if r.PrimeErr != "" {
				fmt.Printf("  %s · %s\n", red("window start failed"), r.PrimeErr)
			} else if r.Primed {
				fmt.Println(dim("  live · quota windows started just now"))
			} else {
				fmt.Println(dim("  live · updated just now"))
			}
		} else if !r.Usage.FetchedAt.IsZero() {
			for _, line := range usageLines(r) {
				fmt.Println(line)
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

func statusJSON(p paths, accounts []Account, results []UsageResult) any {
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
