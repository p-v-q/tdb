package ui

// results_input.go maps keyboard and pointer coordinates to result-grid
// actions and owns column-resize dragging.

import tea "github.com/charmbracelet/bubbletea"

// resultHeaderAt returns the column whose header sits under a screen coordinate.
func (m Model) resultHeaderAt(x, y int) (int, bool) {
	rect := m.paneRects().results
	if y != rect.Y+2 { // the header row
		return 0, false
	}
	start := rect.X + m.resultsGutterWidth()
	for _, col := range m.visibleResultCols() {
		w := m.session.result.colWidths[col] + 1
		if x >= start && x < start+w {
			return col, true
		}
		start += w
	}
	return 0, false
}

// resultDataTopY is the first screen row occupied by data (below the summary,
// header and, when shown, the type row).
func (m Model) resultDataTopY() int {
	used := 2 // summary and header
	if m.resultsBodyHeight() >= 5 {
		used++ // the type row
	}
	return m.paneRects().results.Y + 1 + used
}

// resultPointAt maps a screen coordinate in the results pane to a row in the
// current view and a column. Clicking the row-number gutter selects the row
// without changing the active column.
func (m Model) resultPointAt(x, y int) (int, int, bool) {
	r := m.session.result
	if r.res == nil || r.err != nil || len(r.res.Columns) == 0 {
		return 0, 0, false
	}
	rect := m.paneRects().results
	if !rect.Contains(x, y) {
		return 0, 0, false
	}
	if m.resultsHScrollBarShown() && y == m.resultsHScrollBarY() {
		// The bar row is not a data row: a click there scrolls columns, and
		// a hover must not highlight the last row as if the pointer were over
		// it.
		return 0, 0, false
	}
	top := m.resultDataTopY()
	if y < top {
		return 0, 0, false
	}
	display := r.scrollY + (y - top)
	if display < 0 || display >= len(r.viewRows()) {
		return 0, 0, false
	}

	col := r.cursorCol
	start := rect.X + m.resultsGutterWidth()
	for _, c := range m.visibleResultCols() {
		w := r.colWidths[c] + 1 // the separator column
		if x >= start && x < start+w {
			col = c
			break
		}
		start += w
	}
	return display, col, true
}

// resultColumnEdgeAt returns the column whose right boundary (the header's
// separator cell) is under the pointer. Dragging that boundary resizes the
// column; a double click restores its automatic width.
func (m Model) resultColumnEdgeAt(x, y int) (int, bool) {
	r := m.session.result
	if r.res == nil || r.err != nil || len(r.res.Columns) == 0 {
		return 0, false
	}
	rect := m.paneRects().results
	if y != rect.Y+2 { // the header row
		return 0, false
	}
	start := rect.X + m.resultsGutterWidth()
	for _, col := range m.visibleResultCols() {
		if x == start+r.colWidths[col] {
			return col, true
		}
		start += r.colWidths[col] + 1
	}
	return 0, false
}

// beginColumnResize starts a column-width drag. The width follows the pointer
// rather than the column edge, so the drag remains stable while scrolling.
func (m *Model) beginColumnResize(col, x int) {
	r := &m.session.result
	if r.res == nil || col < 0 || col >= len(r.colWidths) {
		return
	}
	m.colResize = columnResizeState{
		active: true,
		col:    col,
		startX: x,
		startW: r.colWidths[col],
	}
	m.layout.focus = paneResults
}

// resizeColumn updates the dragged column from the pointer position.
func (m *Model) resizeColumn(x int) {
	if !m.colResize.active {
		return
	}
	width := m.colResize.startW + x - m.colResize.startX
	m.session.result.setColumnWidth(m.colResize.col, width)
	m.ensureResultVisible()
}

// finishColumnResize ends a resize drag. A plain click on the boundary leaves
// the width untouched (and the preference intact).
func (m *Model) finishColumnResize() {
	m.colResize.active = false
}

