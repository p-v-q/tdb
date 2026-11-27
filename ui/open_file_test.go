package ui

// open_file_test.go covers the "open SQL file" entry points (round 7 §5):
// Ctrl+O and the File menu open the picker with the right start folder, the
// chosen file becomes a new tab, and every refusal (size, encoding, a file
// that vanished) reports in the status bar without leaving a tab behind.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
)

// writeSQLFile drops a SQL file into dir and returns its path.
func writeSQLFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// clearPickerFilter clears the typed list filter. The "*.sql" seed used to
// live here as text (a glob that never matched a name); the restriction is
// now the picker's suffix field, which is separate from the filter and does
// not hide the ".sql" entries the tests pick.
func clearPickerFilter(m Model) Model {
	m.picker.filter, m.picker.filtering = "", false
	return m
}

// openPickerFor picks the entry name in the picker opened by the caller and
// sends Enter through the full Update loop, so the dispatch of the result is
// exercised exactly as a key press drives it in production.
func openPickerFor(t *testing.T, m Model, name string) Model {
	t.Helper()
	if !m.picker.open {
		t.Fatal("precondition: the picker must be open")
	}
	m = clearPickerFilter(m)
	m.picker.sel = pickerIndexOf(t, m, name)
	return send(m, tea.KeyMsg{Type: tea.KeyEnter})
}

// ---- Opening ----

func TestCtrlOOpensTheFilePickerForTheActiveFile(t *testing.T) {
	dir := t.TempDir()
	m := dialogModel(t, 100, 30)
	m.fileName = filepath.Join(dir, "query.sql")

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.picker.open {
		t.Fatal("Ctrl+O must open the file picker")
	}
	if m.picker.mode != pickerFile {
		t.Fatalf("mode = %v, want the file mode", m.picker.mode)
	}
	if m.picker.filter != "" {
		t.Fatalf("filter = %q, the text filter starts empty", m.picker.filter)
	}
	if m.picker.suffix != ".sql" {
		t.Fatalf("suffix = %q, want %q (the SQL restriction moved out of the filter)",
			m.picker.suffix, ".sql")
	}
	if m.picker.dir != dir {
		t.Fatalf("dir = %q, want the folder of the active file (%q)", m.picker.dir, dir)
	}
	if m.pickerPurpose != pickerForOpenSQL {
		t.Fatalf("purpose = %v, want pickerForOpenSQL", m.pickerPurpose)
	}
	if m.menu.open != -1 {
		t.Fatalf("menu.open = %d, no menu may stay open behind the picker", m.menu.open)
	}
}

func TestCtrlOWithoutAFileStartsInTheWorkingDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	m := dialogModel(t, 100, 30) // the tab has no file yet
	if m.fileName != "" {
		t.Fatalf("precondition: fileName = %q, want empty", m.fileName)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.picker.open {
		t.Fatal("Ctrl+O must open the file picker")
	}
	if m.picker.dir != wd {
		t.Fatalf("dir = %q, want the working directory %q", m.picker.dir, wd)
	}
}

