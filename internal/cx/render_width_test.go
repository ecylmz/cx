package cx

import (
	"strings"
	"testing"
	"time"
)

func TestVisibleCellsIgnoresStyling(t *testing.T) {
	if got := visibleCells("\x1b[1mabc\x1b[22m"); got != 3 {
		t.Fatalf("visibleCells=%d want 3", got)
	}
	if got := visibleCells("█░›"); got != 3 {
		t.Fatalf("multi-byte runes counted as bytes: %d", got)
	}
}

// The dashboard runs with wrapping disabled, so an over-long line is chopped at
// the margin — sometimes mid-sequence, which bleeds the style into the rest of
// the screen. Truncation has to happen before the terminal gets there.
func TestFitCellsCutsByCellAndClosesTheStyle(t *testing.T) {
	if got := fitCells("abcdef", 0); got != "abcdef" {
		t.Fatalf("unbounded width should not truncate: %q", got)
	}
	if got := fitCells("abcdef", 6); got != "abcdef" {
		t.Fatalf("a line that fits should be untouched: %q", got)
	}
	if got := fitCells("abcdef", 4); got != "abc…" {
		t.Fatalf("fitCells=%q want %q", got, "abc…")
	}
	cut := fitCells("\x1b[31mabcdef\x1b[39m", 4)
	if visibleCells(cut) != 4 {
		t.Fatalf("styled truncation kept %d cells: %q", visibleCells(cut), cut)
	}
	if !strings.HasSuffix(cut, "\x1b[0m") {
		t.Fatalf("a cut through styling must close it: %q", cut)
	}
	if strings.Count(cut, "\x1b") != 2 {
		t.Fatalf("escape sequence was split: %q", cut)
	}
}

func TestNarrowDashboardKeepsTheRelativeResetTime(t *testing.T) {
	old := useColor
	useColor = false
	defer func() { useColor = old }()

	v := layoutTestView(t, 1, 0, termSize{cols: 76})
	v.results[0].Usage.ResetsAt = time.Now().Add(2*time.Hour + 30*time.Minute).Unix()
	frame, _ := renderDashboardFrame(v)
	if !strings.Contains(frame, "90%  ↻  in 2h") {
		t.Fatalf("narrow dashboard should keep the countdown in the weekly column:\n%s", frame)
	}
}

func TestDetectColorHonoursTheEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("TERM", "xterm")

	t.Setenv("NO_COLOR", "1")
	if detectColor() {
		t.Fatal("NO_COLOR must win")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if detectColor() {
		t.Fatal("a dumb terminal has no styling to render")
	}
	// Forcing is how a caller keeps color through a pipe, where the
	// char-device test says no.
	t.Setenv("FORCE_COLOR", "1")
	if !detectColor() {
		t.Fatal("FORCE_COLOR should override the terminal test")
	}
	t.Setenv("FORCE_COLOR", "0")
	if detectColor() {
		t.Fatal("FORCE_COLOR=0 is not a request for color")
	}
}

func TestTerminalSizeReadsTheEnvironmentOverride(t *testing.T) {
	t.Setenv("LINES", "31")
	t.Setenv("COLUMNS", "97")
	if got := terminalSize(); got.rows != 31 || got.cols != 97 {
		t.Fatalf("terminalSize=%+v", got)
	}
	t.Setenv("COLUMNS", "0")
	// A bad override falls through to stty, which has no terminal here and so
	// reports an unknown size that every caller treats as unbounded.
	if got := terminalSize(); got.rows != 0 || got.cols != 0 {
		t.Fatalf("terminalSize with a bad override=%+v", got)
	}
}
