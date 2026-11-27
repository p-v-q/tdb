package ui

// mouse_geometry.go contains pointer-to-model coordinate mapping and hit
// tests shared by the mouse dispatcher and hover handling.

import "github.com/p-v-q/tdb/textbuf"

// ---- Coordinate mapping ----

// screenToBuffer maps a screen coordinate to a buffer position.
//
// The coordinate is first translated into the editor text area: pane origin,
// gutter, horizontal scroll and wide characters all count.
func (m Model) screenToBuffer(x, y int) textbuf.Pos {
	r := m.paneRects().editor
	row := m.firstRow + (y - (r.Y + 1))
	row = clamp(row, 0, m.buf.Lines()-1)

	line := m.buf.LineString(row)
	target := x - (r.X + m.gutterWidth()) + m.scrollX
	col := runeIdxFromDisplayCol(line, target)
	return textbuf.Pos{Row: row, Col: col}
}

// runeIdxFromDisplayCol maps a display column to a rune index (CJK and other
// wide characters occupy 2 columns).
func runeIdxFromDisplayCol(line string, target int) int {
	if target <= 0 {
		return 0
	}
	w := 0
	idx := 0
	for _, r := range line {
		if w >= target {
			return idx
		}
		w += runeWidth(r)
		idx++
	}
	return len([]rune(line))
}

// displayCol returns the display width of the first runeIdx runes of line.
func displayCol(line string, runeIdx int) int {
	if runeIdx <= 0 {
		return 0
	}
	w := 0
	idx := 0
	for _, r := range line {
		if idx >= runeIdx {
			break
		}
		w += runeWidth(r)
		idx++
	}
	return w
}

// objectsItemAt reports whether a screen coordinate falls on an object tree row
// and returns its index.
func (m Model) objectsItemAt(rect Rect, x, y int) (int, bool) {
	if !rect.Contains(x, y) || rect.H < 2 {
		return 0, false
	}
	row := y - rect.Y - 1 // minus the title row
	if row < 0 {
		return 0, false
	}
	idx := m.objects.scroll + row
	if idx < 0 || idx >= len(m.objectsRows()) {
		return 0, false
	}
	return idx, true
}

// popupItemAt reports whether a screen coordinate falls on a completion popup
// item and returns its index.
func (m Model) popupItemAt(x, y int) (int, bool) {
	gx, gy, gw, gh, ok := m.popupGeometry()
	if !ok {
		return 0, false
	}
	if x < gx || x >= gx+gw || y < gy || y >= gy+gh {
		return 0, false
	}
	// Popup layout: header row first, then candidates, then a hint row.
	row := y - gy - 1
	if row < 0 || row >= m.popup.rows() {
		return 0, false
	}
	idx := m.popup.scroll + row
	if idx < 0 || idx >= len(m.popup.items) {
		return 0, false
	}
	return idx, true
}

func isWordRune(r rune) bool {
	return r == '_' || r == '$' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x80
}