func TestMenuOpenSQLFileItemOpensTheFilePicker(t *testing.T) {
	dir := t.TempDir()
	m := dialogModel(t, 100, 30)
	m.fileName = filepath.Join(dir, "query.sql")

	// The command table is the only registry: the entry exists, lives in the
	// File menu, carries the accelerator and the shortcut, and the shortcut
	// maps to its action.
	var item menuItem
	found := false
	for _, def := range menuDefs {
		for _, it := range def.items {
			if it.labelKey != i18n.KeyMenuOpenFile {
				continue
			}
			item, found = it, true
			if def.titleKey != i18n.KeyMenuFile {
				t.Fatalf("Open SQL File… must live in the File menu, found in %v", def.titleKey)
			}
		}
	}
	if !found {
		t.Fatal("the File menu must offer \"Open SQL File…\"")
	}
	if item.accel != 'o' || item.shortcut != "ctrl+o" || item.action != actOpenFile {
		t.Fatalf("entry = %+v, want accel 'o', shortcut ctrl+o, action actOpenFile", item)
	}
	if a, ok := keymapAction("ctrl+o"); !ok || a != actOpenFile {
		t.Fatalf("keymapAction(ctrl+o) = (%v, %v), want actOpenFile", a, ok)
	}
	// The F1 help is generated from the same table, so the binding is
	// advertised there without a second registry: "Open SQL File… ^O".
	text := keybindingsText(m)
	if !strings.Contains(text, m.t(i18n.KeyMenuOpenFile)+" "+displayShortcut("ctrl+o")) {
		t.Fatalf("the key-bindings overlay must list the binding:\n%s", text)
	}

	// Driving it the way a keyboard user does: Alt+F opens the File menu,
	// ↓ moves onto Open SQL File…, Enter runs it.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}, Alt: true})
	if m.menu.open < 0 {
		t.Fatal("Alt+F must open the File menu")
	}
	items := m.menuItems(menuDefs[m.menu.open])
	idx := -1
	for i, it := range items {
		if it.labelKey == i18n.KeyMenuOpenFile {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("the visible File menu must list Open SQL File…: %+v", items)
	}
	for i := 0; i < idx; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.picker.open || m.picker.mode != pickerFile {
		t.Fatalf("open=%v mode=%v, the menu item must open the file picker",
			m.picker.open, m.picker.mode)
	}
	if m.picker.dir != dir {
		t.Fatalf("dir = %q, want %q", m.picker.dir, dir)
	}
	if m.menu.open != -1 {
		t.Fatalf("menu.open = %d, the menu must close", m.menu.open)
	}
}

// ---- The chosen file ----

func TestPickerChoiceOpensANewTabWithTheFile(t *testing.T) {
	dir := t.TempDir()
	// The tab already edits a file in that folder, so Ctrl+O starts there.
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	path := writeSQLFile(t, dir, "report.sql", "SELECT 42;\n")

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	// The tab edits a file, so it is not the welcome page: on the page the
	// file takes the tab over instead of opening beside it (covered below),
	// but here a second file must still become a second tab.
	m.welcomeToEditor()
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	tabs := len(m.tabs)
	m = openPickerFor(t, m, "report.sql")

	if m.picker.open {
		t.Fatal("picking a file must close the picker")
	}
	if len(m.tabs) != tabs+1 {
		t.Fatalf("tabs = %d, want %d (the file becomes a new tab)", len(m.tabs), tabs+1)
	}
	if m.picker.result != "" {
		t.Fatalf("result = %q, the dispatch must clear it", m.picker.result)
	}
	if m.pickerPurpose != pickerForNone {
		t.Fatalf("purpose = %v, the dispatch must clear it", m.pickerPurpose)
	}
	if m.fileName != path {
		t.Fatalf("fileName = %q, want %q so Ctrl+S writes back to it", m.fileName, path)
	}
	if got := m.tabs[m.active]; got.name != "report.sql" {
		t.Fatalf("tab name = %q, want the base name of the file", got.name)
	}
	if m.active != len(m.tabs)-1 {
		t.Fatalf("active = %d, want the new tab", m.active)
	}
	if got := m.buf.Text(); got != "SELECT 42;\n" {
		t.Fatalf("buffer = %q, want the file content", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, the new tab must be focused in the editor", m.layout.focus)
	}
}

// TestOpenSQLFileReplacesTheWelcomeTab: a file asked for on the welcome page
// takes that tab over instead of opening beside it — the page left standing
// next to the file is exactly what the user would then have to close by hand.
func TestOpenSQLFileReplacesTheWelcomeTab(t *testing.T) {
	dir := t.TempDir()
	// The active file only decides the folder the picker starts in (the page
	// itself has no file); the entry the test picks lives next to it.
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	path := writeSQLFile(t, dir, "report.sql", "SELECT 42;\n")

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	if !m.welcome {
		t.Fatal("precondition: the fixture must start on the welcome page")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	tabs := len(m.tabs)
	m = openPickerFor(t, m, "report.sql")

	if len(m.tabs) != tabs {
		t.Fatalf("tabs = %d, want %d (the file takes the welcome tab over)", len(m.tabs), tabs)
	}
	if m.welcome {
		t.Fatal("the tab must no longer be the welcome page")
	}
	if m.fileName != path {
		t.Fatalf("fileName = %q, want %q so Ctrl+S writes back to it", m.fileName, path)
	}
	if got := m.tabs[m.active].name; got != "report.sql" {
		t.Fatalf("tab name = %q, want the base name of the file", got)
	}
	if got := m.buf.Text(); got != "SELECT 42;\n" {
		t.Fatalf("buffer = %q, want the file content", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, the editor must take the keyboard", m.layout.focus)
	}
	// The page-only state must be gone with the page.
	if m.welcomeSel != 0 || m.welcomeHover != -1 {
		t.Fatalf("welcomeSel=%d welcomeHover=%d, want the page state reset",
			m.welcomeSel, m.welcomeHover)
	}
	if m.firstRow != 0 || m.scrollX != 0 || m.hasSelection() {
		t.Fatalf("firstRow=%d scrollX=%d selection=%v, the converted tab must start at the top",
			m.firstRow, m.scrollX, m.hasSelection())
	}
	if m.picker.open || m.pickerPurpose != pickerForNone || m.picker.result != "" {
		t.Fatalf("picker open=%v purpose=%v result=%q, the dispatch must clear all three",
			m.picker.open, m.pickerPurpose, m.picker.result)
	}
}

// TestOpenSQLFileRebuildsTheCompletionContextOnTheWelcomeTab: the converted
// tab keeps its session, so the completion context computed for the text
// behind the page must be rebuilt from the file — the tail newTabNamed runs
// on the fresh tab it builds. The method is called directly because the key
// that drives the picker recomputes the context anyway, which would hide a
// conversion that forgot to.
func TestOpenSQLFileRebuildsTheCompletionContextOnTheWelcomeTab(t *testing.T) {
	dir := t.TempDir()
	path := writeSQLFile(t, dir, "report.sql", "SELECT 42;\n")

	m := dialogModel(t, 100, 30)
	m.lastCtx = sql.Context{Kind: sql.CtxTable} // nothing computed this from the file

	if cmd := m.openSQLFile(path); cmd != nil {
		t.Fatalf("opening the file must not report a failure: %v", cmd())
	}
	want := sql.AnalyzeContext(m.buf.Text(), toSQLPos(m.buf.Cursor()), m.cfg.Dialect)
	if m.lastCtx.Kind != want.Kind {
		t.Fatalf("ctx kind = %v, want %v — the context must be rebuilt from the file",
			m.lastCtx.Kind, want.Kind)
	}
}

func TestPickerChoiceRefusesAFileLargerThanTheCap(t *testing.T) {
	dir := t.TempDir()
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	path := writeSQLFile(t, dir, "huge.sql", "x")
	// A sparse file: the size is over the cap, the disk usage is not.
	if err := os.Truncate(path, maxSQLFileSize+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	tabs := len(m.tabs)
	m = openPickerFor(t, m, "huge.sql")

	if len(m.tabs) != tabs {
		t.Fatalf("tabs = %d, an oversized file must not open a tab", len(m.tabs))
	}
	if !m.welcome {
		t.Fatal("the refusal must leave the welcome page up")
	}
	if m.picker.open {
		t.Fatal("the pick itself must still close the picker")
	}
	want := m.t(i18n.KeyMsgFileTooLarge, path, maxSQLFileSize>>20)
	if m.status.message != want {
		t.Fatalf("status = %q, want %q", m.status.message, want)
	}
	if m.fileName != seed {
		t.Fatalf("fileName = %q, the refusal must leave the session file alone", m.fileName)
	}
}

func TestPickerChoiceRefusesInvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	path := writeSQLFile(t, dir, "latin1.sql", "\xff\xfe SELECT 1\n")

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	tabs := len(m.tabs)
	m = openPickerFor(t, m, "latin1.sql")

	if len(m.tabs) != tabs {
		t.Fatalf("tabs = %d, invalid UTF-8 must not open a tab", len(m.tabs))
	}
	if !m.welcome {
		t.Fatal("the refusal must leave the welcome page up")
	}
	want := m.t(i18n.KeyMsgFileNotUTF8, path)
	if m.status.message != want {
		t.Fatalf("status = %q, want %q", m.status.message, want)
	}
}

// TestPickThenVanishedFileReportsOpenFailed pins the third refusal: a file
// that disappears between the pick and the read. The picker handler is
// driven directly for the pick (the component stats the entry itself), and
// the pending result is dispatched by the next event, exactly as the stale
// guard in handleKey promises.
func TestPickThenVanishedFileReportsOpenFailed(t *testing.T) {
	dir := t.TempDir()
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	path := writeSQLFile(t, dir, "gone.sql", "SELECT 1\n")

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.picker.filter != "" {
		m.picker.filter, m.picker.filtering = "", false
	}
	m.picker.sel = pickerIndexOf(t, m, "gone.sql")
	tabs := len(m.tabs)

	var handled bool
	m, _, handled = m.handleFilePickerKey("enter", nil)
	if !handled || m.picker.open {
		t.Fatalf("handled=%v open=%v, the pick must close the picker", handled, m.picker.open)
	}
	if m.picker.result != path {
		t.Fatalf("result = %q, want %q", m.picker.result, path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Any event dispatches the pending pick; the file is gone by now.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.tabs) != tabs {
		t.Fatalf("tabs = %d, a missing file must not open a tab", len(m.tabs))
	}
	want := m.t(i18n.KeyMsgFileOpenFailed, path)
	if m.status.message != want {
		t.Fatalf("status = %q, want %q", m.status.message, want)
	}
	if m.pickerPurpose != pickerForNone || m.picker.result != "" {
		t.Fatalf("purpose=%v result=%q, both must be cleared", m.pickerPurpose, m.picker.result)
	}
}

// ---- Modality ----

// TestPickerShieldsThePanesFromKeysAndPointer pins the routing the picker
// needs from the integration layer: typing goes to the filter, and no click
// or motion reaches the menu bar, the toolbar or a pane behind the box.
func TestPickerShieldsThePanesFromKeysAndPointer(t *testing.T) {
	m := dialogModel(t, 100, 30)
	before := m.buf.Text()
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})

	m = typeText(m, "zz")
	if got := m.buf.Text(); got != before {
		t.Fatalf("buffer = %q, typing must not leak into the editor", got)
	}
	if m.picker.filter != "zz" {
		t.Fatalf("filter = %q, the keystrokes belong to the picker", m.picker.filter)
	}

	r := m.paneRects()
	// Motion would hover the toolbar and the menu bar behind the box.
	m = send(m, mouseMotion(r.toolbar.X+1, r.toolbar.Y))
	m = send(m, mouseMotion(r.menubar.X+1, r.menubar.Y))
	if m.toolHover != -1 || m.menu.hover != -1 {
		t.Fatalf("toolHover=%d menu.hover=%d, hover must not reach the chrome",
			m.toolHover, m.menu.hover)
	}
	// A press on a menu title would open a menu without the modal rule.
	m = send(m, mousePress(r.menubar.X+1, r.menubar.Y))
	if m.menu.open != -1 {
		t.Fatalf("menu.open = %d, the menu must not open behind the picker", m.menu.open)
	}
	// A press in the editor body would move the caret and take the focus.
	cursor := m.buf.Cursor()
	m = send(m, mousePress(r.editor.X+3, r.editor.Y+2))
	if got := m.buf.Cursor(); got != cursor {
		t.Fatalf("cursor = %+v, want %+v (the press belongs to the picker)", got, cursor)
	}
	// A press in the results pane would hand it the keyboard focus.
	m = send(m, mousePress(r.results.X+3, r.results.Y+2))
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, the press must not reach the results pane", m.layout.focus)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.picker.open {
		t.Fatal("Esc must close the picker")
	}
	if m.pickerPurpose != pickerForNone {
		t.Fatalf("purpose = %v, cancelling must clear it", m.pickerPurpose)
	}
}

// TestPickerOnATinyScreen still opens and still closes: the frame refuses to
// draw below its nine-row minimum, but the keyboard keeps working and View
// must not panic on the empty frame.
func TestPickerOnATinyScreenStillOpensAndCloses(t *testing.T) {
	m := dialogModel(t, 40, 10)
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.picker.open {
		t.Fatal("the picker must open on a 40x10 screen")
	}
	if m.pickerLayout().ok {
		t.Fatal("the frame must refuse a screen this short")
	}
	// The point of this call is that drawing the empty frame does not panic.
	if view := m.View(); view == "" {
		t.Fatal("the model must still produce a frame")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.picker.open {
		t.Fatal("Esc must close the picker even when nothing is drawn")
	}
}

// ---- The suffix restriction ----

// hasPickerName reports whether the visible list holds an entry.
func hasPickerName(m Model, name string) bool {
	for _, n := range pickerNames(m) {
		if n == name {
			return true
		}
	}
	return false
}

// TestCtrlOListsSQLFilesImmediately: Ctrl+O used to seed the list with the
// text "*.sql", which the component's substring filter can never match, so
// the picker opened on "Nothing matches the filter". The restriction is now
// the picker's suffix: the SQL files are on screen at once, other files are
// not, and the folders stay reachable.
func TestCtrlOListsSQLFilesImmediately(t *testing.T) {
	dir := t.TempDir()
	seed := writeSQLFile(t, dir, "current.sql", "-- seed\n")
	writeSQLFile(t, dir, "query.sql", "SELECT 1\n")
	writeSQLFile(t, dir, "notes.txt", "note\n")
	scripts := filepath.Join(dir, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	m := dialogModel(t, 100, 30)
	m.fileName = seed
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.picker.open {
		t.Fatal("Ctrl+O must open the file picker")
	}
	if !hasPickerName(m, "query.sql") || !hasPickerName(m, "current.sql") {
		t.Fatalf("the SQL files must be listed right away: %v", pickerNames(m))
	}
	if hasPickerName(m, "notes.txt") {
		t.Fatalf("the suffix must hide other files at first: %v", pickerNames(m))
	}
	if !hasPickerName(m, "scripts") {
		t.Fatalf("directories must stay visible: %v", pickerNames(m))
	}

	// The restriction is said out loud, and the list is not "empty".
	view := pickerView(t, m)
	if want := m.t(i18n.KeyPickerSuffix, ".sql"); !strings.Contains(view, want) {
		t.Fatalf("the open picker must explain %q:\n%s", want, view)
	}
	if strings.Contains(view, m.t(i18n.KeyPickerNoMatch)) {
		t.Fatalf("the picker must not open on a no-match message:\n%s", view)
	}

	// ⌫ lifts the restriction (the text filter is empty): the whole folder
	// comes back, the way the old "*.sql" seed filter was cleared.
	m = pickerKey(t, m, "backspace", nil)
	if m.picker.suffix != "" {
		t.Fatalf("suffix = %q, backspace must lift it", m.picker.suffix)
	}
	if !hasPickerName(m, "notes.txt") || !hasPickerName(m, "query.sql") {
		t.Fatalf("after lifting the suffix the folder is listed whole: %v", pickerNames(m))
	}
}

// TestPickerSuffixAndFilterStack: the extension restriction and the typed
// filter are two independent narrowings. Both apply at once, each is lifted
// on its own (the filter first, the suffix after it), and the match is
// case-insensitive on both sides.
func TestPickerSuffixAndFilterStack(t *testing.T) {
	tr := newPickerTree(t)
	m := dialogModel(t, 100, 30)
	m.openFilePickerWithSuffix(pickerFile, tr.root, "", ".SQL")
	if !m.picker.open {
		t.Fatalf("the picker must open (err=%v)", m.picker.err)
	}
	// The stored suffix is lower-cased and compared case-insensitively.
	pickerWantOrder(t, pickerNames(m),
		[]string{"..", "alpha", "zeta", "query.sql"})

	// The filter narrows what the suffix already allows.
	m = pickerType(t, m, "que")
	pickerWantOrder(t, pickerNames(m), []string{"query.sql"})

	// A filter that only matches a file the suffix excludes matches nothing:
	// both restrictions are in effect.
	m = pickerType(t, m, "not") // "quenot" — no match either way
	if len(pickerNames(m)) != 0 {
		t.Fatalf("no entry may survive filter+suffix: %v", pickerNames(m))
	}
	if view := pickerView(t, m); !strings.Contains(view, m.t(i18n.KeyPickerNoMatch)) {
		t.Fatalf("an empty list must say so:\n%s", view)
	}

	// Backspace works on the filter first…
	for range len("quenot") {
		m = pickerKey(t, m, "backspace", nil)
	}
	if m.picker.filter != "" {
		t.Fatalf("filter = %q, want it cleared", m.picker.filter)
	}
	if m.picker.suffix == "" {
		t.Fatal("the suffix must survive the filter being cleared")
	}
	if hasPickerName(m, "notes.txt") {
		t.Fatalf("the suffix must still hide notes.txt: %v", pickerNames(m))
	}
	pickerWantOrder(t, pickerNames(m), []string{"..", "alpha", "zeta", "query.sql"})

	// …and on the suffix once the filter is gone.
	m = pickerKey(t, m, "backspace", nil)
	if m.picker.suffix != "" {
		t.Fatalf("suffix = %q, the last backspace must lift it", m.picker.suffix)
	}
	pickerWantOrder(t, pickerNames(m),
		[]string{"..", "alpha", "zeta", "notes.txt", "query.sql"})
}

// ---- The descriptor re-check ----

// TestVerifyOpenSQLFileRefusesTheDescriptorOfADirectory pins the rule that is
// checked on the descriptor rather than on the path: between the stat and the
// open the path may point somewhere else, and a directory is the replacement
// every platform can make (os.Open succeeds on one) — reading it must never
// start.
func TestVerifyOpenSQLFileRefusesTheDescriptorOfADirectory(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open the directory: %v", err)
	}
	defer f.Close()

	if err := verifyOpenSQLFile(f); !errors.Is(err, errSQLFileNotRegular) {
		t.Fatalf("verify = %v, want %v", err, errSQLFileNotRegular)
	}
}

// TestVerifyOpenSQLFileRefusesAFilePastTheCap pins the second rule of the
// re-check: a file that outgrew the cap between the pick and the read is
// refused on the descriptor, before a byte of it is read.
func TestVerifyOpenSQLFileRefusesAFilePastTheCap(t *testing.T) {
	path := writeSQLFile(t, t.TempDir(), "huge.sql", "x")
	// A sparse file: over the cap in size, not on disk.
	if err := os.Truncate(path, maxSQLFileSize+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if err := verifyOpenSQLFile(f); !errors.Is(err, errSQLFileTooLarge) {
		t.Fatalf("verify = %v, want %v", err, errSQLFileTooLarge)
	}
}

// TestVerifyOpenSQLFileAcceptsARegularFile pins the happy path: the re-check
// is a second opinion, not a new refusal — an ordinary pick must pass it.
func TestVerifyOpenSQLFileAcceptsARegularFile(t *testing.T) {
	path := writeSQLFile(t, t.TempDir(), "ok.sql", "SELECT 1\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if err := verifyOpenSQLFile(f); err != nil {
		t.Fatalf("verify = %v, want no error for a regular file", err)
	}
}
