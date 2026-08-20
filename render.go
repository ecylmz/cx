package main

import (
	"fmt"
	"math"
	"os"
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
func bar(used float64, width int) string {
	used = clamp(used, 0, 100)
	n := int(math.Round(used / 100 * float64(width)))
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}
func resetText(epoch int64, now time.Time) string {
	if epoch <= 0 {
		return "unknown"
	}
	t := time.Unix(epoch, 0)
	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}
	if d < 24*time.Hour {
		return shortDuration(d)
	}
	if d < 7*24*time.Hour {
		return t.Local().Format("Mon 15:04")
	}
	return t.Local().Format("Jan 02 15:04")
}
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func usageLine(u WeeklyUsage) string {
	left := clamp(100-u.UsedPercent, 0, 100)
	pct := quotaColor(left, fmt.Sprintf("%5.1f%% left", left))
	return fmt.Sprintf("  weekly  %s  %s   resets %s", quotaColor(left, bar(u.UsedPercent, 22)), pct, resetText(u.ResetsAt, time.Now()))
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
			fmt.Println(usageLine(r.Usage))
			fmt.Println(dim("  live · updated just now"))
		} else if !r.Usage.FetchedAt.IsZero() {
			fmt.Println(usageLine(r.Usage))
			fmt.Printf("  %s cached %s ago · %s\n", yellow("stale"), shortDuration(time.Since(r.Usage.FetchedAt)), r.Err)
		} else {
			fmt.Printf("  %s %s\n", red("unavailable"), r.Err)
		}
		if i < len(results)-1 {
			fmt.Println()
		}
	}
}

type jsonAccountStatus struct {
	Name   string      `json:"name"`
	Email  string      `json:"email,omitempty"`
	Plan   string      `json:"plan,omitempty"`
	Active bool        `json:"active"`
	Weekly WeeklyUsage `json:"weekly"`
}

func statusJSON(p paths, accounts []Account, results []UsageResult) any {
	st, _ := loadState(p)
	out := make([]jsonAccountStatus, 0, len(results))
	for _, r := range results {
		u := r.Usage
		if r.Err != "" {
			u.Err = r.Err
		}
		out = append(out, jsonAccountStatus{Name: r.Account.Name, Email: r.Account.Email, Plan: r.Account.Plan, Active: r.Account.ID == st.ActiveID, Weekly: u})
	}
	return map[string]any{"version": version, "accounts": out}
}
