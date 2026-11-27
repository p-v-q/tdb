package ui

// export_dialog.go implements the modal result exporter. The grid is exported
// in the order the user currently sees it (after sorting and filtering), with
// the column names as the first row. CSV is the default; TSV, JSON and
// Markdown live in the format dropdown (←→ keeps cycling as a fallback).
//
//   - ↑↓/Tab move between the path field, the format select, the directory
//     row and the Browse button; the format list opens with Enter/Space and
//     picks with Enter.
//   - Browse… opens the directory picker on the folder of the current path;
//     the exporter stays open beneath the picker and takes the chosen folder
//     while keeping the file name.
//   - A preview of the chosen format sits under the browse row.
//   - The path field scrolls horizontally to keep the caret visible; the
//     dialog keeps the shared modal geometry (see modal.go).
//   - The dialog is modal: it swallows every other key, and the open dropdown
//     swallows everything as long as it is open.

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// exportFormat is the file format written by the exporter.
type exportFormat int

const (
	exportCSV exportFormat = iota
	exportTSV
	exportJSON
	exportMarkdown

	exportFormatCount // number of formats, for cycling
)

// next returns the following format in a fixed order, wrapping around.
func (f exportFormat) next() exportFormat {
	return (f + 1) % exportFormatCount
}

// extension returns the conventional suffix of a format.
func (f exportFormat) extension() string {
	switch f {
	case exportTSV:
		return ".tsv"
	case exportJSON:
		return ".json"
	case exportMarkdown:
		return ".md"
	}
	return ".csv"
}

// labelKey returns the localized name of a format.
func (f exportFormat) labelKey() i18n.Key {
	switch f {
	case exportTSV:
		return i18n.KeyExportTSV
	case exportJSON:
		return i18n.KeyExportJSON
	case exportMarkdown:
		return i18n.KeyExportMarkdown
	}
	return i18n.KeyExportCSV
}

// id returns the stable identifier the format select hands back on pick. The
// labelKey changes with the language; the id never does.
func (f exportFormat) id() string {
	switch f {
	case exportTSV:
		return "tsv"
	case exportJSON:
		return "json"
	case exportMarkdown:
		return "markdown"
	}
	return "csv"
}

// formatByID maps an id of the format select back onto a format. Unknown ids
// (they cannot come from this file, so they are defended against anyway)
// fall back to CSV.
func formatByID(id string) exportFormat {
	switch id {
	case "tsv":
		return exportTSV
	case "json":
		return exportJSON
	case "markdown":
		return exportMarkdown
	}
	return exportCSV
}

// formatOptions are the entries of the format dropdown, in export order.
func (m Model) formatOptions() []selectOption {
	opts := make([]selectOption, 0, exportFormatCount)
	for i := 0; i < int(exportFormatCount); i++ {
		f := exportFormat(i)
		opts = append(opts, selectOption{Value: f.id(), Label: m.t(f.labelKey())})
	}
	return opts
}

// exportDialog is the state of the modal exporter.
type exportDialog struct {
	open   bool
	focus  int // one of the exportFocus* stops below
	path   string
	caret  int
	format exportFormat
	// choice is the open dropdown of the format row. While it is open it is
	// modal inside the dialog: every key belongs to it.
	choice selectList
}

// The focus stops of the exporter, in the order Tab (and ↑↓, which wrap)
// walks them: the path field, the format select, the directory row and the
// Browse button.
//
// The directory row is a display, but it is a stop of its own so the
// pointer-free path can reach everything the dialog offers; Enter and Space
// on either of the last two stops open the directory picker, because a focus
// stop that ignores Enter would be a dead end.
const (
	exportFocusPath = iota
	exportFocusFormat
	exportFocusDirectory
	exportFocusBrowse

	exportFocusCount // number of focus stops, for the ↑↓/Tab wrap
)

// errNoResult is returned when there is nothing to write.
var errNoResult = errors.New("no result to export")

