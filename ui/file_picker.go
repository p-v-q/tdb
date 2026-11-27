package ui

// file_picker.go implements the built-in file/directory picker: the modal
// browser behind "open SQL file" and "export to a directory". A terminal
// application cannot rely on a system file dialog, so the picker draws its
// own (docs/ux-round7.md, section 4).
//
//   - The path line at the top is editable and accepts a full path, "~" and
//     paths relative to the current folder; the entry list below is filtered
//     by typing. Tab moves between the path line and the list.
//   - Enter descends into a directory (and picks a file in file mode),
//     Backspace walks up (it edits the filter while one is in effect) and
//     Esc cancels. The directory mode has a button that picks the current
//     folder regardless of the highlight.
//   - The picker never writes anything: it only reports the chosen path in
//     filePicker.result once open goes false ("" = cancelled).
//   - Rendering and mouse hit testing share pickerLayout, the same contract
//     every dialog in this package obeys (see modal.go).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// The size the picker asks the shared modal rules for, plus the shortest
// frame it can draw: borders (2) + title + blank row + path line + filter
// line + one list row + hint + buttons = 9 rows. Below that the drawn rows
// and the hit rectangles can no longer come from one formula, so the
// picker shows nothing at all (the same early exit renderFilePicker had
// for a box too narrow to hold a row).
const (
	pickerWantW = 72
	pickerWantH = 18
	pickerMinH  = 9
)

// pickerMode decides what the picker may hand back: a file or the folder
// itself.
type pickerMode int

const (
	pickerFile pickerMode = iota
	pickerDir
)

// pickerEntry is one row of the listing. The parent row carries name ".."
// and is localized only at render time, so the model stays language-neutral.
type pickerEntry struct {
	name   string // file name as the filesystem stores it (".." for the parent)
	dir    bool
	parent bool
}

// The focus targets of Tab: the list, the path line and the button row. Tab
// cycles list → path → buttons → list, so the primary action ("Open" or
// "Choose this folder") is reachable without a pointer.
const (
	pickerFocusList = iota
	pickerFocusPath
	pickerFocusActions
)

// filePicker is the state of the modal picker.
type filePicker struct {
	open bool
	mode pickerMode
	dir  string // the folder being listed, absolute

	entries []pickerEntry // the listing of dir, in display order
	sel     int           // highlighted row of the visible list (Enter/click acts on it)
	scroll  int           // first visible row
	hover   int           // row under the pointer, -1 when the pointer is elsewhere

	filter    string // typed narrowing of the list
	filtering bool   // a filter is in effect (kept in sync with filter != "")
	// suffix restricts the files the list shows to the ones ending in it,
	// compared case-insensitively. It is a separate restriction from the
	// typed filter: a glob like "*.sql" would never match a file name as
	// text, which is what made the picker open on an empty list. Empty
	// means no restriction; directories and the ".." row ignore it.
	suffix string

	err error // last folder/entry failure; shown inline, it never closes the picker

	path  string // the editable path line (mirrors dir, may hold a draft)
	caret int    // rune caret inside path
	focus int    // pickerFocusPath, pickerFocusList or pickerFocusActions
	// action is the selected button while focus is pickerFocusActions:
	// 0 is the primary button, 1 is Cancel.
	action int

	// result is what the picker hands back once open is false: the chosen
	// path, or "" when the interaction ended without a choice (Esc, the
	// Cancel button). A valid pick can never be "", so the two cases are
	// never confused.
	result string
}

// ---- Opening ----

// openFilePicker opens the picker on startDir (falling back to the user's
// home and then to the working directory when that folder cannot be read).
// filter seeds the list filter; it may be empty.
//
// The function has no return value: the integration decides whether reading
// the directory deserves a tea.Cmd. The outcome is read from m.picker.result
// after a key/mouse handler reports the picker closed.
func (m *Model) openFilePicker(mode pickerMode, startDir, filter string) {
	m.openFilePickerWithSuffix(mode, startDir, filter, "")
}

