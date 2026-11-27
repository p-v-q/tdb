package ui

// results_render.go renders the results pane: the rows a statement returned,
// or the error it failed with. The grid has its own caret, scrolling and copy
// behaviour, and it is shared by every database: the driver hands over plain
// columns and rows, so nothing here knows about SQLite.
//
// Layout inside the pane:
//
//	summary line            "3 rows in 2ms" / "1 rows affected" / the error
//	column names            bold
//	column types            dim (only when the pane is tall enough)
//	rows                    numbered, one cell per column

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// ---- Rendering ----

// renderResults renders the results pane: title row plus the grid.
func (m Model) renderResults(rect Rect) []string {
	if rect.H < 1 || rect.W < 1 {
		return nil
	}
	out := m.renderPaneTitle(rect, paneResults, true)
	body := m.renderResultBody(rect.W, rect.H-1)
	return append(out, body...)
}

// renderResultBody renders the summary, the header and the visible rows.
func (m Model) renderResultBody(width, height int) []string {
	if height < 1 {
		return nil
	}
	r := m.session.result
	lines := make([]string, 0, height)

	switch {
	case r.filtering || r.editing != nil:
		// The filter input and the cell editor each take over the summary
		// row while they are open.
		if r.editing != nil {
			lines = append(lines, m.resultEditRow(width))
		} else {
			lines = append(lines, m.resultFilterRow(width))
		}
		lines = append(lines, m.resultHeaderRow(width))
		if height >= 5 {
			lines = append(lines, m.resultTypeRow(width))
		}
		gutter := m.resultsGutterWidth()
		hbar := m.resultsHScrollBarShown() && height-len(lines) >= 2
		body := height - len(lines)
		if hbar {
			body--
		}
		view := r.viewRows()
		for i := 0; i < body; i++ {
			idx := r.scrollY + i
			if idx >= len(view) {
				lines = append(lines, padANSI("", width))
				continue
			}
			lines = append(lines, m.resultRow(view[idx], idx, width, gutter))
		}
		if hbar {
			lines = append(lines, m.renderResultsHScrollBar(width))
		}
	case r.empty():
		lines = append(lines, padANSI(m.theme.paneHint.Render(" "+m.t(i18n.KeyResultsEmpty)), width))
	case r.err != nil:
		lines = append(lines, padANSI(" "+m.theme.statusMsg.Render(m.t(i18n.KeyResultsError)), width))
		// The error is the server's text: a PostgreSQL DETAIL or a MySQL
		// message may carry any byte, escape sequences included. It is
		// cleaned with its line breaks intact — wrapText lays the message
		// out over them — so the terminal cannot be repainted from here and
		// the words of the server still read as the server wrote them.
		for _, l := range wrapText(sanitizeMultiline(r.err.Error()), width-2) {
			lines = append(lines, padANSI(m.theme.danger.Render(" "+l), width))
		}
	default:
		// The summary is a line of prose: it is clipped to the pane the same
		// way the header row is, so a long one (rows, filter, selection) can
		// never push the grid wider than the pane.
		summary := " " + m.theme.paneHint.Render(m.resultSetSummary(m.session))
		lines = append(lines, padANSI(truncateANSI(summary, width), width))
		lines = append(lines, m.resultHeaderRow(width))
		if height >= 5 { // the type row only when there is room for it
			lines = append(lines, m.resultTypeRow(width))
		}
		gutter := m.resultsGutterWidth()
		hbar := m.resultsHScrollBarShown() && height-len(lines) >= 2
		body := height - len(lines)
		if hbar {
			body--
		}
		view := r.viewRows()
		for i := 0; i < body; i++ {
			idx := r.scrollY + i
			if idx >= len(view) {
				lines = append(lines, padANSI("", width))
				continue
			}
			lines = append(lines, m.resultRow(view[idx], idx, width, gutter))
		}
		if hbar {
			lines = append(lines, m.renderResultsHScrollBar(width))
		}
	}

	for len(lines) < height {
		lines = append(lines, padANSI("", width))
	}
	return lines[:height]
}

// resultSetSummary prefixes a statement's summary with its position when the
// last execution produced more than one result set.
func (m Model) resultSetSummary(s *session) string {
	if s == nil {
		return ""
	}
	base := m.resultSummary(s.result)
	if s.resultSetCount() < 2 {
		return base
	}
	label := m.t(i18n.KeyResultsSet, s.resultIdx+1, s.resultSetCount())
	return label + " · " + base
}

