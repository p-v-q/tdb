package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/p-v-q/tdb/textbuf"
)

// runeWidth returns the display width of a single rune (CJK/emoji count as 2
// columns, control characters as 0).
func runeWidth(r rune) int {
	if r == '\n' || r == '\t' {
		return 1
	}
	return uniseg.StringWidth(string(r))
}

// truncateANSI truncates a styled string to a display width, keeping the ANSI
// styles intact.
func truncateANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

// clipEllipsis truncates a styled string to a display width and marks the cut
// with an ellipsis. A line that silently stops at the pane edge looks like it
// ends there; the marker says there is more.
func clipEllipsis(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return truncateANSI(s, width-1) + "…"
}

// cutLeftANSI removes n display columns from the left of a styled string (used
// to keep the tail of a line when overlaying a popup).
func cutLeftANSI(s string, n int) string {
	if n <= 0 {
		return s
	}
	return ansi.TruncateLeft(s, n, "")
}

// posAfterOffset returns the position reached by skipping offset runes of text
// from start.
func posAfterOffset(start textbuf.Pos, text string, offset int) textbuf.Pos {
	runes := []rune(text)
	if offset < 0 || offset > len(runes) {
		offset = len(runes)
	}
	row, col := start.Row, start.Col
	for _, r := range runes[:offset] {
		if r == '\n' {
			row++
			col = 0
			continue
		}
		col++
	}
	return textbuf.Pos{Row: row, Col: col}
}

// clamp restricts v to the inclusive range [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