// openExportDialog opens the exporter for the active grid.
func (m *Model) openExportDialog() tea.Cmd {
	if m.session.result.res == nil || len(m.session.result.res.Columns) == 0 {
		return m.setMessage(m.t(i18n.KeyMsgNoResultsToExport))
	}
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeSettings()
	m.closeConnDialog()
	m.exportDlg = exportDialog{
		open:   true,
		path:   m.defaultExportPath(),
		caret:  0,
		format: exportCSV,
	}
	m.exportDlg.caret = len([]rune(m.exportDlg.path))
	return nil
}

// defaultExportPath suggests a timestamped file next to the active SQL file,
// or in the current directory when the tab has no file yet.
func (m Model) defaultExportPath() string {
	name := "results-" + time.Now().Format("20060102-150405") + exportCSV.extension()
	if m.fileName != "" {
		dir := filepath.Dir(m.fileName)
		if dir != "." && dir != "" {
			return filepath.Join(dir, name)
		}
	}
	return name
}

// closeExportDialog closes the exporter.
func (m *Model) closeExportDialog() {
	m.exportDlg.open = false
	m.exportDlg.choice.Close()
}

// openExportDirPicker opens the directory picker for the export path, seeded
// with the folder the path points at today.
//
// openFilePicker closes every dialog by contract, but the exporter is the
// dialog the picker is a *layer* of: it must still be there when the picker
// hands a folder back — and when the pick is cancelled. Its state is
// captured before the open and restored right after; both input handlers
// then route every pointer event to the picker first, so nothing behind the
// box can be clicked or typed until it closes.
func (m *Model) openExportDirPicker() {
	saved := m.exportDlg
	startDir := filepath.Dir(expandExportPath(strings.TrimSpace(m.exportDlg.path)))
	m.openFilePicker(pickerDir, startDir, "")
	m.exportDlg = saved
	m.pickerPurpose = pickerForExportDir
}

// applyExportDirectory moves the export file into dir: the folder part of
// the path follows the choice, the file name stays as typed, and the caret
// jumps to the end so the path can be edited right away. A path the user
// emptied has no name to keep and simply becomes the folder.
func (m *Model) applyExportDirectory(dir string) {
	if !m.exportDlg.open {
		return
	}
	if strings.TrimSpace(m.exportDlg.path) == "" {
		m.exportDlg.path = dir
	} else {
		m.exportDlg.path = filepath.Join(dir, filepath.Base(m.exportDlg.path))
	}
	m.exportDlg.caret = len([]rune(m.exportDlg.path))
}

// setExportFormat changes the format and keeps a conventional suffix in sync
// with it when the path already has one.
func (m *Model) setExportFormat(f exportFormat) {
	oldExt := m.exportDlg.format.extension()
	m.exportDlg.format = f
	ext := filepath.Ext(m.exportDlg.path)
	if strings.EqualFold(ext, oldExt) {
		m.exportDlg.path = strings.TrimSuffix(m.exportDlg.path, ext) + f.extension()
		m.exportDlg.caret = len([]rune(m.exportDlg.path))
	}
}