// renderResultsHScrollBar draws the horizontal scrollbar row: a track under
// the columns, with a thumb whose width is the fraction of the grid that fits
// and whose position is how far right the view is. Clicking and dragging it
// scrolls the grid sideways, like the vertical bar scrolls rows.
func (m Model) renderResultsHScrollBar(width int) string {
	hs, ok := m.resultsHScrollMetrics()
	if !ok {
		return padANSI("", width)
	}
	line := strings.Repeat(" ", hs.x-m.paneRects().results.X)
	for i := 0; i < hs.width; i++ {
		cell := m.theme.scrollTrack.Render("░")
		if i >= hs.thumbLeft && i < hs.thumbLeft+hs.thumbWidth {
			cell = m.theme.scrollThumb.Render("█")
		}
		line += cell
	}
	return padANSI(line, width)
}

// clipCellText fits a cleaned cell value into its column. A value wider than
// the column is cut and its last column shows an ellipsis: a cell that silently
// stops at the column edge looks like a value that ends there, and the user
// would never know there is more to read (a resize or a copy reveals it).
func clipCellText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return padANSI(text, width)
	}
	if width == 1 {
		return "…"
	}
	return padANSI(truncateANSI(text, width-1)+"…", width)
}

// rowCountText says how many rows a result set holds. A single row gets the
// singular message: "%d rows" would read "1 rows" for every count query, which
// is exactly what a count query returns. took is either a time.Duration (a
// fresh result) or an already formatted duration string (a history entry).
func (m Model) rowCountText(rows int, took any) string {
	if rows == 1 {
		return m.t(i18n.KeyResultsOneRow, took)
	}
	return m.t(i18n.KeyResultsRows, rows, took)
}

// affectedCountText is rowCountText for a write statement.
func (m Model) affectedCountText(affected int64, took any) string {
	if affected == 1 {
		return m.t(i18n.KeyResultsOneRowAffected, took)
	}
	return m.t(i18n.KeyResultsAffected, affected, took)
}

// resultSummary is the line that says what happened to a result.
func (m Model) resultSummary(r resultState) string {
	took := r.res.Duration.Round(time.Millisecond)
	if took <= 0 {
		took = time.Millisecond
	}
	var s string
	if len(r.res.Columns) == 0 {
		s = m.affectedCountText(r.res.Affected, took)
	} else {
		s = m.rowCountText(len(r.viewRows()), took)
		if r.filter != "" {
			s += " · " + m.t(i18n.KeyResultsFiltered, len(r.res.Rows))
		}
	}
	if r.res.Truncated {
		s += " · " + m.t(i18n.KeyResultsTruncated, len(r.res.Rows))
	}
	if n := r.selectedRowCount(); n > 0 {
		// The selection must never be invisible: the summary says how many
		// rows a copy would take (round 7 §3).
		s += " · " + m.selectedRowsText(n)
	}
	if n := r.pendingCount(); n > 0 {
		// The pending changes are in memory only; the user must see that
		// there is something to apply.
		s += " · " + m.pendingChangesText(n)
	}
	return s
}

// selectedRowsText names the size of the selected row range, singular included.
func (m Model) selectedRowsText(n int) string {
	if n == 1 {
		return m.t(i18n.KeyResultsOneRowSelected)
	}
	return m.t(i18n.KeyResultsSelected, n)
}

// pendingChangesText names how many cell edits wait to be applied.
func (m Model) pendingChangesText(n int) string {
	if n == 1 {
		return m.t(i18n.KeyResultOnePendingChange)
	}
	return m.t(i18n.KeyResultPendingChanges, n)
}

// readOnlyBlockedText says how many write statements read-only mode stopped.
func (m Model) readOnlyBlockedText(n int) string {
	if n == 1 {
		return m.t(i18n.KeyMsgOneReadOnlyBlocked)
	}
	return m.t(i18n.KeyMsgReadOnlyBlocked, n)
}

