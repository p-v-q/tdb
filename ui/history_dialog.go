package ui

// history_dialog.go implements the query-history browser: a modal list of the
// statements that have run in this profile. Enter opens the selected statement
// in a fresh tab; Ctrl+Enter opens and runs it. Delete removes one entry.
//
// History is persisted next to the settings file (history.json), so it
// survives restarts without making the settings document grow without bound.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/history"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/textbuf"
)

// historyDialog is the state of the modal query-history list.
type historyDialog struct {
	open   bool
	sel    int
	scroll int
	// filter narrows the list to the entries whose SQL or connection name
	// contains the text. It is a view of the log: the entries themselves are
	// never touched, so a filtered list still opens the original statement.
	filter string
	// filtering is true while the filter input owns the keyboard.
	filtering bool
}

// recordHistory appends a completed execution to the log. Cancellations and
// drivers that cannot query are not history: they did not run a statement.
func (m *Model) recordHistory(msg queryMsg) tea.Cmd {
	if msg.err != nil && (errors.Is(msg.err, context.Canceled) ||
		errors.Is(msg.err, driver.ErrNotImplemented)) {
		return nil
	}
	if msg.apply != nil {
		// The statements of a grid apply are generated UPDATEs bound to
		// arguments: the recorded text (with its $1/$2 placeholders) would not
		// be re-runnable from the history list, so the batch is not recorded.
		// The grid itself shows what was applied.
		return nil
	}
	sqlText := strings.TrimSpace(msg.stmt)
	if sqlText == "" {
		return nil
	}
	// Store the statement verbatim: silently truncating it would make a
	// history entry impossible to re-run correctly.
	entry := history.Entry{
		At:         msg.at,
		Connection: msg.conn,
		SQL:        sqlText,
		DurationMS: msg.took.Milliseconds(),
	}
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	if msg.res != nil {
		entry.RowSet = len(msg.res.Columns) > 0 || msg.res.Rows != nil
		entry.Rows = len(msg.res.Rows)
		entry.Affected = msg.res.Affected
		entry.Truncated = msg.res.Truncated
	}
	if msg.err != nil {
		entry.Error = msg.err.Error()
	}
	m.historyEntries = append([]history.Entry{entry}, m.historyEntries...)
	limit := m.histStore.Limit
	if limit <= 0 {
		limit = history.DefaultLimit
	}
	if len(m.historyEntries) > limit {
		m.historyEntries = m.historyEntries[:limit]
	}
	return m.saveHistory()
}

// saveHistory writes a snapshot in the background. History is auxiliary: a
// failed write must not interrupt the query workflow.
func (m *Model) saveHistory() tea.Cmd {
	if m.histStore.Path == "" {
		return nil
	}
	store := m.histStore
	entries := append([]history.Entry(nil), m.historyEntries...)
	return func() tea.Msg {
		_ = store.Save(entries)
		return noopMsg{}
	}
}

// openHistoryDialog opens the list, or reports that there is nothing to show.
func (m *Model) openHistoryDialog() tea.Cmd {
	if len(m.historyEntries) == 0 {
		return m.setMessage(m.t(i18n.KeyHistoryEmpty))
	}
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeSettings()
	m.closeConnDialog()
	m.closeExportDialog()
	m.closeFind()
	m.popup.close()
	m.historyDlg = historyDialog{open: true, sel: 0, scroll: 0}
	return nil
}

// closeHistoryDialog dismisses the list.
func (m *Model) closeHistoryDialog() { m.historyDlg.open = false }

// historyFilterQuery is the active filter, lowercased once so the matching
// can compare case-insensitively. It is empty when no filter is set.
func (m Model) historyFilterQuery() string {
	if m.historyDlg.filter == "" {
		return ""
	}
	return strings.ToLower(m.historyDlg.filter)
}

// historyEntryMatches reports whether an entry answers to the filter: the
// statement text or the connection it ran on, case-insensitively.
func historyEntryMatches(e history.Entry, query string) bool {
	if strings.Contains(strings.ToLower(e.SQL), query) {
		return true
	}
	return strings.Contains(strings.ToLower(e.Connection), query)
}

