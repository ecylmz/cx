// Command dashboardsvg draws the dashboard picture the README shows.
//
// The screenshot cannot be a real capture: it has to show several accounts with
// quotas and banked resets that nobody's account happens to hold, and it has to
// keep showing them after those windows would have reset. So it is drawn here
// from the same layout constants the TUI lays its columns out with, and the
// result is checked in as assets/cx-dashboard.svg.
//
// Nothing in it is drawn with a block glyph. A terminal has one font and square
// cells, so ██ and ░░ tile into a meter; a README is read in whatever browser
// and whatever font the reader has, and there the same glyphs came out as a
// dotted rash with gaps between the cells. Every meter, the cursor bar, the
// banked axis and the account markers are therefore rectangles, lines and
// circles, and the text that is left is pinned to its terminal column with
// textLength — so a reader missing every font we ask for gets different
// letterforms but the same columns.
//
// Regenerate with: make assets
package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// The cell grid. The width is 0.6em, the advance of every font in fontStack, so
// a reader who has one of them sees the text sit on the same grid the shapes
// are drawn on rather than being spaced out to reach it.
const (
	cellW    = 9.6
	cellH    = 19.2
	fontSize = 16.0
	baseline = 14.6 // from the top of the cell
	barH     = 15.5 // a meter is a touch shorter than its cell, so stacked bars keep a hairline between them
)

// The card, and the plate it sits on.
const (
	padX     = 20.0 // text margin inside the card
	padTop   = 14.0 // below the title bar
	padBot   = 14.0
	titlebar = 34.0
	margin   = 28.0
)

// The palette is GitHub's dark theme rather than any one terminal's: the picture
// is read on a GitHub page, next to that page's own greys.
const (
	fg     = "#c9d1d9"
	dim    = "#8b949e"
	green  = "#3fb950"
	yellow = "#d29922"
	red    = "#f85149"
	blue   = "#58a6ff"
	// track is the dimmed half of a meter. A terminal draws it as ░ — a quarter
	// of the foreground, dithered — and this is that mix as a flat tint.
	track = "#39414b"
)

const fontStack = "'JetBrains Mono','JetBrainsMono Nerd Font','SF Mono',SFMono-Regular," +
	"ui-monospace,Menlo,Consolas,'DejaVu Sans Mono','Liberation Mono',monospace"

// The columns of a dashboard line, from internal/cx: the account header cells in
// dashboardAccountHeader, and the meter cells in labeledUsageLine — a three-cell
// indent, a six-cell label, and the two spaces before the bar.
const (
	labelCol = 3
	barCol   = 11
	valueCol = 35
	tailCol  = 49
	nameCol  = 4
	planCol  = 23
	mailCol  = 34
	barCells = 22 // defaultBarCells
)

type canvas struct {
	b      strings.Builder
	maxCol int // the rightmost cell used, which sets the width of the card
}

func (c *canvas) reach(col int) {
	if col > c.maxCol {
		c.maxCol = col
	}
}

func x(col int) float64    { return padX + float64(col)*cellW }
func yTop(row int) float64 { return titlebar + padTop + float64(row)*cellH }

// text writes part of a line, from the column it starts in. Runs are placed by
// column instead of a line being one string with spaces in it: a run whose font
// is not the one this was drawn for then loses its own spacing rather than the
// alignment of everything after it. A gap of two or more spaces starts a new
// run, both because that is where the terminal's columns are and because SVG
// collapses a run of whitespace inside a text element down to one space.
func (c *canvas) text(row, col int, s, fill string, bold bool) {
	for _, part := range splitCells(s) {
		c.run(row, col+part.col, part.text, fill, bold)
	}
}

// cellRun is a piece of a line and the cell it starts in.
type cellRun struct {
	col  int
	text string
}

// splitCells cuts a line into the runs the terminal's columns separate.
func splitCells(s string) []cellRun {
	var runs []cellRun
	start := -1
	cells := []rune(s)
	for i := 0; i <= len(cells); i++ {
		// A single space belongs to the run it sits in; two end it.
		gap := i == len(cells) || (cells[i] == ' ' && (i+1 == len(cells) || cells[i+1] == ' '))
		switch {
		case gap && start >= 0:
			// A lone space at the head of a run is a column, not a character:
			// SVG drops it, and the run would then be stretched over its width.
			for start < i && cells[start] == ' ' {
				start++
			}
			if start < i {
				runs = append(runs, cellRun{start, string(cells[start:i])})
			}
			start = -1
		case !gap && start < 0:
			start = i
		}
	}
	return runs
}