// resultHeaderRow renders the column names.
//
// A column name is the database's (an alias may hold anything): it is cleaned
// before it is measured, exactly like the cells below it, so the header keeps
// the width the grid aligns on. A break in a name folds into a space for the
// same reason a cell's does — one line per row, words kept apart. The
// clipboard still exports the original name.
func (m Model) resultHeaderRow(width int) string {
	r := m.session.result
	gutter := m.resultsGutterWidth()
	line := m.theme.statusKey.Render(strings.Repeat(" ", gutter))
	for _, i := range m.visibleResultCols() {
		name := clipCellText(foldBreaks(r.res.Columns[i].Name), r.colWidths[i])
		mark, markStyle := m.sortMark(i), m.theme.statusKey
		if m.colHover == i || (m.colResize.active && m.colResize.col == i) {
			mark, markStyle = "│", m.theme.dividerActive
		}
		line += m.theme.statusKey.Render(name) + markStyle.Render(mark)
	}
	return padANSI(line, width)
}

// sortMark is the arrow drawn in the separator column of a sorted column.
func (m Model) sortMark(col int) string {
	r := m.session.result
	if r.sortCol != col {
		return " "
	}
	if r.sortDesc {
		return "▼"
	}
	return "▲"
}

// resultFilterRow renders the filter input while it is being typed.
//
// The stored filter is data — "filter by this cell" seeds it with the bytes
// of a cell, escape sequences included — and rowMatches keeps comparing it
// with the cells as they are stored, so it is not cleaned on the way in.
// What is drawn is the cleaned copy (see editViewText): the input owns the
// summary row, which is exactly one line the terminal must not act on.
func (m Model) resultFilterRow(width int) string {
	r := m.session.result
	text, at := editViewText(r.filter, r.filterAt)
	runes := []rune(text)
	line := m.theme.statusKey.Render(" "+m.t(i18n.KeyResultsFilter)+" ") +
		m.theme.dlgValue.Render(string(runes[:at])) +
		m.theme.cursor.Render(" ") +
		m.theme.dlgValue.Render(string(runes[at:]))
	return padANSI(truncateANSI(line, width), width)
}

// editViewText cleans the text of a one-line prompt the way it is drawn and
// maps its caret onto the cleaned copy.
//
// The prompt holds data — the cell a filter was seeded from, a pasted
// fragment — so an escape sequence in it would reach the terminal the moment
// the row is drawn. foldBreaks flattens the text onto the one line the row
// has (a break folds into a space so the words stay apart, every other
// control character drops) and the caret, which counts runes of the stored
// text, is re-counted on the copy in front of it: the position the caret
// names still points at the character the user put it on.
func editViewText(text string, at int) (string, int) {
	raw := []rune(text)
	at = clamp(at, 0, len(raw))
	clean := foldBreaks(text)
	return clean, clamp(len([]rune(foldBreaks(string(raw[:at])))), 0, len([]rune(clean)))
}

// resultEditRow renders the value being typed while a cell is edited. It
// takes over the summary row, the way the filter input does.
func (m Model) resultEditRow(width int) string {
	r := m.session.result
	e := r.editing
	if e == nil {
		return ""
	}
	text, at := editViewText(e.text, e.at)
	runes := []rune(text)
	line := m.theme.statusKey.Render(" "+m.t(i18n.KeyResultEdit)+" ") +
		m.theme.dlgValue.Render(string(runes[:at])) +
		m.theme.cursor.Render(" ") +
		m.theme.dlgValue.Render(string(runes[at:])) +
		m.theme.statusKey.Render("  "+m.t(i18n.KeyResultEditHint))
	return padANSI(truncateANSI(line, width), width)
}

// resultTypeRow renders the column types under their names. The type is the
// driver's text, so it is cleaned before it is measured as well; a break in
// it folds into a space, like the names above it.
func (m Model) resultTypeRow(width int) string {
	r := m.session.result
	gutter := m.resultsGutterWidth()
	line := m.theme.lineNum.Render(strings.Repeat(" ", gutter))
	for _, i := range m.visibleResultCols() {
		name := clipCellText(foldBreaks(r.res.Columns[i].Type), r.colWidths[i])
		line += m.theme.lineNum.Render(name) + m.theme.lineNum.Render(" ")
	}
	return padANSI(line, width)
}