// historyVisible returns the indexes of the entries the list shows, in list
// order. Without a filter that is every entry.
func (m Model) historyVisible() []int {
	query := m.historyFilterQuery()
	if query == "" {
		out := make([]int, len(m.historyEntries))
		for i := range m.historyEntries {
			out[i] = i
		}
		return out
	}
	var out []int
	for i, e := range m.historyEntries {
		if historyEntryMatches(e, query) {
			out = append(out, i)
		}
	}
	return out
}

// historySelected returns the entry the selection points at. The selection
// indexes the visible list, so a filter can hide entries without breaking it.
func (m Model) historySelected() (history.Entry, bool) {
	visible := m.historyVisible()
	if m.historyDlg.sel < 0 || m.historyDlg.sel >= len(visible) {
		return history.Entry{}, false
	}
	return m.historyEntries[visible[m.historyDlg.sel]], true
}

// clampHistorySelection keeps the selection inside the visible list: a filter
// can hide the entry that was selected.
func (m *Model) clampHistorySelection() {
	visible := m.historyVisible()
	if len(visible) == 0 {
		m.historyDlg.sel = 0
		m.historyDlg.scroll = 0
		return
	}
	l := m.historyLayout()
	m.historyDlg.sel = clamp(m.historyDlg.sel, 0, len(visible)-1)
	if m.historyDlg.sel < m.historyDlg.scroll {
		m.historyDlg.scroll = m.historyDlg.sel
	}
	if m.historyDlg.sel >= m.historyDlg.scroll+l.listH {
		m.historyDlg.scroll = m.historyDlg.sel - l.listH + 1
	}
	m.historyDlg.scroll = clamp(m.historyDlg.scroll, 0, max(0, len(visible)-l.listH))
}

// moveHistorySelection moves by delta rows and keeps the selection visible.
func (m *Model) moveHistorySelection(delta int) {
	visible := m.historyVisible()
	if len(visible) == 0 {
		return
	}
	l := m.historyLayout()
	m.historyDlg.sel = clamp(m.historyDlg.sel+delta, 0, len(visible)-1)
	if m.historyDlg.sel < m.historyDlg.scroll {
		m.historyDlg.scroll = m.historyDlg.sel
	}
	if m.historyDlg.sel >= m.historyDlg.scroll+l.listH {
		m.historyDlg.scroll = m.historyDlg.sel - l.listH + 1
	}
	m.historyDlg.scroll = clamp(m.historyDlg.scroll, 0, max(0, len(visible)-l.listH))
}

// handleHistoryKey handles the modal list's keys.
func (m Model) handleHistoryKey(key string, runes []rune) (Model, tea.Cmd, bool) {
	// The filter input owns the keyboard while it is open.
	if m.historyDlg.filtering {
		m.historyFilterKey(key, runes)
		return m, nil, true
	}
	switch key {
	case "esc":
		// A filter that is still in effect is cleared first: the hint says
		// Esc clears, and the list is more useful than the dialog.
		if m.historyDlg.filter != "" {
			m.historyDlg.filter = ""
			m.clampHistorySelection()
			return m, nil, true
		}
		m.closeHistoryDialog()
		return m, nil, true
	case "/":
		// The filter input takes over the keyboard. The text stays, so it
		// can be refined instead of retyped.
		m.historyDlg.filtering = true
		return m, nil, true
	case "up":
		m.moveHistorySelection(-1)
		return m, nil, true
	case "down":
		m.moveHistorySelection(1)
		return m, nil, true
	case "pgup":
		m.moveHistorySelection(-max(1, m.historyLayout().listH))
		return m, nil, true
	case "pgdown":
		m.moveHistorySelection(max(1, m.historyLayout().listH))
		return m, nil, true
	case "home":
		m.historyDlg.sel = 0
		m.clampHistorySelection()
		return m, nil, true
	case "end":
		m.historyDlg.sel = max(0, len(m.historyVisible())-1)
		m.clampHistorySelection()
		return m, nil, true
	case "enter":
		entry, ok := m.historySelected()
		if !ok {
			return m, nil, true
		}
		return m, m.openHistoryEntry(entry, false), true
	case "ctrl+enter", "ctrl+j":
		entry, ok := m.historySelected()
		if !ok {
			return m, nil, true
		}
		return m, m.openHistoryEntry(entry, true), true
	case "delete":
		return m, m.deleteHistoryEntry(), true
	}
	// Swallow every other key: a modal list must not let typing leak into the
	// editor behind it.
	return m, nil, true
}

