// Command dashboardsvg draws the dashboard picture the README shows.
//
// The preview uses sample accounts and fixed reset times. Text sits on a
// terminal cell grid, and shapes mark the active account and selected block.
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
	fg      = "#c9d1d9"
	dim     = "#8b949e"
	green   = "#3fb950"
	blue    = "#58a6ff"
	surface = "#0d1117"
)

const fontStack = "'JetBrains Mono','JetBrainsMono Nerd Font','SF Mono',SFMono-Regular," +
	"ui-monospace,Menlo,Consolas,'DejaVu Sans Mono','Liberation Mono',monospace"

// Columns in the grouped dashboard at 120 cells wide.
const (
	labelCol    = 6
	fiveHourCol = 29
	weeklyCol   = 55
	bankedCol   = 81
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

func (c *canvas) rect(px, py, w, h float64, fill string) {
	fmt.Fprintf(&c.b, "  <rect x=%q y=%q width=%q height=%q fill=%q shape-rendering=\"crispEdges\"/>\n",
		f(px), f(py), f(w), f(h), fill)
}

// marker is the ●/○ in front of an account name: filled for the live account.
func (c *canvas) marker(row, col int, live bool, outline string) {
	cx, cy := x(col)+cellW/2, yTop(row)+cellH/2
	if live {
		fmt.Fprintf(&c.b, "  <circle cx=%q cy=%q r=\"3.6\" fill=%q/>\n", f(cx), f(cy), green)
	} else {
		fmt.Fprintf(&c.b, "  <circle cx=%q cy=%q r=\"3.1\" fill=\"none\" stroke=%q stroke-width=\"1.3\"/>\n",
			f(cx), f(cy), outline)
	}
	c.reach(col + 1)
}

type account struct {
	email, fiveHour, weekly, banked string
	live, selected                  bool
}

// draw returns the first row of the next account block.
func (c *canvas) draw(row int, a account) int {
	if a.selected {
		c.rect(x(3), yTop(row)+2, cellW/2, cellH-4, blue)
	} else {
		c.marker(row, 4, a.live, dim)
	}
	local, domain, _ := strings.Cut(a.email, "@")
	color := fg
	if a.live {
		color = green
	} else if a.selected {
		color = blue
	}
	c.text(row, labelCol, local, color, true)
	c.text(row, labelCol+len(local), "@"+domain, dim, false)
	color = fg
	if !a.live {
		color = dim
	}
	c.text(row, fiveHourCol, a.fiveHour, color, false)
	c.text(row, weeklyCol, a.weekly, color, false)
	c.text(row, bankedCol, a.banked, dim, false)
	return row + 1
}

func (c *canvas) headers(row int) {
	c.text(row, fiveHourCol, "5 hour", fg, true)
	c.text(row, weeklyCol, "weekly", fg, true)
	c.text(row, bankedCol, "banked", fg, true)
}

func main() {
	var c canvas

	row := 0
	c.text(row, 1, "cx", fg, true)
	c.text(row, 5, "· updated 12s ago", dim, false)
	row += 2

	c.text(row, 1, "▾", fg, false)
	c.text(row, 4, "Antigravity", fg, true)
	c.text(row, 16, "Pro", dim, false)
	c.headers(row)
	row = c.draw(row+2, account{
		email: "work@example.com", live: true,
		fiveHour: "78%  ↻  23.11.2026 15:20", weekly: "92%  ↻  27.11.2026 10:00", banked: "—",
	})
	row++

	c.text(row, 1, "▾", fg, false)
	c.rect(x(3), yTop(row)+2, 7*cellW, cellH-4, fg)
	c.text(row, 4, "Codex", surface, true)
	c.text(row, 11, "Plus · default", dim, false)
	row = c.draw(row+2, account{
		email: "you@example.com", live: true, selected: true,
		fiveHour: "9%   ↻  23.11.2026 17:50", weekly: "88%  ↻  26.11.2026 13:39",
		banked: "2 left   25.11.2026 17:50 · in 2d",
	})
	row = c.draw(row, account{
		email:    "other@example.com",
		fiveHour: "Starts after reset", weekly: "0%   ↻  27.11.2026 09:40", banked: "0 left",
	})
	c.text(row+1, 1, "↑/↓ select   ←/→ fold/unfold   enter open/activate   r refresh   s subscription   b banked   m models   ? keys   q quit", dim, false)
	rows := row + 2

	// Rounded to whole pixels: the card is the one edge a reader sees against the
	// page, and a half-pixel border there renders as a soft grey line.
	cardW := math.Round(padX*2 + float64(c.maxCol)*cellW)
	cardH := math.Round(titlebar + padTop + float64(rows)*cellH + padBot)
	w, h := cardW+2*margin, cardH+2*margin

	fmt.Fprintf(os.Stdout, `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%[1]s" height="%[2]s" viewBox="0 0 %[1]s %[2]s"
     role="img" aria-label="cx dashboard with aligned quota columns for Antigravity and Codex"
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

func escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