// resultRow renders one data row: the row number, then one cell per column.
// index is the row of the result, display its position in the current view.
//
// Three things can highlight a row: the caret (only while the pane has the
// focus, exactly as before), the multi-row selection and the row under the
// pointer. The selection wins over the hover (round 7 §1) and gets its own
// dedicated style so a copied range is unmistakable; the caret row inside a
// selection keeps the selection's background across the whole row, with the
// active cell drawn on top of it.
func (m Model) resultRow(index, display, width, gutter int) string {
	r := m.session.result
	caret := display == r.cursorRow && m.layout.focus == paneResults
	inRange := r.selectionContains(display)
	hovered := r.hover >= 0 && display == r.hover

	// hl is the background of the whole row, if any.
	var hl lipgloss.Style
	haveHL := false
	switch {
	case inRange:
		hl, haveHL = m.theme.resultRowRangeSel, true
	case caret:
		hl, haveHL = m.theme.resultRowSel, true
	case hovered:
		hl, haveHL = m.theme.resultRowSel, true
	}

	num := strconv.Itoa(display + 1)
	numStyle := m.theme.lineNum
	if haveHL {
		numStyle = hl
	}
	if caret && !inRange {
		numStyle = m.theme.resultRowSelNum
	}
	// A row with a pending change is marked in the gutter: the change is in
	// memory only, and the user must see that at a glance.
	marker, markerStyle := " ", numStyle
	if r.rowPending(index) {
		marker, markerStyle = "*", m.theme.statusMsg
	}
	line := numStyle.Render(strings.Repeat(" ", gutter-len(num)-1)+num) + markerStyle.Render(marker)

	for _, col := range m.visibleResultCols() {
		value := cellAt(r.res.Rows[index], col)
		cell, style := "", m.theme.plain
		switch {
		case value == nil:
			// NULL is spelled out and dimmed: it is not the empty string and a
			// database client must not pretend otherwise. It carries no
			// characters to clean, so it stays exactly as it reads.
			cell, style = "NULL", m.theme.lineNum
		default:
			// A value comes from the database, escape sequences included, and
			// the terminal would execute them. Sanitize before measuring:
			// the cleaning shortens the text, so the width below — and with it
			// the truncate/pad the column alignment rests on — has to be taken
			// from the cleaned cell, or the columns drift out of line.
			// A break folds into a space rather than vanishing: the grid is
			// one line per row, but "a\nb" must still read as two words.
			// The clipboard keeps the original bytes (see results_copy.go).
			cell = foldBreaks(*value)
		}
		pending, hasPending := r.pendingAt(index, col)
		active := caret && col == r.cursorCol
		switch {
		case active:
			style = m.theme.resultRowSelCell
		case haveHL:
			style = hl
		}
		if hasPending {
			// A pending change is drawn in the cell itself: the database
			// still holds the old value, so drawing the old text would hide
			// the edit the user just made. A pending NULL keeps the marker
			// the grid uses for NULL.
			if !active {
				style = m.theme.statusMsg
			}
			if pending.value == nil {
				cell = "NULL"
			} else {
				cell = foldBreaks(*pending.value)
			}
		}
		text := clipCellText(cell, r.colWidths[col])
		if e := r.editing; e != nil && e.row == index && e.col == col {
			text = clipCellText(m.editedCellText(e), r.colWidths[col])
		}
		sep := m.theme.divider
		if haveHL {
			sep = hl
		}
		line += style.Render(text) + sep.Render("│")
	}
	if haveHL {
		if pad := width - lipgloss.Width(line); pad > 0 {
			line += hl.Render(strings.Repeat(" ", pad))
		}
		return line
	}
	return padANSI(line, width)
}

// editedCellText renders the value being typed in the cell under the caret,
// with the text cursor inside it. The text is cleaned the way the rest of
// the row is (see editViewText): the cell under the caret draws exactly one
// line, and it may still carry bytes no path cleaned before.
func (m Model) editedCellText(e *cellEdit) string {
	text, at := editViewText(e.text, e.at)
	runes := []rune(text)
	return m.theme.dlgValue.Render(string(runes[:at])) +
		m.theme.cursor.Render(" ") +
		m.theme.dlgValue.Render(string(runes[at:]))
}

// wrapText breaks a message into lines no wider than width.
func wrapText(s string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			switch {
			case line == "":
				line = w
			case lipgloss.Width(line)+1+lipgloss.Width(w) <= width:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		if line != "" || len(words) == 0 {
			out = append(out, line)
		}
	}
	return out
}