// openFilePickerWithSuffix is openFilePicker plus a file-extension
// restriction: only files whose name ends in suffix (case-insensitively) are
// listed, while directories and ".." stay reachable. An empty suffix means
// no restriction. Keeping it apart from filter is the point: filter is text
// the user types and backspaces, the suffix is what the dialog was opened
// for ("open SQL file").
func (m *Model) openFilePickerWithSuffix(mode pickerMode, startDir, filter, suffix string) {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeSettings()
	m.closeConnDialog()
	m.closeExportDialog()
	m.closeHistoryDialog()
	m.closeFind()
	m.popup.close()
	m.picker = filePicker{
		open:   true,
		mode:   mode,
		filter: filter,
		suffix: strings.ToLower(suffix),
		hover:  -1,
		focus:  pickerFocusList,
	}
	m.picker.filtering = filter != ""
	m.picker.loadWithFallback(startDir)
}

// closeFilePicker dismisses the picker without a choice.
func (m *Model) closeFilePicker() {
	m.picker.open = false
	m.picker.result = ""
}

// loadWithFallback picks the folder to open: startDir when it can be read,
// the user's home directory when it cannot, and the working directory as
// the last resort — a broken argument must not stop the picker from
// opening.
func (p *filePicker) loadWithFallback(startDir string) {
	var candidates []string
	if s := strings.TrimSpace(startDir); s != "" {
		s = expandExportPath(s) // the "~" rules live next to the export path field
		// A file names its own folder: the picker browses directories.
		if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
			s = filepath.Dir(s)
		}
		candidates = append(candidates, s)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, home)
	}
	candidates = append(candidates, ".")

	var lastErr error
	for _, c := range candidates {
		entries, err := loadPickerEntries(c)
		if err != nil {
			lastErr = err
			continue
		}
		p.setDir(c, entries)
		return
	}
	// Nothing was readable: keep the first candidate as the location and
	// show the message instead of refusing to open. candidates always has
	// at least the "." entry, so lastErr is set whenever the loop returns.
	p.dir = absPickerPath(candidates[0])
	p.path = p.dir
	p.caret = len([]rune(p.path))
	p.entries = nil
	p.err = lastErr
}

// setDir makes dir the current folder and resets the position: a new
// listing starts at the top with the previous error cleared. The filter is
// deliberately kept — narrowing the same way across folders is the point of
// a filter, and Backspace clears it.
func (p *filePicker) setDir(dir string, entries []pickerEntry) {
	p.dir = absPickerPath(dir)
	p.entries = entries
	p.err = nil
	p.path = p.dir
	p.caret = len([]rune(p.path))
	p.sel, p.scroll, p.hover = 0, 0, -1
}

// absPickerPath returns a clean absolute path, so the path line and the
// parent-folder logic never depend on the working directory.
func absPickerPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// loadPickerEntries reads one folder: ".." first (except at the root),
// then directories, then files. os.ReadDir returns names sorted, so both
// groups stay alphabetical without a second sort.
//
// A symlink is classified by where it points (following it once with
// os.Stat): entering a linked folder must behave like entering the folder.
// A broken symlink stays a file — it exists as a name, and picking it
// reports the failure inline instead of vanishing from the list.
func loadPickerEntries(dir string) ([]pickerEntry, error) {
	clean := absPickerPath(dir)
	des, err := os.ReadDir(clean)
	if err != nil {
		return nil, err
	}
	dirs := make([]os.DirEntry, 0, len(des))
	files := make([]os.DirEntry, 0, len(des))
	for _, d := range des {
		isDir := d.IsDir()
		if !isDir && d.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(clean, d.Name())); err == nil {
				isDir = fi.IsDir()
			}
		}
		if isDir {
			dirs = append(dirs, d)
		} else {
			files = append(files, d)
		}
	}

	out := make([]pickerEntry, 0, len(des)+1)
	if parent := filepath.Dir(clean); parent != clean {
		out = append(out, pickerEntry{name: "..", dir: true, parent: true})
	}
	for _, d := range dirs {
		out = append(out, pickerEntry{name: d.Name(), dir: true})
	}
	for _, d := range files {
		out = append(out, pickerEntry{name: d.Name()})
	}
	return out, nil
}

// ---- Entries and filtering ----

