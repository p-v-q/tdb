package ui

// results_copy.go turns the visible result view into TSV clipboard text.
// Copying always follows the current sort and filter order.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
)

// copyResultRow copies the row under the grid caret to the clipboard as TSV.
// The caret indexes the current view (after sorting and filtering), not the
// original result order.
func (m Model) copyResultRow() tea.Cmd {
	return m.copyResultRowAt(m.session.result.cursorRow)
}

// copyResultCell copies one cell. NULL is copied as the literal NULL so it
// cannot be confused with an empty string.
func (m Model) copyResultCell(displayRow, col int) tea.Cmd {
	r := m.session.result
	view := r.viewRows()
	if displayRow < 0 || displayRow >= len(view) {
		return nil
	}
	return m.copyText(cellText(cellAt(r.res.Rows[view[displayRow]], col)))
}

// rowTSV renders one row of the current view as TSV: the cells of that row,
// tab separated, NULL spelled out. It is the single row representation the
// single-row copy, the selection copy and (through copyResultAll) the whole
// result share — one spelling, one place to change it (round 7 §3).
func (r resultState) rowTSV(view []int, displayRow int) string {
	if displayRow < 0 || displayRow >= len(view) {
		return ""
	}
	row := r.res.Rows[view[displayRow]]
	cells := make([]string, 0, len(row))
	for _, cell := range row {
		cells = append(cells, cellText(cell))
	}
	return strings.Join(cells, "\t")
}

// copyResultRowAt copies the row at a view position as TSV.
func (m Model) copyResultRowAt(displayRow int) tea.Cmd {
	r := m.session.result
	view := r.viewRows()
	if displayRow < 0 || displayRow >= len(view) {
		return nil
	}
	return m.copyText(r.rowTSV(view, displayRow))
}

// copyResultSelection copies the selected rows as TSV: view order, one row per
// line, no header. The selection's job is the clipboard — export keeps
// exporting the whole view (round 7 §3).
func (m Model) copyResultSelection() tea.Cmd {
	r := m.session.result
	lo, hi, ok := r.selectionSpan()
	if !ok {
		return nil
	}
	view := r.viewRows()
	if len(view) == 0 {
		return nil
	}
	lo, hi = max(lo, 0), min(hi, len(view)-1)
	if lo > hi {
		return nil
	}
	lines := make([]string, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		lines = append(lines, r.rowTSV(view, i))
	}
	return m.copyResultRows(strings.Join(lines, "\n"), len(lines))
}

// copyResultRows copies several rows and reports the row count on success.
// The write itself stays inside copyText — the one clipboard boundary — and
// only the success line changes: the character count copyText would print says
// nothing about how many rows went to the clipboard. A failure keeps
// copyText's own message, so a failed copy still reads as a failure.
func (m Model) copyResultRows(text string, rows int) tea.Cmd {
	inner := m.copyText(text)
	tr := m.translator
	// The exact success line copyText produces, so the failure message can be
	// told apart from it without duplicating the clipboard write.
	copied := tr.T(i18n.KeyMsgCopied, len([]rune(text)))
	return func() tea.Msg {
		msg := inner()
		if sm, isStatus := msg.(statusMessageMsg); isStatus && string(sm) == copied {
			if rows == 1 {
				return statusMessageMsg(tr.T(i18n.KeyMsgOneRowCopied))
			}
			return statusMessageMsg(tr.T(i18n.KeyMsgCopiedRows, rows))
		}
		return msg
	}
}

// copyResultColumn copies every visible value of a column, one per line.
func (m Model) copyResultColumn(col int) tea.Cmd {
	r := m.session.result
	if col < 0 || col >= len(r.res.Columns) {
		return nil
	}
	view := r.viewRows()
	cells := make([]string, 0, len(view))
	for _, idx := range view {
		cells = append(cells, cellText(cellAt(r.res.Rows[idx], col)))
	}
	return m.copyText(strings.Join(cells, "\n"))
}

// cellText is the clipboard representation of one cell. NULL stays explicit
// so it cannot be mistaken for an empty string.
func cellText(cell *string) string {
	if cell == nil {
		return "NULL"
	}
	return *cell
}

// copyResultAll copies the whole visible result as TSV, with a header row.
func (m Model) copyResultAll() tea.Cmd {
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return nil
	}
	var b strings.Builder
	for i, c := range r.res.Columns {
		if i > 0 {
			b.WriteByte('\t')
		}
		b.WriteString(c.Name)
	}
	for _, idx := range r.viewRows() {
		b.WriteByte('\n')
		for i, cell := range r.res.Rows[idx] {
			if i > 0 {
				b.WriteByte('\t')
			}
			b.WriteString(cellText(cell))
		}
	}
	return m.copyText(b.String())
}