// historyFilterKey edits the filter while its input owns the keyboard. The
// arrow keys keep moving the selection, like in the object tree: the filter
// is a view of the list, not a mode of its own.
func (m *Model) historyFilterKey(key string, runes []rune) {
	switch key {
	case "esc":
		// Esc clears the filter and leaves the input.
		m.historyDlg.filter, m.historyDlg.filtering = "", false
	case "enter":
		// Enter leaves the input but keeps the filter in effect.
		m.historyDlg.filtering = false
	case "up":
		m.moveHistorySelection(-1)
		return
	case "down":
		m.moveHistorySelection(1)
		return
	case "pgup":
		m.moveHistorySelection(-max(1, m.historyLayout().listH))
		return
	case "pgdown":
		m.moveHistorySelection(max(1, m.historyLayout().listH))
		return
	case "home":
		m.historyDlg.sel = 0
		m.clampHistorySelection()
		return
	case "end":
		m.historyDlg.sel = max(0, len(m.historyVisible())-1)
		m.clampHistorySelection()
		return
	case "backspace":
		if text := []rune(m.historyDlg.filter); len(text) > 0 {
			m.historyDlg.filter = string(text[:len(text)-1])
		}
	case "ctrl+u":
		m.historyDlg.filter = ""
	case "/":
		// The key that opens the input is a mode switch, never filter text.
		return
	default:
		// Only keys whose key name IS the text they carry are filter text:
		// modified keys (Ctrl+A) carry runes as well, and those belong to
		// the shortcut layer that this modal input shields.
		if len(runes) == 0 || (key != string(runes) && key != "space") {
			return
		}
		m.historyDlg.filter += string(runes)
	}
	m.clampHistorySelection()
}

// openHistoryEntry opens an entry in a new tab. Running re-executes the exact
// stored text, not just the statement under the caret, so a saved script is
// reproduced faithfully.
//
// The tab is named after the statement's first line, so the tab bar says what
// the tab holds instead of showing the time it was opened.
func (m *Model) openHistoryEntry(entry history.Entry, run bool) tea.Cmd {
	m.closeHistoryDialog()
	m.newTabNamed(tabNameFromSQL(entry.SQL), entry.SQL)
	m.buf.SetCursor(textbuf.Pos{})
	m.layout.focus = paneEditor
	m.updateContext()
	if !run {
		return nil
	}
	return m.executeText(entry.SQL, sql.Statements(entry.SQL, m.cfg.Dialect))
}

// deleteHistoryEntry removes the selected row and persists the shorter list.
func (m *Model) deleteHistoryEntry() tea.Cmd {
	visible := m.historyVisible()
	if m.historyDlg.sel < 0 || m.historyDlg.sel >= len(visible) {
		return nil
	}
	i := visible[m.historyDlg.sel]
	m.historyEntries = append(m.historyEntries[:i], m.historyEntries[i+1:]...)
	if len(m.historyEntries) == 0 {
		m.closeHistoryDialog()
		return tea.Batch(m.setMessage(m.t(i18n.KeyHistoryDeleted)), m.saveHistory())
	}
	m.clampHistorySelection()
	return tea.Batch(m.setMessage(m.t(i18n.KeyHistoryDeleted)), m.saveHistory())
}

// ---- Geometry ----

type historyLayout struct {
	x, y, w, h int
	innerW     int
	timeW      int
	statusW    int
	connW      int
	sqlW       int
	listY      int
	listH      int
}

func (m Model) historyLayout() historyLayout {
	area := m.paneRects().area
	w := min(84, max(4, m.width-4))
	h := min(20, max(10, area.H-2))
	innerW := w - 2
	timeW := 8
	statusW := 1
	connW := clamp(innerW/5, 8, 20)
	if connW+16 > innerW {
		connW = max(2, innerW-16)
	}
	sqlW := max(1, innerW-(1+timeW+1+statusW+1+connW+2))
	l := historyLayout{
		x: area.X + max(0, (area.W-w)/2),
		y: area.Y + max(0, (area.H-h)/2),
		w: w, h: h, innerW: innerW,
		timeW: timeW, statusW: statusW, connW: connW, sqlW: sqlW,
		listH: max(1, h-9),
	}
	l.listY = l.y + 3
	return l
}