// handleResultKey moves the grid caret, sorts, filters or copies the row under
// the caret. While the filter input is open it owns the keyboard.
func (m Model) handleResultKey(key string, runes []rune) (Model, tea.Cmd) {
	if m.session.result.filtering {
		m.filterKey(key, runes)
		return m, nil
	}
	rows := len(m.session.result.viewRows())
	switch key {
	case "up":
		m.moveCursor(0, -1)
	case "down":
		m.moveCursor(0, 1)
	case "left":
		m.moveCursor(-1, 0)
	case "right":
		m.moveCursor(1, 0)
	case "pgup":
		m.moveCursor(0, -m.resultsBodyHeight())
	case "pgdown":
		m.moveCursor(0, m.resultsBodyHeight())
	case "home":
		m.moveCursor(0, -rows)
	case "end":
		m.moveCursor(0, rows)
	case "ctrl+c":
		return m, m.copyResultRow()
	case "y":
		return m, m.copyResultCell(m.session.result.cursorRow, m.session.result.cursorCol)
	case shiftF10Key:
		m.openResultMenuForCursor()
	case "s":
		m.toggleSort(m.session.result.cursorCol)
	case "e":
		cmd := m.startCellEdit()
		return m, cmd
	case "/":
		m.session.result.filtering = true
		m.session.result.filterAt = len([]rune(m.session.result.filter))
	case "[", "alt+left":
		if m.session.selectRelativeResultSet(-1) {
			m.ensureResultVisible()
		}
	case "]", "alt+right":
		if m.session.selectRelativeResultSet(1) {
			m.ensureResultVisible()
		}
	}
	return m, nil
}

// cellEditKey edits the value of the cell under the caret. The grid owns the
// keyboard while the edit is open: Enter stores the value as a pending
// change, Esc cancels it.
func (m *Model) cellEditKey(key string, runes []rune) {
	r := &m.session.result
	e := r.editing
	if e == nil {
		return
	}
	text := []rune(e.text)
	switch key {
	case "esc":
		// The edit is abandoned: the cell keeps the value it had.
		r.editing = nil
	case "enter":
		m.commitCellEdit()
	case "backspace":
		if e.at > 0 {
			e.text = string(text[:e.at-1]) + string(text[e.at:])
			e.at--
		}
	case "delete":
		if e.at < len(text) {
			e.text = string(text[:e.at]) + string(text[e.at+1:])
		}
	case "left":
		e.at = max(e.at-1, 0)
	case "right":
		e.at = min(e.at+1, len(text))
	case "home":
		e.at = 0
	case "end":
		e.at = len(text)
	default:
		if len(runes) > 0 {
			// A paste arrives as one message whose runes hold the whole
			// pasted text, and it may carry escape sequences. The editor
			// draws the text as it is, so the control characters are
			// dropped: a value must not be able to repaint the terminal.
			insert := stripControl(string(runes))
			if insert == "" {
				return
			}
			e.text = string(text[:e.at]) + insert + string(text[e.at:])
			e.at += len([]rune(insert))
		}
	}
}

// filterKey edits the filter input.
func (m *Model) filterKey(key string, runes []rune) {
	r := &m.session.result
	text := []rune(r.filter)
	switch key {
	case "esc":
		r.filter, r.filtering, r.filterAt = "", false, 0
		m.ensureResultVisible()
	case "enter":
		r.filtering = false
		r.cursorRow = 0
		m.ensureResultVisible()
	case "backspace":
		if r.filterAt > 0 {
			r.filter = string(text[:r.filterAt-1]) + string(text[r.filterAt:])
			r.filterAt--
		}
	case "delete":
		if r.filterAt < len(text) {
			r.filter = string(text[:r.filterAt]) + string(text[r.filterAt+1:])
		}
	case "left":
		r.filterAt = max(r.filterAt-1, 0)
	case "right":
		r.filterAt = min(r.filterAt+1, len(text))
	case "home":
		r.filterAt = 0
	case "end":
		r.filterAt = len(text)
	default:
		if len(runes) > 0 {
			// A paste arrives as one message whose runes hold the whole
			// pasted text, and it may carry escape sequences. The filter
			// is drawn as plain text, so the control characters are
			// dropped.
			insert := stripControl(string(runes))
			if insert == "" {
				return
			}
			r.filter = string(text[:r.filterAt]) + insert + string(text[r.filterAt:])
			r.filterAt += len([]rune(insert))
		}
	}
	// Filtering changes the number of rows: keep the caret inside the view.
	r.cursorRow = clamp(r.cursorRow, 0, max(0, len(r.viewRows())-1))
	m.ensureResultVisible()
}

// setResultSort applies an explicit sort direction.
func (m *Model) setResultSort(col int, desc bool) {
	r := &m.session.result
	if r.res == nil || col < 0 || col >= len(r.res.Columns) {
		return
	}
	r.sortCol, r.sortDesc = col, desc
	m.ensureResultVisible()
}

// filterResultCell filters the grid by the selected cell's value.
func (m *Model) filterResultCell(displayRow, col int) {
	r := &m.session.result
	view := r.viewRows()
	if displayRow < 0 || displayRow >= len(view) {
		return
	}
	cell := cellAt(r.res.Rows[view[displayRow]], col)
	if cell == nil || *cell == "" {
		return
	}
	r.filter = *cell
	r.filterAt = len([]rune(r.filter))
	r.filtering = false
	r.cursorRow = 0
	m.ensureResultVisible()
}
