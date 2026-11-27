package ui

// result_menu.go implements the result grid's context menu. Right-clicking a
// cell, header, row-number gutter or empty area (or pressing Shift+F10 while
// the grid has focus) offers the operations that make sense for that target.
//
// The menu deliberately works on the visible view: copying and exporting use
// the current sort and filter order, exactly like what is on screen.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// resultMenuAction is one action of the result grid context menu.
type resultMenuAction int

// The context menu actions.
const (
	resultActNone resultMenuAction = iota
	resultActCopyCell
	resultActCopyRow
	resultActCopyColumn
	resultActCopyAll
	resultActCopyColName
	resultActSortAsc
	resultActSortDesc
	resultActSortClear
	resultActFitColumn
	resultActFilterCell
	resultActClearFilter
	resultActPrevSet
	resultActNextSet
	resultActExport
	resultActClear
	resultActEditCell
)

// resultMenuTarget is what was right-clicked.
type resultMenuTarget int

// The context menu targets.
const (
	resultTargetCell resultMenuTarget = iota
	resultTargetHeader
	resultTargetGutter
	resultTargetEmpty
)

// resultMenuState is the open result context menu.
type resultMenuState struct {
	open    bool
	x, y    int // screen coordinate of the click that opened it
	kind    resultMenuTarget
	row     int // view row for cell/gutter targets
	col     int // result column for cell/header targets
	actions []resultMenuAction
	sel     int
}

// openResultMenu opens the context menu at a screen point for a target.
func (m *Model) openResultMenu(x, y int, kind resultMenuTarget, row, col int) {
	r := m.session.result
	if r.res == nil {
		return
	}
	actions := make([]resultMenuAction, 0, 12)
	switch kind {
	case resultTargetCell:
		if row < 0 || row >= len(r.viewRows()) || col < 0 || col >= len(r.res.Columns) {
			return
		}
		actions = append(actions, resultActCopyCell, resultActCopyRow, resultActCopyColumn)
		actions = append(actions, resultActSortAsc, resultActSortDesc, resultActSortClear)
		actions = append(actions, resultActFitColumn)
		// The edit is only offered for a result that can be edited at all;
		// the reason why it cannot is shown when the key is pressed.
		if m.session.result.src != nil {
			actions = append(actions, resultActEditCell)
		}
		if cell := cellAt(r.res.Rows[r.viewRows()[row]], col); cell != nil && *cell != "" {
			actions = append(actions, resultActFilterCell)
		}
		if r.filter != "" {
			actions = append(actions, resultActClearFilter)
		}
	case resultTargetHeader:
		if col < 0 || col >= len(r.res.Columns) {
			return
		}
		actions = append(actions, resultActCopyColName, resultActCopyColumn)
		actions = append(actions, resultActSortAsc, resultActSortDesc, resultActSortClear)
		actions = append(actions, resultActFitColumn)
	case resultTargetGutter:
		if row < 0 || row >= len(r.viewRows()) {
			return
		}
		actions = append(actions, resultActCopyRow, resultActCopyAll)
	case resultTargetEmpty:
		if len(r.res.Columns) > 0 {
			actions = append(actions, resultActCopyAll)
		}
	}
	if m.session.resultSetCount() > 1 {
		actions = append(actions, resultActPrevSet, resultActNextSet)
	}
	actions = append(actions, resultActExport, resultActClear)
	if len(actions) == 0 {
		return
	}
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeFind()
	m.popup.close()
	m.resultMenu = resultMenuState{
		open:    true,
		x:       x,
		y:       y,
		kind:    kind,
		row:     row,
		col:     col,
		actions: actions,
	}
}

// openResultMenuForCursor is the keyboard entry point (Shift+F10).
func (m *Model) openResultMenuForCursor() {
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return
	}
	// Like a right click, the keyboard entry point is a fresh caret
	// placement for the menu: the menu acts on the cell under the caret
	// (copy, sort, filter, edit), so a row selection left over from a shift
	// gesture is dropped instead of surviving next to a menu that ignores it.
	m.session.result.clearRowSelection()
	m.ensureResultVisible()
	rect := m.paneRects().results
	x := rect.X + m.resultsGutterWidth()
	for _, col := range m.visibleResultCols() {
		if col == r.cursorCol {
			x += r.colWidths[col] / 2
			break
		}
		x += r.colWidths[col] + 1
	}
	y := m.resultDataTopY() + (r.cursorRow - r.scrollY)
	m.openResultMenu(x, y, resultTargetCell, r.cursorRow, r.cursorCol)
}

// closeResultMenu closes the context menu.
func (m *Model) closeResultMenu() { m.resultMenu.open = false }

// resultMenuLabelKey maps an action to its localized label.
func resultMenuLabelKey(a resultMenuAction) i18n.Key {
	switch a {
	case resultActCopyCell:
		return i18n.KeyResultMenuCopyCell
	case resultActCopyRow:
		return i18n.KeyResultMenuCopyRow
	case resultActCopyColumn:
		return i18n.KeyResultMenuCopyColumn
	case resultActCopyAll:
		return i18n.KeyResultMenuCopyAll
	case resultActCopyColName:
		return i18n.KeyResultMenuCopyColName
	case resultActSortAsc:
		return i18n.KeyResultMenuSortAsc
	case resultActSortDesc:
		return i18n.KeyResultMenuSortDesc
	case resultActSortClear:
		return i18n.KeyResultMenuSortClear
	case resultActFitColumn:
		return i18n.KeyResultMenuFitColumn
	case resultActFilterCell:
		return i18n.KeyResultMenuFilterCell
	case resultActClearFilter:
		return i18n.KeyResultMenuClearFilter
	case resultActPrevSet:
		return i18n.KeyResultMenuPrevSet
	case resultActNextSet:
		return i18n.KeyResultMenuNextSet
	case resultActExport:
		return i18n.KeyResultMenuExport
	case resultActClear:
		return i18n.KeyResultMenuClearResults
	case resultActEditCell:
		return i18n.KeyResultEdit
	}
	return i18n.KeyResultMenuCopyCell
}