// run writes one text element, pinned to its column and to the exact width its
// cells occupy, so a reader missing every font in fontStack gets different
// letterforms in the same place rather than a line that drifts out of column.
func (c *canvas) run(row, col int, s, fill string, bold bool) {
	n := len([]rune(s))
	c.reach(col + n)
	fmt.Fprintf(&c.b, "  <text x=%q y=%q textLength=%q", f(x(col)), f(yTop(row)+baseline),
		f(float64(n)*cellW))
	if fill != fg {
		fmt.Fprintf(&c.b, " fill=%q", fill)
	}
	if bold {
		c.b.WriteString(` font-weight="600"`)
	}
	fmt.Fprintf(&c.b, ">%s</text>\n", escape(s))
}

// bar is a quota meter: the filled cells and the dimmed track behind them, in
// the same proportion barCells rounds a percentage to.
func (c *canvas) bar(row, col int, percent float64) {
	n := int(clamp(percent, 0, 100)/100*barCells + 0.5)
	y := yTop(row) + (cellH-barH)/2
	if n > 0 {
		c.rect(x(col), y, float64(n)*cellW, barH, fg)
	}
	if n < barCells {
		c.rect(x(col+n), y, float64(barCells-n)*cellW, barH, track)
	}
	c.reach(col + barCells)
}

func (c *canvas) rect(px, py, w, h float64, fill string) {
	fmt.Fprintf(&c.b, "  <rect x=%q y=%q width=%q height=%q fill=%q shape-rendering=\"crispEdges\"/>\n",
		f(px), f(py), f(w), f(h), fill)
}

// pip is one banked reset on the axis, in the column bankedColumn puts it in and
// the colour bankedPipStyle gives it.
type pip struct {
	col  int
	fill string
}

// axis draws the banked line: the 30-day rule, a tick on each week boundary, and
// a dot per reset. Drawn as one rule rather than a cell of ─ each, so the axis
// cannot come out dashed on a reader whose font sets that glyph short.
func (c *canvas) axis(row, col int, ticks []int, pips []pip) {
	mid := yTop(row) + cellH/2
	fmt.Fprintf(&c.b, "  <line x1=%q y1=%q x2=%q y2=%q stroke=%q stroke-width=\"1.1\"/>\n",
		f(x(col)), f(mid), f(x(col+barCells)), f(mid), dim)
	for _, t := range ticks {
		cx := x(col+t) + cellW/2
		fmt.Fprintf(&c.b, "  <line x1=%q y1=%q x2=%q y2=%q stroke=%q stroke-width=\"1.1\"/>\n",
			f(cx), f(mid-5.2), f(cx), f(mid+5.2), dim)
	}
	for _, p := range pips {
		fmt.Fprintf(&c.b, "  <circle cx=%q cy=%q r=\"3.5\" fill=%q/>\n",
			f(x(col+p.col)+cellW/2), f(mid), p.fill)
	}
	c.reach(col + barCells)
}

// marker is the ●/○ in front of an account name: filled for the live account.
func (c *canvas) marker(row, col int, live bool) {
	cx, cy := x(col)+cellW/2, yTop(row)+cellH/2
	if live {
		fmt.Fprintf(&c.b, "  <circle cx=%q cy=%q r=\"3.6\" fill=%q/>\n", f(cx), f(cy), green)
	} else {
		fmt.Fprintf(&c.b, "  <circle cx=%q cy=%q r=\"3.1\" fill=\"none\" stroke=%q stroke-width=\"1.3\"/>\n",
			f(cx), f(cy), dim)
	}
	c.reach(col + 1)
}

// gutter is the selection cursor. The TUI repeats ▌ down the block; here it is
// one rectangle, so it reads as the single unbroken bar it is meant to be
// instead of a stack of glyphs with the line gaps showing through.
func (c *canvas) gutter(row, rows int) {
	c.rect(x(1), yTop(row)+2, cellW/2, float64(rows)*cellH-4, blue)
}

type meter struct {
	label string
	left  float64
	tail  string
}

type banked struct {
	ticks []int
	pips  []pip
	count string
	tail  string
}

type account struct {
	name, plan, email string
	live, selected    bool
	meters            []meter
	banked            *banked
}

// draw lays out one account block and returns the row the next one starts on,
// the blank line between blocks included.
func (c *canvas) draw(row int, a account) int {
	rows := 1 + len(a.meters)
	if a.banked != nil {
		rows++
	}
	if a.selected {
		c.gutter(row, rows)
	}
	c.marker(row, 2, a.live)
	name := fg
	if a.selected {
		name = blue // the cursor colour, so the selection reads even where the bar is faint
	}
	c.text(row, nameCol, a.name, name, true)
	c.text(row, planCol, a.plan, fg, false)
	c.text(row, mailCol, a.email, dim, false)
	row++
	for _, m := range a.meters {
		c.text(row, labelCol, m.label, fg, false)
		c.bar(row, barCol, m.left)
		c.text(row, valueCol, fmt.Sprintf("%5.1f%% left", m.left), quotaColor(m.left), false)
		c.text(row, tailCol, m.tail, dim, false)
		row++
	}
	if a.banked != nil {
		c.text(row, labelCol, "banked", fg, false)
		c.axis(row, barCol, a.banked.ticks, a.banked.pips)
		c.text(row, valueCol, fmt.Sprintf("%11s", a.banked.count), fg, false)
		c.text(row, tailCol, a.banked.tail, dim, false)
		row++
	}
	return row + 1
}