// pickerEntryLabel is the text a row shows: the localized parent line for
// "..", directories marked with the folder glyph of the active icon set (or
// a trailing "/" when the plain set has no glyph), files as they are stored.
//
// Every name is passed through stripControl first: a file name comes from
// the filesystem and may carry an escape sequence, which the terminal would
// execute if it reached the screen verbatim.
func (m Model) pickerEntryLabel(e pickerEntry) string {
	if e.parent {
		return stripControl(m.t(i18n.KeyPickerParent))
	}
	name := stripControl(e.name)
	if !e.dir {
		return name
	}
	if glyph := m.icons().folder; glyph != "" {
		return glyph + " " + name
	}
	return name + "/"
}

// pickerVisible returns the indexes of the entries the list shows, in list
// order: dotfiles hidden unless the filter starts with ".", files hidden in
// directory mode, files outside the suffix restriction dropped (file mode
// only, and never a directory or ".."), and rows the filter does not match
// dropped. The parent row follows the filter like any other row (its label
// is ordinary text), but neither the dotfile rule nor the suffix.
func (m Model) pickerVisible() []int {
	q := strings.ToLower(m.picker.filter)
	showDot := strings.HasPrefix(m.picker.filter, ".")
	suffix := strings.ToLower(m.picker.suffix)
	out := make([]int, 0, len(m.picker.entries))
	for i, e := range m.picker.entries {
		if !e.parent {
			if m.picker.mode == pickerDir && !e.dir {
				continue
			}
			if !showDot && strings.HasPrefix(e.name, ".") {
				continue
			}
			if suffix != "" && !e.dir && !strings.HasSuffix(strings.ToLower(e.name), suffix) {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(m.pickerEntryLabel(e)), q) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// pickerStatusMessage is the line drawn above the entries when the list has
// nothing useful to show: an unreadable (or vanished) folder first, then a
// filter without matches, then a folder this mode cannot pick from (the
// directory mode hides files, so a folder of only files is not empty), then
// an empty folder. It returns "" when every entry is listed normally.
func (m Model) pickerStatusMessage() string {
	if m.picker.err != nil {
		return m.t(i18n.KeyPickerUnreadable)
	}
	hasMatch := false
	for _, i := range m.pickerVisible() {
		if !m.picker.entries[i].parent {
			hasMatch = true
			break
		}
	}
	if hasMatch {
		return ""
	}
	if m.picker.filter != "" {
		return m.t(i18n.KeyPickerNoMatch)
	}
	// No text filter hides anything: either the folder is empty, or a
	// restriction the user did not type hides files that really are there
	// (the suffix of the file mode, the directory mode's "files are not
	// pickable"). "No match" tells the truer story in that case, "empty"
	// only when the folder holds nothing beyond the parent row.
	if m.picker.suffix != "" || m.picker.mode == pickerDir {
		for _, e := range m.picker.entries {
			if !e.parent {
				return m.t(i18n.KeyPickerNoMatch)
			}
		}
	}
	return m.t(i18n.KeyPickerEmpty)
}

// pickerPrimaryLabel is the label of the action button: the directory mode
// picks the folder it is showing, the file mode opens the highlighted row.
func (m Model) pickerPrimaryLabel() string {
	if m.picker.mode == pickerDir {
		return m.t(i18n.KeyPickerChoose)
	}
	return m.t(i18n.KeyPickerOpen)
}

// ---- Geometry ----

// pickerLayout is the computed geometry of the open picker; rendering and
// mouse hit testing share it, so the drawn box and the clickable box agree.
type pickerLayout struct {
	// ok reports whether the frame fits the screen at all. When it is
	// false nothing is drawn and every rectangle stays empty, so no click
	// can reach a row that is not on screen.
	ok         bool
	x, y, w, h int
	innerW     int
	pathY      int
	filterY    int
	listY      int
	listH      int
	statusRows int // rows the status message takes at the top of the list
	entryRows  int // rows the entries themselves may use
	pathRect   Rect
	filterRect Rect
	choose     Rect
	cancel     Rect
}

// pickerLayout computes the dialog geometry: the shared modal rules size a
// 72×18 want, and the frame is the usual one — title, one blank row, the
// path line, the filter line, the list, the hint and the buttons.
//
// The rows are counted once, here: the render writes exactly 8 + listH
// lines (two borders, title, blank, path, filter, list, hint, buttons),
// which is h by construction, and the button row sits at the bottom the
// hit rectangles are taken from. A screen too small for that nine-row
// minimum yields ok=false instead of a box whose drawn rows and clickable
// rows disagree.
func (m Model) pickerLayout() pickerLayout {
	area := m.paneRects().area
	w, h := modalSize(pickerWantW, pickerWantH, area.W, area.H)
	r := modalRect(area, w, h)
	innerW := w - 2

	l := pickerLayout{
		x: r.X, y: r.Y, w: w, h: h, innerW: innerW,
		pathY:   r.Y + 3,
		filterY: r.Y + 4,
		listY:   r.Y + 5,
	}
	if w < 4 || h < pickerMinH {
		return l // ok stays false: no list, no clickable rectangle
	}
	l.ok = true
	// Frame (2) + title + blank + path + filter + hint + buttons leaves
	// h-8 rows for the list (h >= pickerMinH keeps that at one row).
	l.listH = h - 8
	if m.pickerStatusMessage() != "" {
		l.statusRows = 1
	}
	l.entryRows = max(0, l.listH-l.statusRows)
	l.pathRect = Rect{X: r.X + 1, Y: l.pathY, W: innerW, H: 1}
	l.filterRect = Rect{X: r.X + 1, Y: l.filterY, W: innerW, H: 1}

	btnY := r.Y + h - 2
	l.choose = Rect{X: r.X + 2, Y: btnY, W: lipgloss.Width(m.pickerPrimaryLabel()) + 2, H: 1}
	l.cancel = Rect{X: l.choose.X + l.choose.W + 2, Y: btnY, W: lipgloss.Width(m.t(i18n.KeyConnCancel)) + 2, H: 1}
	// The button row is drawn truncated to innerW, so the hit rectangles
	// are clipped to the part that is actually drawn: on a screen too
	// narrow for both buttons the unclipped rectangles would reach past the
	// border, letting a stray click outside the box reach a button (the
	// confirmation dialog follows the same rule).
	rowEnd := r.X + 1 + innerW
	clip := func(rc Rect) Rect {
		if rc.X > rowEnd {
			rc.X = rowEnd
		}
		if rc.X+rc.W > rowEnd {
			rc.W = max(0, rowEnd-rc.X)
		}
		return rc
	}
	l.choose, l.cancel = clip(l.choose), clip(l.cancel)
	return l
}

// pickerRowAt maps a press inside the list area to the visible row under
// the pointer. The status message row (when present) is not an entry.
func (m Model) pickerRowAt(x, y int) (int, bool) {
	l := m.pickerLayout()
	if !l.ok {
		return 0, false // nothing is on screen to point at
	}
	if !(Rect{X: l.x, Y: l.listY, W: l.w, H: l.listH}).Contains(x, y) {
		return 0, false
	}
	row := y - l.listY
	if row < l.statusRows || row >= l.listH {
		return 0, false
	}
	idx := m.picker.scroll + row - l.statusRows
	if idx < 0 || idx >= len(m.pickerVisible()) {
		return 0, false
	}
	return idx, true
}

// ---- Navigation ----

// pickerEnsureVisible scrolls the list so the selection stays inside it.
func (m *Model) pickerEnsureVisible() {
	vis := m.pickerVisible()
	if len(vis) == 0 {
		m.picker.scroll = 0
		return
	}
	rows := max(1, m.pickerLayout().entryRows)
	if m.picker.sel < m.picker.scroll {
		m.picker.scroll = m.picker.sel
	}
	if m.picker.sel >= m.picker.scroll+rows {
		m.picker.scroll = m.picker.sel - rows + 1
	}
	m.picker.scroll = clamp(m.picker.scroll, 0, max(0, len(vis)-rows))
}

// pickerClamp re-derives a valid selection after the visible list changed
// (typing, deleting a filter character, a new folder).
func (m *Model) pickerClamp() {
	// The rows the pointer named may be gone from the list, so the stale
	// hover index is dropped; a motion event sets it again.
	m.picker.hover = -1
	vis := m.pickerVisible()
	m.picker.sel = clamp(m.picker.sel, 0, max(0, len(vis)-1))
	if len(vis) == 0 {
		m.picker.sel, m.picker.scroll = 0, 0
		return
	}
	m.pickerEnsureVisible()
}

// pickerMove moves the selection by delta rows and keeps it visible.
func (m *Model) pickerMove(delta int) {
	vis := m.pickerVisible()
	if len(vis) == 0 {
		return
	}
	m.picker.sel = clamp(m.picker.sel+delta, 0, len(vis)-1)
	m.pickerEnsureVisible()
}

// pickerCd enters a folder; a failure is reported inline and the picker
// stays where it is.
func (m *Model) pickerCd(target string) {
	entries, err := loadPickerEntries(target)
	if err != nil {
		m.picker.err = err
		return
	}
	m.picker.setDir(target, entries)
}

// pickerParent walks one level up. At the root the parent equals the
// folder itself, so nothing happens.
func (m *Model) pickerParent() {
	parent := filepath.Dir(m.picker.dir)
	if parent == m.picker.dir {
		return
	}
	m.pickerCd(parent)
}

// pickerChoose records the path and closes the picker.
func (m *Model) pickerChoose(path string) {
	m.picker.result = path
	m.picker.open = false
}

// pickerActivate acts on the highlighted row, exactly like Enter and like a
// click on a row: the parent walks up, a directory is entered, a file is
// handed back (file mode). An entry that vanished in the meantime reports
// inline instead of closing the picker.
func (m *Model) pickerActivate() {
	vis := m.pickerVisible()
	if m.picker.sel < 0 || m.picker.sel >= len(vis) {
		return
	}
	e := m.picker.entries[vis[m.picker.sel]]
	switch {
	case e.parent:
		m.pickerParent()
	case e.dir:
		m.pickerCd(filepath.Join(m.picker.dir, e.name))
	default:
		path := filepath.Join(m.picker.dir, e.name)
		fi, err := os.Stat(path)
		if err != nil {
			m.picker.err = err
			return
		}
		if fi.IsDir() {
			m.pickerCd(path)
			return
		}
		m.pickerChoose(path)
	}
}

// pickerJump resolves the path line: "~" through the shared expansion,
// relative paths against the current folder. A folder is entered; in file
// mode a file is picked right away, so typing the full path and pressing
// Enter finishes the dialog. Anything else reports inline.
func (m *Model) pickerJump() {
	target := strings.TrimSpace(m.picker.path)
	if target == "" {
		m.picker.err = fmt.Errorf("picker: the path line is empty")
		return
	}
	target = expandExportPath(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(m.picker.dir, target)
	}
	target = absPickerPath(target)
	entries, err := loadPickerEntries(target)
	if err == nil {
		m.picker.setDir(target, entries)
		return
	}
	// A file names itself: in file mode, jumping to a file picks it right
	// away so a fully typed path finishes the dialog.
	if m.picker.mode == pickerFile {
		if fi, serr := os.Stat(target); serr == nil && !fi.IsDir() {
			m.pickerChoose(target)
			return
		}
	}
	m.picker.err = err
}

// pickerFilterKey appends runes to the list filter. Only the keys whose key
// name IS the text they carry count as typing: modified keys (Ctrl+A) carry
// runes as well and belong to the shortcut layer this modal input shields.
func (m *Model) pickerFilterKey(key string, runes []rune) {
	if len(runes) == 0 || (key != string(runes) && key != "space") {
		return
	}
	m.picker.filter += string(runes)
	m.picker.filtering = m.picker.filter != ""
	m.pickerClamp()
}

// pickerFilterBackspace drops the last rune of the filter, or widens the
// list back to the whole folder when only the suffix restriction is left
// (the way the old "*.sql" seed filter could be backspaced away), and walks
// to the parent folder when there is nothing left to widen.
func (m *Model) pickerFilterBackspace() {
	if r := []rune(m.picker.filter); len(r) > 0 {
		m.picker.filter = string(r[:len(r)-1])
		m.picker.filtering = m.picker.filter != ""
		m.pickerClamp()
		return
	}
	if m.picker.suffix != "" {
		// The suffix is the last narrowing in effect: ⌫ lifts it before it
		// walks up, so one key always widens the list the user is looking at.
		m.picker.suffix = ""
		m.pickerClamp()
		return
	}
	m.pickerParent()
}

// ---- Keyboard ----

// handleFilePickerKey handles a key press while the picker is open and
// reports whether it consumed the key. The picker is modal: it consumes
// everything while open.
func (m Model) handleFilePickerKey(key string, runes []rune) (Model, tea.Cmd, bool) {
	if !m.picker.open {
		return m, nil, false
	}
	if key == "esc" {
		m.closeFilePicker()
		return m, nil, true
	}

	if m.picker.focus == pickerFocusPath {
		m.pickerPathKey(key, runes)
		return m, nil, true
	}
	if m.picker.focus == pickerFocusActions {
		m.pickerActionKey(key)
		return m, nil, true
	}

	switch key {
	case "tab":
		m.picker.focus = pickerFocusPath
		m.picker.caret = len([]rune(m.picker.path))
		return m, nil, true
	case "shift+tab":
		m.picker.focus = pickerFocusActions
		m.picker.action = 0
		return m, nil, true
	case "up":
		m.pickerMove(-1)
	case "down":
		m.pickerMove(1)
	case "pgup":
		m.pickerMove(-max(1, m.pickerLayout().entryRows))
	case "pgdown":
		m.pickerMove(max(1, m.pickerLayout().entryRows))
	case "home":
		m.picker.sel = 0
		m.pickerEnsureVisible()
	case "end":
		m.picker.sel = max(0, len(m.pickerVisible())-1)
		m.pickerEnsureVisible()
	case "enter":
		m.pickerActivate()
	case "backspace":
		m.pickerFilterBackspace()
	default:
		m.pickerFilterKey(key, runes)
	}
	// Every other key is swallowed so nothing leaks to the panes behind.
	return m, nil, true
}

// pickerActionKey handles the button row while it has the keyboard: ←/→
// choose between the primary action and Cancel, Enter runs the selected one.
// The primary action is "Open" in file mode (it activates the highlighted row,
// like Enter in the list) and "Choose this folder" in directory mode.
func (m *Model) pickerActionKey(key string) {
	switch key {
	case "tab":
		m.picker.focus = pickerFocusList
	case "shift+tab":
		m.picker.focus = pickerFocusPath
		m.picker.caret = len([]rune(m.picker.path))
	case "left", "right":
		m.picker.action = 1 - m.picker.action
	case "enter", "space":
		if m.picker.action == 1 {
			m.closeFilePicker()
			return
		}
		if m.picker.mode == pickerDir {
			m.pickerChoose(m.picker.dir)
			return
		}
		m.pickerActivate()
	}
}

// pickerPathKey edits the path line while it owns the keyboard.
func (m *Model) pickerPathKey(key string, runes []rune) {
	switch key {
	case "tab":
		m.picker.focus = pickerFocusActions
		m.picker.action = 0
	case "shift+tab":
		m.picker.focus = pickerFocusList
	case "enter":
		m.pickerJump()
	case "left":
		m.picker.caret = max(0, m.picker.caret-1)
	case "right":
		m.picker.caret = min(len([]rune(m.picker.path)), m.picker.caret+1)
	case "home":
		m.picker.caret = 0
	case "end":
		m.picker.caret = len([]rune(m.picker.path))
	case "backspace":
		if m.picker.caret > 0 {
			r := []rune(m.picker.path)
			m.picker.path = string(r[:m.picker.caret-1]) + string(r[m.picker.caret:])
			m.picker.caret--
		}
	case "delete":
		if r := []rune(m.picker.path); m.picker.caret < len(r) {
			m.picker.path = string(r[:m.picker.caret]) + string(r[m.picker.caret+1:])
		}
	case "space":
		r := []rune(m.picker.path)
		m.picker.path = string(r[:m.picker.caret]) + " " + string(r[m.picker.caret:])
		m.picker.caret++
	default:
		if len(runes) > 0 && (key == string(runes) || key == "space") {
			r := []rune(m.picker.path)
			insert := string(runes)
			m.picker.path = string(r[:m.picker.caret]) + insert + string(r[m.picker.caret:])
			m.picker.caret += len([]rune(insert))
		}
	}
}

// ---- Mouse ----

// handleFilePickerMouse handles a pointer event while the picker is open.
// The picker is modal: motion highlights a row (and never moves the
// selection), the wheel scrolls the list, and clicks outside the dialog are
// swallowed instead of reaching the panes behind.
func (m Model) handleFilePickerMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if !m.picker.open {
		return m, nil
	}
	l := m.pickerLayout()
	// With no frame on screen there is no dialog to be inside of, so every
	// event lands "outside" and is swallowed by the modal rules.
	inside := l.ok && (Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(msg.X, msg.Y)

	if tea.MouseEvent(msg).IsWheel() {
		if !inside {
			return m, nil
		}
		// The wheel moves the selection, which scrolls the list to keep it
		// visible: keyboard and pointer stay on one model, like the query
		// history does.
		if delta := wheelDelta(msg.Button); delta != 0 {
			m.pickerMove(delta)
		}
		return m, nil
	}
	if msg.Action == tea.MouseActionMotion {
		// Hover is a view of the pointer: it never changes the selection.
		// A motion outside the list clears it, and a motion outside the
		// dialog is ignored (the picker is modal).
		if idx, ok := m.pickerRowAt(msg.X, msg.Y); ok {
			m.picker.hover = idx
		} else {
			m.picker.hover = -1
		}
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	switch {
	case l.choose.Contains(msg.X, msg.Y):
		if m.picker.mode == pickerDir {
			// The button picks the current folder regardless of the row
			// that happens to be highlighted.
			m.pickerChoose(m.picker.dir)
		} else {
			m.pickerActivate()
		}
		return m, nil
	case l.cancel.Contains(msg.X, msg.Y):
		m.closeFilePicker()
		return m, nil
	case l.pathRect.Contains(msg.X, msg.Y):
		m.picker.focus = pickerFocusPath
		m.picker.caret = len([]rune(m.picker.path))
		return m, nil
	case l.filterRect.Contains(msg.X, msg.Y):
		// The filter is edited from the list (typing goes straight to it),
		// so clicking the line hands the keyboard there.
		m.picker.focus = pickerFocusList
		return m, nil
	}
	if idx, ok := m.pickerRowAt(msg.X, msg.Y); ok {
		m.picker.sel = idx
		m.picker.focus = pickerFocusList
		m.pickerEnsureVisible()
		m.pickerActivate()
	}
	return m, nil
}

// ---- Rendering ----

// renderFilePicker renders the picker and returns its screen origin plus
// the block of lines to blit. The block is exactly l.h lines long — the
// same frame the hit rectangles are taken from — or absent entirely when
// the screen cannot hold the frame.
func (m Model) renderFilePicker() (x, y int, block []string, ok bool) {
	if !m.picker.open {
		return 0, 0, nil, false
	}
	l := m.pickerLayout()
	if !l.ok {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)
	// A row is plain text truncated and padded to the inner width, then
	// styled in one piece, so every line of the box is exactly l.innerW.
	row := func(style lipgloss.Style, text string) string {
		return border.Render("│") +
			style.Render(padANSI(truncateANSI(text, l.innerW), l.innerW)) +
			border.Render("│")
	}
	// fill pads a row that was composed of separately styled segments (the
	// path line with its caret) to the full inner width.
	fill := func(text string, style lipgloss.Style) string {
		if gap := l.innerW - lipgloss.Width(text); gap > 0 {
			text += style.Render(strings.Repeat(" ", gap))
		}
		return border.Render("│") + truncateANSI(text, l.innerW) + border.Render("│")
	}

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))

	titleKey := i18n.KeyPickerOpenTitle
	if m.picker.mode == pickerDir {
		titleKey = i18n.KeyPickerDirTitle
	}
	out = append(out, row(m.theme.dlgTitle, " "+m.t(titleKey)))
	out = append(out, row(box, ""))

	out = append(out, fill(m.pickerPathLine(l), m.pathLineStyle(l)))

	filter := m.t(i18n.KeyStatusFiltering, stripControl(m.picker.filter))
	filterStyle := m.theme.dlgDesc
	if m.picker.filtering {
		filterStyle = m.theme.dlgValueSel
	}
	out = append(out, row(filterStyle, " "+filter))

	msg, vis := m.pickerStatusMessage(), m.pickerVisible()
	for r := 0; r < l.listH; r++ {
		text, style := "", box
		switch {
		case r == 0 && msg != "":
			text = " " + msg
			if m.picker.err != nil {
				style = m.theme.danger
			} else {
				style = m.theme.dlgDesc
			}
		case r >= l.statusRows:
			idx := m.picker.scroll + r - l.statusRows
			if idx < len(vis) {
				e := m.picker.entries[vis[idx]]
				text = " " + m.pickerEntryLabel(e)
				// idx is the row's position in the visible list — the unit
				// sel and hover work in (vis[idx] is the entry index).
				switch {
				case idx == m.picker.sel:
					style = m.theme.dlgRowSel
				case idx == m.picker.hover:
					// Hover is drawn under the selection: when both land
					// on one row, the selection style wins.
					style = m.theme.menuHover
				default:
					style = m.theme.dlgValue
				}
			}
		}
		out = append(out, row(style, text))
	}

	hint := m.t(i18n.KeyPickerHint)
	// The suffix is drawn as plain text, so it is cleaned defensively: no
	// caller may turn a hint line into an escape sequence — and a suffix
	// that cleans away to nothing hides the hint instead of showing an
	// empty restriction.
	if suffix := stripControl(m.picker.suffix); suffix != "" {
		// The restriction must be visible: it hides files the user may be
		// looking for, so the hint line says what the list is cut down to.
		hint += "  · " + m.t(i18n.KeyPickerSuffix, suffix)
	}
	out = append(out, row(m.theme.dlgDesc, " "+hint))

	primaryStyle, cancelStyle := m.theme.toolBtnHover, m.theme.toolBtn
	if m.picker.focus == pickerFocusActions && m.picker.action == 1 {
		primaryStyle, cancelStyle = m.theme.toolBtn, m.theme.toolBtnHover
	}
	primary := primaryStyle.Render(" " + m.pickerPrimaryLabel() + " ")
	cancel := cancelStyle.Render(" " + m.t(i18n.KeyConnCancel) + " ")
	pad := strings.Repeat(" ", max(0, l.choose.X-l.x-1))
	out = append(out, row(box, pad+primary+"  "+cancel))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// pathLineStyle is the style of the path line: the focused line carries the
// selection background, the unfocused one sits in the box.
func (m Model) pathLineStyle(l pickerLayout) lipgloss.Style {
	if m.picker.focus == pickerFocusPath {
		return m.theme.dlgRowSel
	}
	return m.theme.dlgValue
}

// pickerPathLine renders the path line: a focus marker, the path scrolled
// horizontally so the caret stays visible (with the caret cell reversed
// while the line is focused), padded to the inner width.
func (m Model) pickerPathLine(l pickerLayout) string {
	// The marker occupies two columns and says whether the line has the
	// keyboard focus; the caret needs one more column while it does.
	marker := m.theme.dlgBox.Render("  ")
	if m.picker.focus == pickerFocusPath {
		marker = m.theme.dlgRowSel.Render("▸ ")
	}
	textW := max(1, l.innerW-2)
	runes := []rune(stripControl(m.picker.path))
	if m.picker.focus != pickerFocusPath {
		return marker + m.theme.dlgValue.Render(padANSI(truncateANSI(string(runes), textW), textW))
	}

	at := clamp(m.picker.caret, 0, len(runes))
	maxChars := max(1, textW-1)
	start := 0
	if at > maxChars {
		start = at - maxChars
	}
	end := min(len(runes), start+maxChars)
	visible := runes[start:end]
	at -= start
	before := m.theme.dlgValueSel.Render(string(visible[:at]))
	caret := m.theme.cursor.Render(" ")
	after := m.theme.dlgValueSel.Render(string(visible[at:]))
	// Pad the tail of the window so the composed line is exactly textW.
	if gap := textW - (lipgloss.Width(string(visible)) + 1); gap > 0 {
		after += m.theme.dlgValueSel.Render(strings.Repeat(" ", gap))
	}
	return marker + before + caret + after
}