// historyRowAt returns the index of the visible row under a screen
// coordinate.
func (m Model) historyRowAt(x, y int) (int, bool) {
	l := m.historyLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(x, y) {
		return 0, false
	}
	row := y - l.listY
	if row < 0 || row >= l.listH {
		return 0, false
	}
	visible := m.historyVisible()
	idx := m.historyDlg.scroll + row
	if idx < 0 || idx >= len(visible) {
		return 0, false
	}
	return idx, true
}

// ---- Rendering ----

func (m Model) renderHistoryDialog() (x, y int, block []string, ok bool) {
	if !m.historyDlg.open {
		return 0, 0, nil, false
	}
	l := m.historyLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)
	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))

	visible := m.historyVisible()
	title := fmt.Sprintf("%s (%d)", m.t(i18n.KeyHistoryTitle), len(visible))
	out = append(out, border.Render("│")+
		m.theme.dlgTitle.Render(padANSI(truncateANSI(" "+title, l.innerW), l.innerW))+
		border.Render("│"))

	header := " " + padANSI(m.t(i18n.KeyHistoryTime), l.timeW) +
		" " + strings.Repeat(" ", l.statusW) +
		" " + padANSI(m.t(i18n.KeyHistoryConnection), l.connW) +
		"  " + m.t(i18n.KeyHistorySQL)
	out = append(out, border.Render("│")+
		m.theme.dlgLabel.Render(padANSI(truncateANSI(header, l.innerW), l.innerW))+
		border.Render("│"))

	for row := 0; row < l.listH; row++ {
		idx := m.historyDlg.scroll + row
		line := ""
		switch {
		case idx >= 0 && idx < len(visible):
			line = m.renderHistoryRow(visible[idx], idx, l)
		case len(visible) == 0 && row == 0:
			// The filter hides every entry: say so instead of showing an
			// empty box the user cannot read.
			line = m.theme.paneHint.Render(" " + m.t(i18n.KeyHistoryNoMatch))
		}
		out = append(out, border.Render("│")+padANSI(truncateANSI(line, l.innerW), l.innerW)+border.Render("│"))
	}

	out = append(out, border.Render("│")+box.Render(strings.Repeat(" ", l.innerW))+border.Render("│"))

	entry, _ := m.historySelected()
	meta := historyMetaLine(m, entry)
	out = append(out, border.Render("│")+
		m.theme.dlgDesc.Render(padANSI(truncateANSI(" "+meta, l.innerW), l.innerW))+
		border.Render("│"))

	// historyPreviewLines hands back cleaned lines: the preview is drawn,
	// while the log keeps the statement byte for byte (opening the entry
	// re-runs the original, see openHistoryEntry).
	sqlLines := historyPreviewLines(entry.SQL, 2)
	sqlStyle := m.theme.dlgValue
	if entry.Error != "" {
		sqlStyle = m.theme.danger
	}
	first := ""
	if len(sqlLines) > 0 {
		first = sqlLines[0]
	}
	out = append(out, border.Render("│")+
		sqlStyle.Render(padANSI(truncateANSI(" "+first, l.innerW), l.innerW))+
		border.Render("│"))
	second := ""
	if len(sqlLines) > 1 {
		second = sqlLines[1]
	}
	if entry.Error != "" {
		second = oneLine(entry.Error)
	}
	style := m.theme.dlgDesc
	if entry.Error != "" {
		style = m.theme.danger
	}
	out = append(out, border.Render("│")+
		style.Render(padANSI(truncateANSI(" "+second, l.innerW), l.innerW))+
		border.Render("│"))

	hint := padANSI(truncateANSI(m.historyHint(), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgBox.Render(hint)+border.Render("│"))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// historyHint is the line at the bottom of the dialog: the usual keys, or the
// filter keys while a filter is in effect. The filter text is shown as well,
// so the user knows what the list is narrowed down to.
//
// The filter is cleaned here, at the only place it is drawn: the field itself
// stays byte-for-byte (it is matched against the log raw, so a filter finds
// an entry that holds the same bytes), and the caller measures this same
// cleaned string for the row, so the width and the drawn text cannot drift.
func (m Model) historyHint() string {
	if !m.historyDlg.filtering && m.historyDlg.filter == "" {
		return m.t(i18n.KeyHistoryHint)
	}
	hint := m.t(i18n.KeyHistoryFilterHint)
	if m.historyDlg.filter != "" {
		hint = "/" + stripControl(m.historyDlg.filter) + "  " + hint
	}
	return hint
}

// renderHistoryRow renders one list row without the dialog borders. entry is
// the index of the entry in the log, visible its position in the shown list.
func (m Model) renderHistoryRow(entry, visible int, l historyLayout) string {
	e := m.historyEntries[entry]
	status := "✓"
	if e.Error != "" {
		status = "✕"
	}
	// The stored connection name is text from the settings file or the
	// history JSON, so it is drawn like every other name in the program
	// (render.go's header): cleaned first, measured second. Truncating the
	// raw name would measure its escape sequences as zero-width text and
	// still write them into the frame, where the terminal executes them.
	conn := stripControl(e.Connection)
	if conn == "" {
		conn = "—"
	}
	conn = truncateANSI(conn, l.connW)
	text := " " + padANSI(e.At.Local().Format("15:04:05"), l.timeW) +
		" " + padANSI(status, l.statusW) +
		" " + padANSI(conn, l.connW) +
		"  " + truncateANSI(oneLine(e.SQL), l.sqlW)
	text = padANSI(truncateANSI(text, l.innerW), l.innerW)
	if visible == m.historyDlg.sel {
		return m.theme.dlgRowSel.Render(text)
	}
	if e.Error != "" {
		return m.theme.danger.Render(text)
	}
	return m.theme.dlgValue.Render(text)
}

// historyMetaLine summarizes one entry for the preview pane.
func historyMetaLine(m Model, e history.Entry) string {
	if e.At.IsZero() {
		return ""
	}
	out := e.At.Local().Format("2006-01-02 15:04:05")
	if e.Connection != "" {
		// Same cleaning as the connection column of the list row: this
		// line is drawn too, and a name carrying escape bytes must be
		// read here rather than executed by the terminal.
		out += " · " + stripControl(e.Connection)
	}
	if e.Error != "" {
		out += " · " + m.t(i18n.KeyResultsError)
		return out
	}
	dur := historyDuration(e.DurationMS)
	if e.RowSet || e.Rows > 0 {
		out += " · " + m.rowCountText(e.Rows, dur)
	} else if e.Affected > 0 {
		out += " · " + m.affectedCountText(e.Affected, dur)
	}
	if e.Truncated {
		out += " · " + m.t(i18n.KeyResultsTruncated, e.Rows)
	}
	return out
}

// historyDuration formats a stored millisecond count. A zero is shown as
// "<1ms" because a query that ran did take some time, however little the clock
// resolution allowed us to record.
func historyDuration(ms int64) string {
	if ms <= 0 {
		return "<1ms"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

// historyPreviewLines returns up to n non-empty lines from the stored SQL,
// keeping the first indentation but flattening the rest of the preview.
//
// The lines are what the preview pane draws, so they are cleaned here, once,
// for both rows that show them: foldBreaks — the rule the result grid draws
// its cells by — folds the breaks and tabs a statement may hold into spaces
// and drops every remaining control byte, so no escape sequence travels from
// the log into the frame. The stored statement itself is never rewritten:
// opening an entry hands the original bytes to the editor.
func historyPreviewLines(text string, n int) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = foldBreaks(strings.TrimRight(line, " \t\r"))
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= n {
			break
		}
	}
	return out
}

// historyDialogRect is the clickable outer rectangle.
func (m Model) historyDialogRect() Rect {
	l := m.historyLayout()
	return Rect{X: l.x, Y: l.y, W: l.w, H: l.h}
}

// handleHistoryMouse selects rows, opens on a double click and closes when the
// click lands outside the dialog. The wheel moves the selection, which keeps
// the keyboard and mouse models identical.
func (m Model) handleHistoryMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if tea.MouseEvent(msg).IsWheel() {
		l := m.historyLayout()
		if (Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(msg.X, msg.Y) {
			delta := 3
			if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelLeft {
				delta = -3
			}
			m.moveHistorySelection(delta)
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if !m.historyDialogRect().Contains(msg.X, msg.Y) {
		m.closeHistoryDialog()
		return m, nil
	}
	idx, ok := m.historyRowAt(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	m.historyDlg.sel = idx
	clicks := m.registerClick(msg.X, msg.Y)
	if clicks >= 2 {
		m.clicks = 0
		entry, ok := m.historySelected()
		if !ok {
			return m, nil
		}
		return m, m.openHistoryEntry(entry, false)
	}
	return m, nil
}