// quotaColor mirrors the thresholds the TUI colours a percentage with.
func quotaColor(left float64) string {
	switch {
	case left < 20:
		return red
	case left < 50:
		return yellow
	}
	return green
}

func main() {
	var c canvas

	// The scene: three accounts, one live, one selected, banked resets on two of
	// them so the axes can be seen lining up against each other. The week ticks
	// fall where bankedColumn puts 7, 14, 21 and 28 days across 22 cells.
	weeks := []int{5, 10, 15, 20}

	row := 0
	c.text(row, 1, "cx", fg, true)
	c.text(row, 5, "primary", fg, false)
	c.text(row, 14, "· updated 12s ago", dim, false)
	row += 2

	row = c.draw(row, account{
		name: "primary", plan: "plus", email: "you@example.com", live: true,
		meters: []meter{
			{"5 hour", 42.0, "resets 06.09.2026 17:50 · in 2h 10m"},
			{"weekly", 88.0, "resets 09.09.2026 13:39 · in 2d 21h"},
		},
		banked: &banked{weeks, []pip{{9, fg}, {19, fg}}, "2 left", "next in 12d 18h"},
	})
	row = c.draw(row, account{
		name: "work", plan: "pro", email: "work@example.com", selected: true,
		meters: []meter{
			{"5 hour", 100.0, "not started · starts with Codex use"},
			{"weekly", 61.0, "resets 10.09.2026 18:39 · in 4d 2h"},
		},
		banked: &banked{weeks, []pip{{2, yellow}, {6, fg}, {13, fg}}, "3 left", "next in 3d 5h"},
	})
	row = c.draw(row, account{
		name: "backup", plan: "plus", email: "backup@example.com",
		meters: []meter{
			{"5 hour", 6.0, "resets 06.09.2026 16:20 · in 40m"},
			{"weekly", 17.0, "resets 07.09.2026 12:19 · in 20h 39m"},
		},
	})
	c.text(row, 1, "↑/↓ or j/k select   enter switch   r refresh   b banked   ? keys   q quit", dim, false)
	rows := row + 1

	// Rounded to whole pixels: the card is the one edge a reader sees against the
	// page, and a half-pixel border there renders as a soft grey line.
	cardW := math.Round(padX*2 + float64(c.maxCol)*cellW)
	cardH := math.Round(titlebar + padTop + float64(rows)*cellH + padBot)
	w, h := cardW+2*margin, cardH+2*margin

	fmt.Fprintf(os.Stdout, `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%[1]s" height="%[2]s" viewBox="0 0 %[1]s %[2]s"
     role="img" aria-label="the cx dashboard: three accounts with their 5-hour, weekly and banked quotas"
     font-family="%[3]s" font-size="%[4]spx" fill="%[5]s"
     style="font-variant-ligatures:none">
<defs>
  <clipPath id="card"><rect x="0" y="0" width="%[6]s" height="%[7]s" rx="14"/></clipPath>
</defs>
<rect width="%[1]s" height="%[2]s" rx="20" fill="#1c2128"/>
<rect x="%[8]s" y="%[9]s" width="%[6]s" height="%[7]s" rx="14" fill="#000000" opacity="0.24"/>
<g transform="translate(%[10]s %[10]s)">
  <rect x="0" y="0" width="%[6]s" height="%[7]s" rx="14" fill="#0d1117" stroke="#30363d" stroke-width="1"/>
  <g clip-path="url(#card)">
  <line x1="0" y1="%[11]s" x2="%[6]s" y2="%[11]s" stroke="#30363d" stroke-width="1"/>
  <circle cx="13.5" cy="17" r="5.5" fill="#ff5a54"/>
  <circle cx="32.5" cy="17" r="5.5" fill="#e6bf29"/>
  <circle cx="51.5" cy="17" r="5.5" fill="#52c12b"/>
%[12]s  </g>
</g>
</svg>
`, f(w), f(h), fontStack, f(fontSize), fg, f(cardW), f(cardH),
		f(margin+4), f(margin+8), f(margin), f(titlebar), c.b.String())
}

// f trims a number to the shortest form that still places a shape on the grid.
func f(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