// ---- Geometry ----

// resultMenuInnerWidth is the content width of the context menu.
func (m Model) resultMenuInnerWidth() int {
	w := 0
	for _, a := range m.resultMenu.actions {
		w = max(w, lipgloss.Width(m.t(resultMenuLabelKey(a))))
	}
	return max(w+2, 12)
}

// resultMenuRect is the on-screen rectangle of the open context menu.
func (m Model) resultMenuRect() (Rect, bool) {
	if !m.resultMenu.open || len(m.resultMenu.actions) == 0 {
		return Rect{}, false
	}
	w := m.resultMenuInnerWidth() + 2
	h := len(m.resultMenu.actions) + 2
	x := clamp(m.resultMenu.x, 0, max(0, m.width-w))
	y := m.resultMenu.y + 1
	if y+h > m.height {
		y = m.resultMenu.y - h
	}
	y = clamp(y, 0, max(0, m.height-h))
	return Rect{X: x, Y: y, W: w, H: h}, true
}

// resultMenuItemAt reports whether a coordinate hits an item of the open
// context menu.
func (m Model) resultMenuItemAt(x, y int) (int, bool) {
	r, ok := m.resultMenuRect()
	if !ok || !r.Contains(x, y) {
		return 0, false
	}
	idx := y - r.Y - 1
	if idx < 0 || idx >= len(m.resultMenu.actions) {
		return 0, false
	}
	return idx, true
}

// ---- Interaction ----

// handleResultMenuKey handles a key press while the context menu is open and
// reports whether it consumed the key.
func (m Model) handleResultMenuKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "esc", "shift+f10":
		m.closeResultMenu()
		return m, nil, true
	case "up":
		n := len(m.resultMenu.actions)
		m.resultMenu.sel = (m.resultMenu.sel - 1 + n) % n
		return m, nil, true
	case "down":
		n := len(m.resultMenu.actions)
		m.resultMenu.sel = (m.resultMenu.sel + 1) % n
		return m, nil, true
	case "home":
		m.resultMenu.sel = 0
		return m, nil, true
	case "end":
		m.resultMenu.sel = len(m.resultMenu.actions) - 1
		return m, nil, true
	case "enter", "space":
		if m.resultMenu.sel < 0 || m.resultMenu.sel >= len(m.resultMenu.actions) {
			m.closeResultMenu()
			return m, nil, true
		}
		return m, m.runResultMenuAction(m.resultMenu.actions[m.resultMenu.sel]), true
	}
	// Printable input dismisses the menu without leaking into the editor.
	m.closeResultMenu()
	return m, nil, true
}

// runResultMenuAction performs a context menu action.
func (m *Model) runResultMenuAction(a resultMenuAction) tea.Cmd {
	target := m.resultMenu
	m.closeResultMenu()
	r := &m.session.result
	switch a {
	case resultActCopyCell:
		return m.copyResultCell(target.row, target.col)
	case resultActCopyRow:
		return m.copyResultRowAt(target.row)
	case resultActCopyColumn:
		return m.copyResultColumn(target.col)
	case resultActCopyAll:
		return m.copyResultAll()
	case resultActCopyColName:
		if target.col >= 0 && target.col < len(r.res.Columns) {
			return m.copyText(r.res.Columns[target.col].Name)
		}
	case resultActSortAsc:
		m.setResultSort(target.col, false)
	case resultActSortDesc:
		m.setResultSort(target.col, true)
	case resultActSortClear:
		r.sortCol, r.sortDesc = -1, false
		m.ensureResultVisible()
	case resultActFitColumn:
		r.resetColumnWidth(target.col)
		m.ensureResultVisible()
	case resultActFilterCell:
		m.filterResultCell(target.row, target.col)
	case resultActClearFilter:
		r.filter, r.filtering, r.filterAt = "", false, 0
		r.cursorRow = 0
		m.ensureResultVisible()
	case resultActPrevSet:
		if m.session.selectRelativeResultSet(-1) {
			m.ensureResultVisible()
		}
	case resultActNextSet:
		if m.session.selectRelativeResultSet(1) {
			m.ensureResultVisible()
		}
	case resultActExport:
		return m.openExportDialog()
	case resultActClear:
		m.clearResults()
	case resultActEditCell:
		// Put the caret on the clicked cell and edit it: the grid takes the
		// keyboard over until the edit ends.
		r.cursorRow, r.cursorCol = target.row, target.col
		m.ensureResultVisible()
		return m.startCellEdit()
	}
	return nil
}

// ---- Rendering ----

// renderResultMenu renders the context menu with a border, on top of the panes.
func (m Model) renderResultMenu() (Rect, []string, bool) {
	r, ok := m.resultMenuRect()
	if !ok || r.H < 3 || r.W < 4 {
		return Rect{}, nil, false
	}
	inner := r.W - 2
	border := m.theme.menuBorder

	out := []string{border.Render("┌" + strings.Repeat("─", inner) + "┐")}
	for i, a := range m.resultMenu.actions {
		style := m.theme.menuBox
		if i == m.resultMenu.sel {
			style = m.theme.menuItemSel
		}
		label := m.t(resultMenuLabelKey(a))
		pad := max(inner-1-lipgloss.Width(label), 1)
		line := style.Render(" " + label + strings.Repeat(" ", pad))
		out = append(out, border.Render("│")+line+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return r, out, true
}
