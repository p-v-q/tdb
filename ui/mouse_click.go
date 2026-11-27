package ui

// mouse_click.go implements multi-click detection and word/line selection.

import (
	"time"

	"github.com/p-v-q/tdb/textbuf"
)

// registerClick records a click and returns how many consecutive clicks landed
// on that spot (1, 2 or 3).
func (m *Model) registerClick(x, y int) int {
	now := time.Now()
	if now.Sub(m.lastClickAt) <= multiClickWindow && m.lastClickX == x && m.lastClickY == y {
		m.clicks++
	} else {
		m.clicks = 1
	}
	m.lastClickAt, m.lastClickX, m.lastClickY = now, x, y
	if m.clicks > 3 {
		m.clicks = 1
	}
	return m.clicks
}

// selectWordAt selects the "word" at pos (alphanumerics, underscore and dot).
func (m *Model) selectWordAt(pos textbuf.Pos) {
	line := []rune(m.buf.Line(pos.Row))
	if len(line) == 0 {
		m.buf.SetCursor(pos)
		m.beginSelection()
		return
	}
	col := clamp(pos.Col, 0, len(line)-1)
	if !isWordRune(line[col]) {
		// The click landed on blank space: fall back to placing the caret.
		m.buf.SetCursor(pos)
		m.beginSelection()
		return
	}
	start, end := col, col+1
	for start > 0 && isWordRune(line[start-1]) {
		start--
	}
	for end < len(line) && isWordRune(line[end]) {
		end++
	}
	m.sel.anchor = &textbuf.Pos{Row: pos.Row, Col: start}
	m.buf.SetCursor(textbuf.Pos{Row: pos.Row, Col: end})
}

// selectLineAt selects an entire line (caret ends at the end of the line).
func (m *Model) selectLineAt(row int) {
	row = clamp(row, 0, m.buf.Lines()-1)
	m.sel.anchor = &textbuf.Pos{Row: row, Col: 0}
	m.buf.SetCursor(textbuf.Pos{Row: row, Col: len(m.buf.Line(row))})
}