// exportResultText renders the current view in the chosen format.
func (m Model) exportResultText(f exportFormat) (string, error) {
	switch f {
	case exportJSON:
		return m.exportResultJSON()
	case exportMarkdown:
		return m.exportResultMarkdown()
	}
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return "", errNoResult
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = ','
	if f == exportTSV {
		w.Comma = '\t'
	}
	header := make([]string, len(r.res.Columns))
	for i, c := range r.res.Columns {
		header[i] = c.Name
	}
	if err := w.Write(header); err != nil {
		return "", err
	}
	for _, idx := range r.viewRows() {
		row := make([]string, len(r.res.Columns))
		for i := range row {
			if cell := cellAt(r.res.Rows[idx], i); cell != nil {
				row[i] = *cell
			}
		}
		if err := w.Write(row); err != nil {
			return "", err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// exportResultJSON renders the current view as a JSON array of objects: one
// object per row, columns in grid order, SQL NULL as a real JSON null. Only
// the object structure is joined by hand so the column order (and duplicate
// column names) survive; every string is escaped by encoding/json.
func (m Model) exportResultJSON() (string, error) {
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return "", errNoResult
	}
	var b strings.Builder
	b.WriteString("[")
	view := r.viewRows()
	for i, idx := range view {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n  {")
		for col, c := range r.res.Columns {
			if col > 0 {
				b.WriteString(",")
			}
			name, err := json.Marshal(c.Name)
			if err != nil {
				return "", err
			}
			b.Write(name)
			b.WriteString(":")
			value, err := json.Marshal(jsonValue(cellAt(r.res.Rows[idx], col)))
			if err != nil {
				return "", err
			}
			b.Write(value)
		}
		b.WriteString("}")
	}
	if len(view) == 0 {
		b.WriteString("]") // an empty result is still a valid, empty array
	} else {
		b.WriteString("\n]\n")
	}
	return b.String(), nil
}

// jsonValue maps a grid cell to its JSON shape: NULL becomes nil so
// encoding/json writes a bare null, never the string "NULL" or "".
func jsonValue(cell *string) any {
	if cell == nil {
		return nil
	}
	return *cell
}

// exportResultMarkdown renders the current view as a GitHub-style table: a
// header row, a separator row and one row per value. NULL is spelled out like
// the grid and the clipboard show it, so it cannot be mistaken for "".
func (m Model) exportResultMarkdown() (string, error) {
	r := m.session.result
	if r.res == nil || len(r.res.Columns) == 0 {
		return "", errNoResult
	}
	var b strings.Builder
	b.WriteString("|")
	for _, c := range r.res.Columns {
		b.WriteString(" " + markdownCell(c.Name) + " |")
	}
	b.WriteString("\n|")
	for range r.res.Columns {
		b.WriteString(" --- |")
	}
	for _, idx := range r.viewRows() {
		b.WriteString("\n|")
		for i := range r.res.Columns {
			b.WriteString(" " + markdownCell(cellText(cellAt(r.res.Rows[idx], i))) + " |")
		}
	}
	b.WriteString("\n")
	return b.String(), nil
}

// markdownCell escapes one value for a GitHub table cell. Backslashes go
// first so an existing \| stays literal, then pipes are escaped and newlines
// become <br>, keeping the value from breaking the table structure.
func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\r", "<br>")
	return s
}

// expandExportPath expands a leading ~ so the path field behaves like a shell.
func expandExportPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// submitExportDialog writes the file and closes the dialog.
func (m Model) submitExportDialog() (Model, tea.Cmd, bool) {
	path := strings.TrimSpace(m.exportDlg.path)
	if path == "" {
		return m, m.setMessage(m.t(i18n.KeyExportPathRequired)), true
	}
	text, err := m.exportResultText(m.exportDlg.format)
	if err != nil {
		m.closeExportDialog()
		return m, m.setMessage(m.t(i18n.KeyMsgExportFailed, err)), true
	}
	format := m.exportDlg.format
	rows := len(m.session.result.viewRows())
	m.closeExportDialog()
	tr := m.translator
	path = expandExportPath(path)
	return m, func() tea.Msg {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return statusMessageMsg(tr.T(i18n.KeyMsgExportFailed, err))
		}
		if rows == 1 {
			return statusMessageMsg(tr.T(i18n.KeyMsgOneRowExported, path, tr.T(format.labelKey())))
		}
		return statusMessageMsg(tr.T(i18n.KeyMsgExported, rows, path, tr.T(format.labelKey())))
	}, true
}

// ---- Geometry ----

// exportDialogRows is the dialog height the rules should guarantee: the
// frame (border/title/hint/buttons), the path, format, directory and browse
// rows and the format preview.
const exportDialogRows = 18

type exportLayout struct {
	x, y, w, h int
	innerW     int
	labelW     int
	valueW     int
	rowY       int
	valueX     int // screen column where the value column starts
	export     Rect
	cancel     Rect
	browse     Rect // the Browse… button on its own row (row 3)
}

// exportLayout computes the dialog geometry. The shared modal rules apply:
// the box wants 72×18, is never narrower than the shared minimum and never
// wider than the screen allows. The slack the minimum-width box has is given
// to the path field, so a long path scrolls instead of stretching the box.
func (m Model) exportLayout() exportLayout {
	labelW := max(lipgloss.Width(m.t(i18n.KeyExportFile)),
		lipgloss.Width(m.t(i18n.KeyExportFormat)),
		lipgloss.Width(m.t(i18n.KeyExportDirectory)))
	// The width the box asks for comes from the text the field will show:
	// a path that only looks long because of control characters must not
	// widen the dialog (those characters never reach the terminal).
	valueW := clamp(len([]rune(stripControl(m.exportDlg.path)))+1, 24, 60)
	maxValueW := 60
	if m.width > 0 {
		maxValueW = max(8, m.width-2-labelW-4)
		valueW = min(valueW, maxValueW)
	}
	innerW := labelW + valueW + 4
	// Leave room for the whole key hint when the terminal is wide enough;
	// a clipped hint is much less useful than a slightly wider path field.
	if hintW := lipgloss.Width(m.t(i18n.KeyExportHint)); hintW > innerW {
		if grow := min(hintW-innerW, maxValueW-valueW); grow > 0 {
			valueW += grow
			innerW += grow
		}
	}

	area := m.paneRects().area
	wantW := innerW + 2
	w, h := modalSize(wantW, exportDialogRows, area.W, area.H)
	// The box width is what the rules decided: the slack of the minimum-width
	// dialog goes to the path field (the caret handler keeps it visible by
	// scrolling, not by widening the box); on a narrow screen the path column
	// truncates. Every line is exactly the box wide either way.
	innerW = w - 2
	valueW = max(1, innerW-labelW-4)

	r := modalRect(area, w, h)
	x, y := r.X, r.Y

	l := exportLayout{
		x: x, y: y, w: w, h: h,
		innerW: innerW, labelW: labelW, valueW: valueW,
		rowY:   y + 2,
		valueX: x + labelW + 4,
	}
	btnY := y + h - 2
	l.export = Rect{X: x + 2, Y: btnY, W: lipgloss.Width(m.t(i18n.KeyExportAction)) + 2, H: 1}
	l.cancel = Rect{X: l.export.X + l.export.W + 2, Y: btnY, W: lipgloss.Width(m.t(i18n.KeyConnCancel)) + 2, H: 1}
	// The Browse button lives on its own row (the third focus stop below the
	// path row): its rectangle and the drawn button come from the same
	// numbers, exactly like the two buttons on the bottom row.
	l.browse = Rect{
		X: x + 2, Y: l.rowY + exportFocusBrowse,
		W: lipgloss.Width(" " + m.t(i18n.KeyExportBrowse) + " "), H: 1,
	}
	return l
}

// exportRowAt returns the row under the pointer (0 path, 1 format, 2
// directory, 3 browse). The preview and the hint below them are not focus
// targets, so they map to nothing.
func (m Model) exportRowAt(x, y int) (int, bool) {
	l := m.exportLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(x, y) {
		return 0, false
	}
	row := y - l.rowY
	if row < 0 || row > exportFocusBrowse {
		return 0, false
	}
	return row, true
}

// ---- Interaction ----

// openExportChoice opens the format dropdown on the current format.
func (m *Model) openExportChoice() {
	m.exportDlg.choice = selectList{}
	m.exportDlg.choice.Open(m.formatOptions(), m.exportDlg.format.id())
}

// exportChoiceLayout computes the geometry of the open format dropdown: it is
// anchored under the format row's value column and stays inside the box,
// above the button row.
func (m Model) exportChoiceLayout() (selectLayout, bool) {
	if !m.exportDlg.choice.open {
		return selectLayout{}, false
	}
	l := m.exportLayout()
	return layoutSelectList(m.exportDlg.choice.options, m.exportDlg.choice.sel,
		l.valueX, l.rowY+1, l.x+l.w-2, l.y+l.h-2), true
}

// pickExportChoice applies the value picked in the format dropdown.
func (m *Model) pickExportChoice(value string) {
	if f := formatByID(value); f.id() == value {
		m.setExportFormat(f)
	}
}

// handleExportKey handles a key press while the exporter is open.
func (m Model) handleExportKey(key string, runes []rune) (Model, tea.Cmd, bool) {
	// The open dropdown is modal inside the dialog: while it is open, no key
	// can reach the path or the fields behind it. Esc (and Tab) close the
	// list only; Enter picks the format right away. Every format value is
	// non-empty, so picked only matters for symmetry with the settings
	// dialog's locale select (whose "follow the system" value is "").
	if chosen, done, picked, handled := m.exportDlg.choice.handleKey(key); handled {
		if done && picked {
			m.pickExportChoice(chosen)
		}
		return m, nil, true
	}
	switch key {
	case "esc":
		m.closeExportDialog()
		return m, nil, true
	case "up", "shift+tab":
		m.exportDlg.focus = (m.exportDlg.focus - 1 + exportFocusCount) % exportFocusCount
		return m, nil, true
	case "down", "tab":
		m.exportDlg.focus = (m.exportDlg.focus + 1) % exportFocusCount
		return m, nil, true
	}

	switch m.exportDlg.focus {
	case exportFocusFormat:
		switch key {
		case "enter", "space", "down":
			// The format row is a select: Enter/Space/↓ open the list.
			m.openExportChoice()
		case "left", "right":
			// ←/→ keep cycling through the formats as a fallback, like the
			// conn form's type field does.
			m.setExportFormat(m.exportDlg.format.next())
		}
		return m, nil, true
	case exportFocusDirectory, exportFocusBrowse:
		// Both rows ask the same question — where does the file go? — so
		// Enter and Space open the directory picker from either stop; the
		// Browse button is the visible answer to it.
		if key == "enter" || key == "space" {
			m.openExportDirPicker()
		}
		return m, nil, true
	}

	// The path field edits the file name (focus == exportFocusPath). Enter
	// submits: the hint promises "⏎ export" and a keyboard user never has to
	// reach for the button.
	switch key {
	case "enter":
		return m.submitExportDialog()
	case "left":
		m.exportDlg.caret = max(0, m.exportDlg.caret-1)
	case "right":
		m.exportDlg.caret = min(len([]rune(m.exportDlg.path)), m.exportDlg.caret+1)
	case "home":
		m.exportDlg.caret = 0
	case "end":
		m.exportDlg.caret = len([]rune(m.exportDlg.path))
	case "backspace":
		if m.exportDlg.caret > 0 {
			r := []rune(m.exportDlg.path)
			m.exportDlg.path = string(r[:m.exportDlg.caret-1]) + string(r[m.exportDlg.caret:])
			m.exportDlg.caret--
		}
	case "delete":
		if r := []rune(m.exportDlg.path); m.exportDlg.caret < len(r) {
			m.exportDlg.path = string(r[:m.exportDlg.caret]) + string(r[m.exportDlg.caret+1:])
		}
	case "space":
		r := []rune(m.exportDlg.path)
		m.exportDlg.path = string(r[:m.exportDlg.caret]) + " " + string(r[m.exportDlg.caret:])
		m.exportDlg.caret++
	default:
		if len(runes) > 0 {
			// A paste arrives as one message whose runes hold the whole
			// pasted text, and it may carry escape sequences. The field
			// draws the path as plain text, so the control characters are
			// dropped before the runes are inserted — the same rule the
			// rename input and the grid's inputs apply.
			insert := stripControl(string(runes))
			if insert == "" {
				return m, nil, true
			}
			r := []rune(m.exportDlg.path)
			m.exportDlg.path = string(r[:m.exportDlg.caret]) + insert + string(r[m.exportDlg.caret:])
			m.exportDlg.caret += len([]rune(insert))
		}
	}
	return m, nil, true
}

// handleExportMouse handles a mouse event while the exporter is open.
func (m Model) handleExportMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Motion comes first: it belongs to the open format dropdown (a hovered
	// option is highlighted and nothing is picked) and does nothing anywhere
	// else. The dialog is modal, so a motion may never press a button, move
	// the caret or reach the panes behind it.
	if msg.Action == tea.MouseActionMotion {
		m.exportDlg.choice.hover = -1
		if m.exportDlg.choice.open {
			if l, ok := m.exportChoiceLayout(); ok {
				if idx, ok := selectRowAt(l, m.exportDlg.choice.options, msg.X, msg.Y); ok {
					m.exportDlg.choice.hover = idx
				}
			}
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	// The open dropdown is a layer of the dialog: a click on an option picks
	// it, a click anywhere else only closes the list — the same click must
	// never also press a button or change the focus behind the list.
	if m.exportDlg.choice.open {
		if l, ok := m.exportChoiceLayout(); ok {
			if idx, ok := selectRowAt(l, m.exportDlg.choice.options, msg.X, msg.Y); ok {
				m.exportDlg.choice.sel = idx
				chosen := m.exportDlg.choice.Chosen()
				m.exportDlg.choice.Close()
				m.pickExportChoice(chosen)
				return m, nil
			}
		}
		m.exportDlg.choice.Close()
		return m, nil
	}
	l := m.exportLayout()
	switch {
	case l.export.Contains(msg.X, msg.Y):
		next, cmd, _ := m.submitExportDialog()
		return next, cmd
	case l.cancel.Contains(msg.X, msg.Y):
		m.closeExportDialog()
		return m, nil
	case l.browse.Contains(msg.X, msg.Y):
		// The Browse button opens the directory picker; the exporter stays
		// open beneath it (openExportDirPicker restores the state the shared
		// openFilePicker contract closed).
		m.exportDlg.focus = exportFocusBrowse
		m.openExportDirPicker()
		return m, nil
	}
	if row, ok := m.exportRowAt(msg.X, msg.Y); ok {
		m.exportDlg.focus = row
		switch row {
		case exportFocusFormat:
			// The format row is a select: clicking its value opens the list.
			m.openExportChoice()
		case exportFocusDirectory, exportFocusBrowse:
			// The directory row is a display and the browse row is the
			// button (its rectangle was checked above): a click beside them
			// only moves the focus, it never opens a dialog on its own.
		default:
			m.exportDlg.caret = len([]rune(m.exportDlg.path))
		}
	}
	return m, nil
}

// exportPreviewRows is how many rows of the current format the preview
// shows: enough to recognize the shape, not enough to duplicate the grid.
const exportPreviewRows = 4

// exportPreviewLines renders what the current format would write, cut to
// the dialog's inner width and to at most maxRows rows (the caller shrinks
// the preview when the screen is too short to show it in full). Renderers
// run on the same code as the exporter, so the preview cannot drift from
// the result.
func (m Model) exportPreviewLines(innerW, maxRows int) []string {
	if maxRows <= 0 {
		return nil
	}
	text, err := m.exportResultText(m.exportDlg.format)
	if err != nil {
		return nil
	}
	out := make([]string, 0, maxRows)
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		// The preview draws the bytes the exporter would write, and those
		// bytes are data: a value carrying an escape sequence must not
		// repaint the terminal from inside the dialog. Only the preview is
		// cleaned — the file itself is written from the text above,
		// unchanged (see submitExportDialog), because the export is the
		// data, not a display of it.
		out = append(out, padANSI(truncateANSI(" "+foldBreaks(line), innerW), innerW))
		if len(out) == maxRows {
			break
		}
	}
	return out
}

// ---- Rendering ----

// renderExportDialog renders the exporter and returns its screen origin plus
// the block of lines to blit.
func (m Model) renderExportDialog() (x, y int, block []string, ok bool) {
	if !m.exportDlg.open {
		return 0, 0, nil, false
	}
	l := m.exportLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	title := padANSI(truncateANSI(" "+m.t(i18n.KeyExportTitle), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgTitle.Render(title)+border.Render("│"))

	// The path is drawn from the cleaned text: it can be seeded from a
	// directory listing, and the dialog must not be the place an escape
	// sequence reaches the terminal. The caret counts runes of the stored
	// path, so it is mapped onto the cleaned text by counting the printable
	// runes in front of it — the position the caret names stays the position
	// the caret points at, and the clamp below only guards a caret that the
	// editing itself already put out of range.
	path := stripControl(m.exportDlg.path)
	if m.exportDlg.focus == exportFocusPath {
		runes := []rune(path)
		raw := []rune(m.exportDlg.path)
		at := clamp(m.exportDlg.caret, 0, len(raw))
		at = clamp(len([]rune(stripControl(string(raw[:at])))), 0, len(runes))
		// Keep the caret visible by sliding a window over long paths. One
		// cell of the value area is reserved for the caret.
		maxChars := max(1, l.valueW-1)
		start := 0
		if at > maxChars {
			start = at - maxChars
		}
		end := min(len(runes), start+maxChars)
		visible := runes[start:end]
		at -= start
		before := string(visible[:at])
		after := padANSI(string(visible[at:]), maxChars-at)
		row := m.theme.dlgRowSel.Render("▸") +
			m.theme.dlgRowSel.Render(padANSI(m.t(i18n.KeyExportFile), l.labelW)) +
			m.theme.dlgRowSel.Render("  ") +
			m.theme.dlgValueSel.Render(before) +
			m.theme.cursor.Render(" ") +
			m.theme.dlgValueSel.Render(after) +
			m.theme.dlgRowSel.Render(" ")
		out = append(out, border.Render("│")+truncateANSI(row, l.innerW)+border.Render("│"))
	} else {
		label := m.theme.dlgLabel.Render(padANSI(m.t(i18n.KeyExportFile), l.labelW))
		value := m.theme.dlgValue.Render(padANSI(truncateANSI(path, l.valueW), l.valueW))
		out = append(out, border.Render("│")+box.Render(" ")+label+box.Render("  ")+value+box.Render(" ")+border.Render("│"))
	}

	// The format row is a select: it shows the current label and the dropdown
	// marker, like the conn form's type field.
	format := m.t(m.exportDlg.format.labelKey()) + selectGlyph
	if m.exportDlg.focus == exportFocusFormat {
		row := m.theme.dlgRowSel.Render("▸") +
			m.theme.dlgRowSel.Render(padANSI(m.t(i18n.KeyExportFormat), l.labelW)) +
			m.theme.dlgRowSel.Render("  ") +
			m.theme.dlgValueSel.Render(padANSI(format, l.valueW)) +
			m.theme.dlgRowSel.Render(" ")
		out = append(out, border.Render("│")+truncateANSI(row, l.innerW)+border.Render("│"))
	} else {
		label := m.theme.dlgLabel.Render(padANSI(m.t(i18n.KeyExportFormat), l.labelW))
		value := m.theme.dlgValue.Render(padANSI(truncateANSI(format, l.valueW), l.valueW))
		out = append(out, border.Render("│")+box.Render(" ")+label+box.Render("  ")+value+box.Render(" ")+border.Render("│"))
	}

	// The directory row shows where the file would land. It follows the path
	// field as the path is edited, so the two never disagree. The folder of
	// the path carries the same risk as the path itself, so it is cleaned
	// for the same reason.
	dir := stripControl(filepath.Dir(expandExportPath(m.exportDlg.path)))
	if m.exportDlg.focus == exportFocusDirectory {
		row := m.theme.dlgRowSel.Render("▸") +
			m.theme.dlgRowSel.Render(padANSI(m.t(i18n.KeyExportDirectory), l.labelW)) +
			m.theme.dlgRowSel.Render("  ") +
			m.theme.dlgValueSel.Render(padANSI(truncateANSI(dir, l.valueW), l.valueW)) +
			m.theme.dlgRowSel.Render(" ")
		out = append(out, border.Render("│")+truncateANSI(row, l.innerW)+border.Render("│"))
	} else {
		label := m.theme.dlgLabel.Render(padANSI(m.t(i18n.KeyExportDirectory), l.labelW))
		value := m.theme.dlgValue.Render(padANSI(truncateANSI(dir, l.valueW), l.valueW))
		out = append(out, border.Render("│")+box.Render(" ")+label+box.Render("  ")+value+box.Render(" ")+border.Render("│"))
	}

	// The Browse button sits on its own row; the marker in front of it says
	// whether that row holds the keyboard focus. Its position comes from
	// l.browse, so the drawn button and the clickable button agree.
	marker := m.theme.dlgRowSel.Render(" ")
	browseBtn := m.theme.toolBtn.Render(" " + m.t(i18n.KeyExportBrowse) + " ")
	if m.exportDlg.focus == exportFocusBrowse {
		marker = m.theme.dlgRowSel.Render("▸")
		browseBtn = m.theme.toolBtnHover.Render(" " + m.t(i18n.KeyExportBrowse) + " ")
	}
	browse := padANSI(truncateANSI(marker+browseBtn, l.innerW), l.innerW)
	out = append(out, border.Render("│")+browse+border.Render("│"))

	// The preview shows what the chosen format will write. On a screen too
	// short for everything the preview gives up rows first: the frame rows
	// (hint and buttons included) are what the shared dialog shape owes the
	// user, so they always stay in the box.
	previewRows := exportPreviewRows
	if spare := l.h - 9; spare < previewRows {
		// 9 = both borders, title, the path/format/directory/browse rows,
		// the hint and the button row.
		previewRows = max(0, spare)
	}
	for _, line := range m.exportPreviewLines(l.innerW, previewRows) {
		out = append(out, border.Render("│")+m.theme.dlgDesc.Render(line)+border.Render("│"))
	}

	// On a narrow screen the long hint would lose its Esc part to the
	// truncation, so a short hint takes over.
	hintKey := i18n.KeyExportHint
	if lipgloss.Width(m.t(hintKey)) > l.innerW {
		hintKey = i18n.KeyExportHintShort
	}
	hint := padANSI(truncateANSI(m.t(hintKey), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgDesc.Render(hint)+border.Render("│"))
	blank := border.Render("│") + box.Render(strings.Repeat(" ", l.innerW)) + border.Render("│")
	for len(out) < l.h-2 {
		out = append(out, blank)
	}
	export := m.theme.toolBtnHover.Render(" " + m.t(i18n.KeyExportAction) + " ")
	cancel := m.theme.toolBtn.Render(" " + m.t(i18n.KeyConnCancel) + " ")
	row := strings.Repeat(" ", l.export.X-l.x-1) + export + "  " + cancel
	out = append(out, border.Render("│")+padANSI(truncateANSI(row, l.innerW), l.innerW)+border.Render("│"))
	out = append(out, border.Render("└"+rule+"┘"))

	// The open dropdown is drawn over the preview rows it covers, inside the
	// frame; its layout is shared with the mouse hit test.
	if sl, dok := m.exportChoiceLayout(); dok {
		for i, pl := range m.renderSelectList(sl, &m.exportDlg.choice) {
			rowIdx := sl.rect.Y - l.y + i // out[0] is the top border row
			if rowIdx < 0 || rowIdx >= len(out) {
				continue
			}
			line := out[rowIdx]
			lx := sl.rect.X - l.x
			prefix := truncateANSI(line, lx)
			suffix := cutLeftANSI(line, lx+lipgloss.Width(pl))
			out[rowIdx] = prefix + pl + suffix
		}
	}
	return l.x, l.y, out, true
}
