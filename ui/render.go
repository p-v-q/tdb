package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
)

// render.go turns the Model into a single frame of text.
//
// Rendering uses a canvas model: each pane is drawn into a full-screen canvas
// at its layout rectangle, then overlays (the completion popup, dropdown menus,
// dialogs) are blitted on top. Because every draw goes through a rectangle,
// panes can never bleed into each other.

// ---- Layout metrics (relative to the editor pane rectangle) ----

// editorHeight is the number of rows available to the editor text area (pane
// height minus the title row).
func (m Model) editorHeight() int {
	h := m.paneRects().editor.H - 1
	if h < 1 {
		return 1
	}
	return h
}

// gutterWidth is the width of the line-number gutter, or 0 when it is disabled.
func (m Model) gutterWidth() int {
	if !m.settings.Gutter {
		return 0
	}
	digits := len(strconv.Itoa(m.buf.Lines()))
	if digits < 2 {
		digits = 2
	}
	return digits + 1
}

// editorWidth is the number of columns available to the editor text area.
func (m Model) editorWidth() int {
	w := m.paneRects().editor.W - m.gutterWidth()
	if w < 1 {
		return 1
	}
	return w
}

// ---- Rendering ----

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 || m.height == 0 {
		return m.t(i18n.KeyAppLoading)
	}
	r := m.paneRects()

	// Canvas: full-screen blank rows.
	canvas := make([]string, m.height)
	for i := range canvas {
		canvas[i] = strings.Repeat(" ", m.width)
	}

	// Title bar / menu bar / status bar (padded to the canvas width so every
	// row has the same length).
	blit(canvas, r.menubar, m.renderMenuBar(r.menubar))
	blit(canvas, r.toolbar, m.renderToolbar(r.toolbar))
	blit(canvas, r.tabbar, m.renderTabBar(r.tabbar))
	canvas[m.height-1] = padANSI(truncateANSI(m.renderStatus(), m.width), m.width)

	// While a divider drag is moving the panes, only their chrome is drawn:
	// laying out a large result grid or the editor on every mouse motion is
	// what made resizing feel slow. The full contents come back on release.
	if m.layout.resizing() {
		m.renderResizingPanes(canvas, r)
		return strings.Join(canvas, "\n")
	}

	// Objects pane (or its expand strip).
	if m.layout.objectsVisible {
		blit(canvas, r.objects, m.renderObjects(r.objects))
		if s, ok := m.objectsScrollMetrics(); ok {
			blit(canvas, Rect{X: s.x, Y: s.y, W: 1, H: s.height},
				m.scrollBar(s.height, s.total, s.visible, s.offset))
		}
	} else {
		blit(canvas, r.objectsExpand, m.renderExpandStrip(r.objectsExpand, true, ""))
	}

	// Editor pane (title + text area + completion popup overlay).
	blit(canvas, r.editor, m.renderEditorBlock(r.editor))

	// Results pane (or its expand strip).
	if m.layout.resultsVisible {
		blit(canvas, r.results, m.renderResults(r.results))
		// The grid scrollbar covers data rows only; the summary, header and
		// type rows keep their full width.
		blit(canvas, m.resultsScrollEdge(), m.resultsScrollBar(r.results))
	} else {
		blit(canvas, r.resultsExpand, m.renderExpandStrip(r.resultsExpand, false, m.t(i18n.KeyPaneResultsHidden)))
	}

	// Dividers last, so their clickable area is always visible.
	if m.layout.objectsVisible {
		blit(canvas, r.dividerV, m.renderDividerV(r.dividerV))
	}
	if m.layout.resultsVisible {
		blit(canvas, r.dividerH, m.renderDividerH(r.dividerH))
	}

	// Dropdown menus render above every pane.
	if m.menu.open >= 0 {
		blit(canvas, m.menuDropdownOverlayRect(), m.renderMenuDropdown(r.menubar))
	}

	// The object browser context menu sits above the panes too.
	if r, block, ok := m.renderObjectsMenu(); ok {
		blit(canvas, r, block)
	}

	// The result grid has its own context menu.
	if r, block, ok := m.renderResultMenu(); ok {
		blit(canvas, r, block)
	}

	// So has the tab bar.
	if r, block, ok := m.renderTabMenu(); ok {
		blit(canvas, r, block)
	}

	// Settings dialog renders above everything else.
	if m.dlg.open {
		if x, y, block, ok := m.renderSettingsDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The connection form too (the dialogs are never open at the same time).
	if m.connDlg.open {
		if x, y, block, ok := m.renderConnDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
		// The option list of the type field floats above the form.
		if x, y, block, ok := m.renderConnDropdown(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The result exporter sits above the panes as well.
	if m.exportDlg.open {
		if x, y, block, ok := m.renderExportDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// Query history is a list dialog like the others.
	if m.historyDlg.open {
		if x, y, block, ok := m.renderHistoryDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The execution confirmation gates writes, so it renders above everything.
	if m.confirm.open {
		if x, y, block, ok := m.renderConfirmDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The tab rename input is a modal too: it sits above the panes.
	if m.tabRename.open {
		if x, y, block, ok := m.renderTabRenameDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The file picker is drawn above every dialog it can coexist with — the
	// exporter stays open beneath it — and below the vault dialog and the
	// key-bindings overlay, matching the keyboard and mouse priority in
	// editor_keys.go and mouse.go.
	if m.picker.open {
		if x, y, block, ok := m.renderFilePicker(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The vault dialog is the topmost form below the key-bindings overlay:
	// it may hold the only way into the saved connections, so it shadows the
	// execution confirmation and every other dialog, matching the keyboard
	// priority order in editor_keys.go.
	if m.vaultDlg.open {
		if x, y, block, ok := m.renderVaultDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	// The key-bindings help is drawn last, above every other overlay, to match
	// the keyboard path where its branch comes before the other modals.
	if m.keysDlg.open {
		if x, y, block, ok := m.renderKeybindingsDialog(); ok {
			blit(canvas, Rect{X: x, Y: y, W: lipgloss.Width(block[0]), H: len(block)}, block)
		}
	}

	for len(canvas) < m.height {
		canvas = append(canvas, "")
	}
	return strings.Join(canvas, "\n")
}

// blit draws a block of rows into the canvas at the given rectangle (clipping
// anything that overflows). Every line is truncated/padded to rect.W columns.
func blit(canvas []string, rect Rect, block []string) {
	if rect.W < 1 || rect.H < 1 {
		return
	}
	for i, line := range block {
		y := rect.Y + i
		if y < 0 || y >= len(canvas) || i >= rect.H {
			continue
		}
		full := canvas[y]
		prefix := truncateANSI(full, rect.X)
		suffix := cutLeftANSI(full, rect.X+rect.W)
		canvas[y] = prefix + padANSI(truncateANSI(line, rect.W), rect.W) + suffix
	}
}

// renderEditorBlock renders the whole editor pane: title row plus text area
// (with the popup overlay).
func (m Model) renderEditorBlock(rect Rect) []string {
	if rect.H < 1 || rect.W < 1 {
		return nil
	}
	title := m.renderPaneTitle(rect, paneEditor, false)
	var body []string
	if m.welcome {
		// The welcome page replaces the text area while it is the active tab
		// (docs/ux-round7.md, §2). Its rows are no buffer, so the completion
		// popup is not drawn over them.
		body = m.renderWelcome(rect)
	} else {
		body = m.renderEditor()
		if m.popup.open {
			body = m.overlayPopup(body, rect)
		}
	}
	return append(title, body...)
}

// sessionInfo describes the active session for the right-hand side of the menu
// bar (macOS keeps its status items there): connection, file and metadata size.
//
// Connection name, default schema and file name are names from outside the
// program — a settings file, a directory listing — and the bar draws them as
// plain text, so each is stripped of control characters before it enters a
// message. The cleaning happens on the values, not on the finished line: a
// value that cleans away to nothing then drops its part instead of leaving a
// label with nothing behind it.
func (m Model) sessionInfo() string {
	parts := []string{
		m.t(i18n.KeyHeaderConn, stripControl(m.cfg.ConnName), m.cfg.Dialect.Name()),
	}
	// The database a PostgreSQL session is attached to comes next: on a
	// server with several databases "conn shop" alone does not say which one
	// unqualified names live in, and the schemas below the connection only
	// make sense inside it.
	if db := stripControl(strings.TrimSpace(m.conn.Database)); db != "" && m.cfg.Dialect.Name() == "postgres" {
		parts = append(parts, m.t(i18n.KeyHeaderDatabase, db))
	}
	// The default schema comes right after the connection: it says where
	// unqualified names resolve, and it is the first thing to survive when
	// the bar is too narrow for the whole description.
	if s := stripControl(m.defaultSchema()); s != "" {
		parts = append(parts, m.t(i18n.KeyConnSchema)+": "+s)
	}
	if f := stripControl(m.fileName); f != "" {
		parts = append(parts, m.t(i18n.KeyHeaderFile, f))
	}
	parts = append(parts, m.sessionMeta())
	return strings.Join(parts, " · ")
}

// sessionConnInfo is the shortest form: just the connection. It cleans the
// connection name for the same reason sessionInfo does — it is the last
// summary the bar falls back to, so it may never be the one that leaks.
func (m Model) sessionConnInfo() string {
	return m.t(i18n.KeyHeaderConn, stripControl(m.cfg.ConnName), m.cfg.Dialect.Name())
}

// sessionInfoShort is the compact form used when the full one does not fit:
// it keeps the default schema and drops the metadata size.
func (m Model) sessionInfoShort() string {
	parts := []string{m.sessionConnInfo()}
	if s := stripControl(m.defaultSchema()); s != "" {
		parts = append(parts, m.t(i18n.KeyConnSchema)+": "+s)
	}
	return strings.Join(parts, " · ")
}

// sessionMeta is the metadata size of the connected database.
func (m Model) sessionMeta() string {
	if m.cat != nil {
		return m.metaText(len(m.cat.Schemas), len(m.cat.AllTables()))
	}
	return m.t(i18n.KeyHeaderNoMeta)
}

// metaText says how large the loaded catalog is, with singular forms for a
// database that holds exactly one schema or one table: "1 schemas / 1 tables"
// is the kind of prose that makes a status bar read as unfinished.
func (m Model) metaText(schemas, tables int) string {
	return m.t(i18n.KeyHeaderMeta, m.schemaCountText(schemas), m.tableCountText(tables))
}

// schemaCountText is the "N schemas" half of metaText.
func (m Model) schemaCountText(n int) string {
	if n == 1 {
		return m.t(i18n.KeyHeaderSchemaOne)
	}
	return m.t(i18n.KeyHeaderSchemas, n)
}

// tableCountText is the "N tables" half of metaText.
func (m Model) tableCountText(n int) string {
	if n == 1 {
		return m.t(i18n.KeyHeaderTableOne)
	}
	return m.t(i18n.KeyHeaderTables, n)
}

// renderStatus renders the bottom status bar.
func (m Model) renderStatus() string {
	if m.find.open {
		return m.renderFindStatus()
	}

	// The right-hand side is built first because the object selection on the
	// left is truncated to the space the hints leave: a long table name must
	// not push the shortcuts off screen.
	right := " " + m.theme.statusKey.Render(m.statusHints()) + " "
	// The persistent transaction indicator sits in front of the key hints:
	// uncommitted work must be impossible to miss, and while there is none the
	// manual mode itself stays visible.
	switch {
	case m.txOpen:
		right = " " + m.theme.statusMsg.Render(m.t(i18n.KeyStatusUncommitted)) + " " + right
	case m.txManual:
		right = " " + m.theme.statusKey.Render(m.t(i18n.KeyStatusManualCommit)) + " " + right
	}

	left := ""
	if m.status.message != "" {
		left = " " + m.statusMsgStyle().Render(m.status.message) + " "
	} else if m.queryRunning {
		left = " " + m.t(i18n.KeyStatusRunning) + " "
	} else if m.colResize.active || m.colHover >= 0 {
		left = " " + m.t(i18n.KeyResultsResizeHint) + " "
	} else if m.layout.focus == paneObjects {
		// The object browser reports its filter or selection here; the hints
		// on the right are the keys that pane owns.
		left = m.objectsStatus(right)
	} else if m.layout.focus == paneResults {
		// The grid has its own keys, so it says so where the caret position
		// usually is. Result-set navigation only appears when it can do
		// something.
		left = " " + m.t(i18n.KeyResultsCaretHint)
		if m.session.resultSetCount() > 1 {
			left += "  " + m.t(i18n.KeyResultsSetHint)
		}
		left += " "
	} else if m.toolHover >= 0 && m.toolHover < len(toolButtons) {
		// Icon buttons have no names on them, so the pointer introduces them.
		b := toolButtons[m.toolHover]
		left = " " + m.t(b.labelKey)
		if s := displayShortcut(shortcutOf(b.action)); s != "" {
			left += "  " + s
		}
		left += " "
	} else {
		c := m.buf.Cursor()
		parts := []string{
			fmt.Sprintf("Ln %d, Col %d", c.Row+1, c.Col+1),
			m.t(i18n.KeyStatusContext, m.t(contextNameKey(m.lastCtx.Kind))),
		}
		if m.lastCtx.Qualifier != "" {
			parts = append(parts, m.t(i18n.KeyStatusQualifier, m.lastCtx.Qualifier))
		}
		if a, b, ok := m.selectionRange(); ok {
			parts = append(parts, m.t(i18n.KeyStatusSelection,
				b.Row-a.Row+1, len([]rune(m.selectedText()))))
		}
		if m.popup.open {
			parts = append(parts, m.t(i18n.KeyStatusCandidates, len(m.popup.items)))
		}
		left = " " + strings.Join(parts, "  │  ") + " "
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateANSI(left, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// statusHints returns the right-hand key hints for the focused pane. The
// editor keeps the general hints; the object browser and the result grid
// advertise the keys they actually handle.
func (m Model) statusHints() string {
	switch m.layout.focus {
	case paneObjects:
		if m.objects.filtering {
			return m.t(i18n.KeyStatusObjectsFilterHints)
		}
		return m.t(i18n.KeyStatusObjectsHints)
	case paneResults:
		return m.t(i18n.KeyStatusResultsHints)
	}
	return m.t(i18n.KeyStatusHints)
}

// objectsStatus is the left-hand status text while the object browser owns the
// keyboard: the filter prompt, the selected row, or just the pane name.
func (m Model) objectsStatus(right string) string {
	if m.objects.filtering {
		// The query is typed or pasted, never the program's own text: the
		// status bar draws it as plain text, so the control characters a
		// paste may carry are dropped — the same rule the pane title above
		// the tree applies to it (see objectsFilterTitle).
		return " " + m.t(i18n.KeyStatusFiltering, stripControl(m.objects.filter)) + " "
	}
	if m.objects.sel < 0 {
		return " " + m.t(i18n.KeyPaneObjects) + " "
	}
	rows := m.objectsRows()
	if m.objects.sel >= len(rows) {
		return " " + m.t(i18n.KeyPaneObjects) + " "
	}

	name := m.objectStatusName(rows[m.objects.sel])
	// Formatting with an empty name gives the width of the position prefix, so
	// the name can be shortened to what is left before the hints start.
	prefix := m.t(i18n.KeyStatusObjectPosition, m.objects.sel+1, len(rows), "")
	avail := m.width - lipgloss.Width(" "+prefix) - lipgloss.Width(right) - 1
	if avail < 1 {
		name = ""
	} else {
		name = truncateANSI(name, avail)
	}
	return " " + m.t(i18n.KeyStatusObjectPosition, m.objects.sel+1, len(rows), name) + " "
}

// objectStatusName is the display name of a tree row in the status bar.
// objectRowName covers schemas, relations, columns and resources; connections
// and resource groups need their own labels.
//
// Everything it returns is text drawn in the status bar, so it is cleaned
// there: a catalog identifier may carry control characters (PostgreSQL
// allows them inside a quoted name) and so may a connection name typed into
// the dialog. Cleaning at this one point keeps the raw name where it belongs
// — the clipboard copy of a name (see copyObjectName) still gets the bytes
// the catalog holds.
func (m Model) objectStatusName(row objectsRow) string {
	switch row.kind {
	case rowLocked:
		return m.t(i18n.KeyVaultLockedTree)
	case rowConn:
		conns := m.connectionList()
		if row.conn >= 0 && row.conn < len(conns) {
			return stripControl(conns[row.conn].Name)
		}
	case rowGroup:
		return m.resourceGroupLabel(row.group)
	}
	return stripControl(objectRowName(row))
}

// contextNameKey maps a SQL context kind to its localized label.
func contextNameKey(k sql.ContextKind) i18n.Key {
	switch k {
	case sql.CtxTable:
		return i18n.KeyCtxTable
	case sql.CtxColumn:
		return i18n.KeyCtxColumn
	case sql.CtxQualified:
		return i18n.KeyCtxQualified
	case sql.CtxSchema:
		return i18n.KeyCtxSchema
	case sql.CtxDefault:
		return i18n.KeyCtxDefault
	default:
		return i18n.KeyCtxNone
	}
}

// statusMsgStyle keeps messages readable even in a narrow status bar.
func (m Model) statusMsgStyle() lipgloss.Style { return m.theme.statusMsg }

// renderEditor renders the editor text area (gutter, syntax colors,
// caret/selection) without the title row.
func (m Model) renderEditor() []string {
	height := m.editorHeight()
	width := m.editorWidth()
	gutterW := m.gutterWidth()
	kinds := m.lineKinds()
	cursor := m.buf.Cursor()

	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		row := m.firstRow + i
		var b strings.Builder
		if gutterW > 0 {
			if row < m.buf.Lines() {
				num := strconv.Itoa(row + 1)
				gutter := strings.Repeat(" ", gutterW-len(num)-1) + num + " "
				if row == cursor.Row {
					b.WriteString(m.theme.lineNumCur.Render(gutter))
				} else {
					b.WriteString(m.theme.lineNum.Render(gutter))
				}
			} else {
				b.WriteString(strings.Repeat(" ", gutterW))
			}
		}
		if row < m.buf.Lines() {
			b.WriteString(m.renderLine(row, width, kinds[row], row == cursor.Row))
		}
		lines = append(lines, padANSI(b.String(), width))
	}
	return lines
}

// renderLine renders one line of text, including the caret and selection
// highlights.
func (m Model) renderLine(row, width int, kinds []sql.TokenKind, cursorLine bool) string {
	line := m.buf.Line(row)
	cursor := m.buf.Cursor()

	start := clamp(m.scrollX, 0, len(line))
	end := clamp(start+width, start, len(line))

	kindAt := func(i int) sql.TokenKind {
		if i >= 0 && i < len(kinds) {
			return kinds[i]
		}
		return sql.KindWhitespace
	}

	// Rune range of the selection on this line ([lo, hi); -1 means "no
	// selection here").
	lo, hi := -1, -1
	if selStart, selEnd, ok := m.selectionRange(); ok &&
		row >= selStart.Row && row <= selEnd.Row {
		lo, hi = 0, len(line)
		if row == selStart.Row {
			lo = min(selStart.Col, len(line))
		}
		if row == selEnd.Row {
			hi = min(selEnd.Col, len(line))
		}
	}
	inSel := func(i int) bool { return lo >= 0 && i >= lo && i < hi }

	// A cell's style is decided by (token kind, selected, caret).
	type cellStyle struct {
		kind sql.TokenKind
		sel  bool
		cur  bool
	}
	keyAt := func(i int) cellStyle {
		return cellStyle{
			kind: kindAt(i),
			sel:  inSel(i),
			cur:  cursorLine && cursor.Col == i,
		}
	}

	var b strings.Builder
	for i := start; i < end; {
		sk := keyAt(i)
		j := i + 1
		for j < end && keyAt(j) == sk {
			j++
		}
		style := m.theme.styleFor(sk.kind)
		switch {
		case sk.cur:
			style = m.theme.cursor
		case sk.sel:
			style = style.Reverse(true) // keep the token color, invert on top
		}
		b.WriteString(style.Render(string(line[i:j])))
		i = j
	}
	// Caret at end of line: render a reverse-video space.
	if cursorLine && cursor.Col == len(line) && cursor.Col >= start && cursor.Col < start+width {
		b.WriteString(m.theme.cursor.Render(" "))
	}
	return padANSI(b.String(), width)
}

// popupGeometry computes the on-screen position and size of the completion
// popup (header and hint rows included). It returns absolute screen coordinates
// so mouse hit testing can reuse them.
func (m Model) popupGeometry() (x, y, w, h int, ok bool) {
	if !m.popup.open || m.width == 0 {
		return 0, 0, 0, 0, false
	}
	popup := m.popup.render(m.theme, m.icons(), m.translator, m.editorWidth()-1)
	if len(popup) == 0 {
		return 0, 0, 0, 0, false
	}
	for _, l := range popup {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	h = len(popup)

	// Position relative to the editor text area, then translate to pane
	// rectangle coordinates.
	r := m.paneRects().editor
	gutterW := m.gutterWidth()
	cursor := m.buf.Cursor()
	cursorRow := cursor.Row - m.firstRow
	line := m.buf.LineString(cursor.Row)
	anchorX := r.X + gutterW + displayCol(line, cursor.Col) - m.scrollX
	anchorX = clamp(anchorX, r.X, max(r.X, r.X+r.W-w))

	anchorY := cursorRow + 1
	if anchorY+h > m.editorHeight() {
		anchorY = cursorRow - h
	}
	anchorY = clamp(anchorY, 0, max(0, m.editorHeight()-h))
	return anchorX, r.Y + 1 + anchorY, w, h, true
}

// overlayPopup draws the completion popup on top of the editor text area.
// block is the text area content (editorWidth+gutter wide) and rect is the pane
// rectangle.
func (m Model) overlayPopup(block []string, rect Rect) []string {
	popup := m.popup.render(m.theme, m.icons(), m.translator, m.editorWidth()-1)
	x, y, _, _, ok := m.popupGeometry()
	if !ok {
		return block
	}
	// Translate back into block-local coordinates.
	lx, ly := x-rect.X, y-(rect.Y+1)

	out := append([]string{}, block...)
	for i, pl := range popup {
		row := ly + i
		if row < 0 || row >= len(out) {
			continue
		}
		line := out[row]
		prefix := truncateANSI(line, lx)
		suffix := cutLeftANSI(line, lx+lipgloss.Width(pl))
		out[row] = prefix + pl + suffix
	}
	return out
}
