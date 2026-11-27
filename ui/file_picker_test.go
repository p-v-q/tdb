package ui

// file_picker_test.go covers the built-in picker: entry order and filters,
// navigation by keyboard and pointer, the path line, the modal geometry and
// the sanitizing of file names that come from the filesystem.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/i18n"
)

// ---- Helpers ----

// pickerTree is the directory tree the picker tests browse.
type pickerTree struct {
	root, sub, deep, dotdir, zeta string
	sql, txt, dot                 string
}

func newPickerTree(t *testing.T) pickerTree {
	t.Helper()
	root := t.TempDir()
	tr := pickerTree{root: root}
	tr.sub = filepath.Join(root, "alpha")
	tr.deep = filepath.Join(tr.sub, "nested")
	tr.dotdir = filepath.Join(root, ".hidden")
	tr.zeta = filepath.Join(root, "zeta")
	for _, d := range []string{tr.sub, tr.deep, tr.dotdir, tr.zeta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	write := func(path, content string) string {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return path
	}
	tr.sql = write(filepath.Join(root, "query.sql"), "SELECT 1\n")
	tr.txt = write(filepath.Join(root, "notes.txt"), "note\n")
	tr.dot = write(filepath.Join(root, ".env"), "K=V\n")
	return tr
}

// openPicker builds a sized model and opens the picker on a folder.
func openPicker(t *testing.T, mode pickerMode, dir, filter string) Model {
	t.Helper()
	m := dialogModel(t, 100, 30)
	m.openFilePicker(mode, dir, filter)
	if !m.picker.open {
		t.Fatalf("the picker must open (dir=%q err=%v)", m.picker.dir, m.picker.err)
	}
	return m
}

// pickerKey sends one key to the open picker and asserts it was consumed.
func pickerKey(t *testing.T, m Model, key string, runes []rune) Model {
	t.Helper()
	next, _, handled := m.handleFilePickerKey(key, runes)
	if !handled {
		t.Fatalf("key %q must be consumed by the open picker", key)
	}
	return next
}

// pickerType types text into the focused line of the picker.
func pickerType(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = pickerKey(t, m, string(r), []rune{r})
	}
	return m
}

// pickerNames lists the visible entries by their file names, in list order.
func pickerNames(m Model) []string {
	vis := m.pickerVisible()
	out := make([]string, 0, len(vis))
	for _, i := range vis {
		out = append(out, m.picker.entries[i].name)
	}
	return out
}

// pickerIndexOf finds a visible entry and returns its position in the list
// (the unit sel works in, not the index into entries).
func pickerIndexOf(t *testing.T, m Model, name string) int {
	t.Helper()
	for pos, i := range m.pickerVisible() {
		if m.picker.entries[i].name == name {
			return pos
		}
	}
	t.Fatalf("entry %q is not visible; list = %v", name, pickerNames(m))
	return -1
}

// pickerView renders the picker and returns its lines joined.
func pickerView(t *testing.T, m Model) string {
	t.Helper()
	_, _, block, ok := m.renderFilePicker()
	if !ok || len(block) == 0 {
		t.Fatal("the open picker must render")
	}
	return strings.Join(block, "\n")
}

// pickerWantOrder asserts an exact list order.
func pickerWantOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("list = %v, want %v", got, want)
		}
	}
}

// ---- Entries: order, dotfiles, modes ----

func TestFilePickerOrdersParentDirsThenFiles(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	if !m.picker.entries[0].parent {
		t.Fatal(".. must be the first entry of every non-root listing")
	}
	// ".." first, directories before files, each group alphabetical; the
	// dot entries are hidden without a filter.
	pickerWantOrder(t, pickerNames(m),
		[]string{"..", "alpha", "zeta", "notes.txt", "query.sql"})

	// Directory mode lists directories only.
	m = openPicker(t, pickerDir, tr.root, "")
	pickerWantOrder(t, pickerNames(m), []string{"..", "alpha", "zeta"})
}

func TestFilePickerHidesDotfilesUnlessTheFilterStartsWithADot(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	for _, name := range pickerNames(m) {
		if name == ".env" || name == ".hidden" {
			t.Fatalf("dotfile %q must be hidden by default", name)
		}
	}

	m = openPicker(t, pickerFile, tr.root, ".")
	names := pickerNames(m)
	has := func(want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	if !has(".env") || !has(".hidden") {
		t.Fatalf("a filter starting with '.' must show dotfiles: %v", names)
	}
	if !has("query.sql") {
		t.Fatalf("the filter is a substring match, so query.sql stays: %v", names)
	}
	if has("alpha") {
		t.Fatalf("alpha does not contain a dot: %v", names)
	}
}

func TestFilePickerParentRowIsAbsentAtTheRoot(t *testing.T) {
	// The root of the file system: its parent is itself, so the picker must
	// not offer a ".." row and Backspace must not move.
	root := string(filepath.Separator)
	if vol := filepath.VolumeName(os.TempDir()); vol != "" {
		root = vol + string(filepath.Separator)
	}
	entries, err := loadPickerEntries(root)
	if err != nil {
		t.Fatalf("read the root: %v", err)
	}
	for _, e := range entries {
		if e.parent {
			t.Fatal(".. must not be listed at the root")
		}
	}

	m := openPicker(t, pickerFile, root, "")
	if m.picker.dir != root {
		t.Fatalf("dir = %q, want %q", m.picker.dir, root)
	}
	m = pickerKey(t, m, "backspace", nil)
	if m.picker.dir != root {
		t.Fatalf("backspace at the root moved to %q", m.picker.dir)
	}
}

// ---- Keyboard-only actions ----

// TestFilePickerButtonsAreReachableWithTab: a directory picker must be usable
// without a pointer. Tab reaches the button row, Enter on the primary button
// chooses the folder being shown, and Cancel closes without a choice.
func TestFilePickerButtonsAreReachableWithTab(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerDir, tr.root, "")
	m.picker.setDir(tr.sub, mustEntries(t, tr.sub))

	// list -> path -> buttons
	m = pickerKey(t, m, "tab", nil)
	m = pickerKey(t, m, "tab", nil)
	if m.picker.focus != pickerFocusActions {
		t.Fatalf("focus = %d, want the button row", m.picker.focus)
	}
	m = pickerKey(t, m, "enter", nil)
	if m.picker.open || m.picker.result != tr.sub {
		t.Fatalf("open=%v result=%q, the primary button must choose the folder", m.picker.open, m.picker.result)
	}

	// The same path with Cancel closes without a result.
	m = openPicker(t, pickerDir, tr.root, "")
	m.picker.setDir(tr.sub, mustEntries(t, tr.sub))
	m = pickerKey(t, m, "tab", nil)
	m = pickerKey(t, m, "tab", nil)
	m = pickerKey(t, m, "right", nil) // the Cancel button
	if m.picker.action != 1 {
		t.Fatalf("action = %d, right must select Cancel", m.picker.action)
	}
	m = pickerKey(t, m, "enter", nil)
	if m.picker.open {
		t.Fatal("Enter on Cancel must close the picker")
	}
	if m.picker.result != "" {
		t.Fatalf("result = %q, Cancel must not choose anything", m.picker.result)
	}
}

// mustEntries lists a folder for the fixtures above.
func mustEntries(t *testing.T, dir string) []pickerEntry {
	t.Helper()
	entries, err := loadPickerEntries(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return entries
}

// TestFilePickerEnterDescendsAndBackspaceWalksUp covers the list keys.
func TestFilePickerEnterDescendsAndBackspaceWalksUp(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	m.picker.sel = pickerIndexOf(t, m, "alpha")
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open {
		t.Fatal("enter on a directory must descend, not close the picker")
	}
	if m.picker.dir != tr.sub {
		t.Fatalf("dir = %q, want %q", m.picker.dir, tr.sub)
	}
	pickerWantOrder(t, pickerNames(m), []string{"..", "nested"})

	m = pickerKey(t, m, "backspace", nil)
	if m.picker.dir != tr.root {
		t.Fatalf("backspace went to %q, want %q", m.picker.dir, tr.root)
	}
}

func TestFilePickerMovementKeepsTheSelectionVisible(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("file%02d.sql", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m := openPicker(t, pickerFile, root, "")
	rows := m.pickerLayout().entryRows
	if len(pickerNames(m)) <= rows {
		t.Fatalf("the list (%d rows) must be longer than the viewport (%d)", len(pickerNames(m)), rows)
	}

	m = pickerKey(t, m, "pgdown", nil)
	if m.picker.sel != rows {
		t.Fatalf("pgdown sel = %d, want %d", m.picker.sel, rows)
	}
	if m.picker.scroll != 1 {
		t.Fatalf("scroll = %d, want the selection on the first row", m.picker.scroll)
	}
	m = pickerKey(t, m, "end", nil)
	last := len(pickerNames(m)) - 1
	if m.picker.sel != last || m.picker.scroll != last-rows+1 {
		t.Fatalf("end: sel=%d scroll=%d, want sel=%d at the bottom", m.picker.sel, m.picker.scroll, last)
	}
	m = pickerKey(t, m, "home", nil)
	if m.picker.sel != 0 || m.picker.scroll != 0 {
		t.Fatalf("home: sel=%d scroll=%d, want both 0", m.picker.sel, m.picker.scroll)
	}
	// ↑ below the top clamps instead of wrapping.
	m = pickerKey(t, m, "up", nil)
	if m.picker.sel != 0 {
		t.Fatalf("up from the first row moved to %d", m.picker.sel)
	}
}

func TestFilePickerFileModeEnterPicksTheFile(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	m.picker.sel = pickerIndexOf(t, m, "query.sql")
	m = pickerKey(t, m, "enter", nil)
	if m.picker.open {
		t.Fatal("enter on a file must close the picker")
	}
	if m.picker.result != tr.sql {
		t.Fatalf("result = %q, want %q", m.picker.result, tr.sql)
	}
}

func TestFilePickerEscCancelsWithoutAResult(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	m = pickerKey(t, m, "esc", nil)
	if m.picker.open {
		t.Fatal("esc must close the picker")
	}
	if m.picker.result != "" {
		t.Fatalf("result = %q, cancelling must leave no result", m.picker.result)
	}
}

func TestFilePickerDirModeEnterDescendsAndButtonPicksTheFolder(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerDir, tr.root, "")

	m.picker.sel = pickerIndexOf(t, m, "alpha")
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open {
		t.Fatal("enter on a directory must descend, even in directory mode")
	}
	if m.picker.dir != tr.sub {
		t.Fatalf("dir = %q, want %q", m.picker.dir, tr.sub)
	}
	l := m.pickerLayout()
	m, _ = m.handleFilePickerMouse(mousePress(l.choose.X, l.choose.Y))
	if m.picker.open {
		t.Fatal("the button must pick the current folder and close")
	}
	if m.picker.result != tr.sub {
		t.Fatalf("result = %q, want the folder %q", m.picker.result, tr.sub)
	}
}

func TestFilePickerFilteringNarrowsTheListAndBackspaceRestoresIt(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	m = pickerType(t, m, "que")
	pickerWantOrder(t, pickerNames(m), []string{"query.sql"})
	if !m.picker.filtering {
		t.Fatal("a non-empty filter must be reported as filtering")
	}
	view := pickerView(t, m)
	if !strings.Contains(view, "query.sql") || strings.Contains(view, "notes.txt") {
		t.Fatalf("the filtered view must show only matches:\n%s", view)
	}

	m = pickerType(t, m, "zz")
	if len(pickerNames(m)) != 0 {
		t.Fatalf("query.sqlzz must match nothing: %v", pickerNames(m))
	}
	if view = pickerView(t, m); !strings.Contains(view, m.t(i18n.KeyPickerNoMatch)) {
		t.Fatalf("an empty filter result must say so:\n%s", view)
	}

	// Backspace deletes one character at a time; the list follows it.
	m = pickerKey(t, m, "backspace", nil) // "quez"
	if len(pickerNames(m)) != 0 {
		t.Fatalf("quez must match nothing: %v", pickerNames(m))
	}
	m = pickerKey(t, m, "backspace", nil) // "que"
	pickerWantOrder(t, pickerNames(m), []string{"query.sql"})
	m = pickerKey(t, m, "backspace", nil) // "qu"
	pickerWantOrder(t, pickerNames(m), []string{"query.sql"})
	m = pickerKey(t, m, "backspace", nil) // "q"
	pickerWantOrder(t, pickerNames(m), []string{"query.sql"})
	m = pickerKey(t, m, "backspace", nil) // ""
	pickerWantOrder(t, pickerNames(m),
		[]string{"..", "alpha", "zeta", "notes.txt", "query.sql"})
	if m.picker.filtering {
		t.Fatal("an empty filter must not be reported as filtering")
	}

	// With no filter left, backspace walks to the parent folder again.
	m = pickerKey(t, m, "backspace", nil)
	if m.picker.dir == tr.root {
		t.Fatal("backspace with an empty filter must go to the parent folder")
	}
}

func TestFilePickerEmptyFolderSaysSo(t *testing.T) {
	empty := t.TempDir() // holds no entry but ".."
	m := openPicker(t, pickerFile, empty, "")
	view := pickerView(t, m)
	if !strings.Contains(view, m.t(i18n.KeyPickerEmpty)) {
		t.Fatalf("an empty folder must be named:\n%s", view)
	}
	if !strings.Contains(view, m.t(i18n.KeyPickerHint)) {
		t.Fatalf("the hint line must stay visible:\n%s", view)
	}
}

// ---- Unreadable folders ----

func TestFilePickerUnreadableFolderShowsInlineMessage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits: chmod does not deny directory access")
	}
	tr := newPickerTree(t)
	bad := filepath.Join(tr.root, "noperm")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bad, err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", bad, err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	m := openPicker(t, pickerFile, tr.root, "")
	m.pickerCd(bad)
	if m.picker.err == nil {
		t.Fatal("entering an unreadable folder must record the error")
	}
	if !m.picker.open {
		t.Fatal("an unreadable folder must not close the picker")
	}
	view := pickerView(t, m)
	if !strings.Contains(view, m.t(i18n.KeyPickerUnreadable)) {
		t.Fatalf("the failure must be shown inline:\n%s", view)
	}

	// The listing stayed where it was: navigation keeps working and the
	// error clears with the next successful folder change.
	m.picker.sel = pickerIndexOf(t, m, "alpha")
	m = pickerKey(t, m, "enter", nil)
	if m.picker.err != nil || m.picker.dir != tr.sub {
		t.Fatalf("err=%v dir=%q, the picker must recover", m.picker.err, m.picker.dir)
	}
}

func TestFilePickerOpenFallsBackWhenTheStartFolderIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits: chmod does not deny directory access")
	}
	tr := newPickerTree(t)
	bad := filepath.Join(tr.root, "noperm")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bad, err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", bad, err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	m := dialogModel(t, 100, 30)
	m.openFilePicker(pickerFile, bad, "")
	if !m.picker.open {
		t.Fatal("the picker must open even when the start folder is unreadable")
	}
	if m.picker.dir == bad {
		t.Fatalf("dir = %q, the unreadable folder must be replaced", m.picker.dir)
	}
	if m.picker.err != nil {
		t.Fatalf("the fallback must land on a readable folder: %v", m.picker.err)
	}
	if _, err := os.ReadDir(m.picker.dir); err != nil {
		t.Fatalf("the fallback folder %q is not readable: %v", m.picker.dir, err)
	}
}

// ---- The path line ----

func TestFilePickerPathLineJumpsToRelativeAbsoluteAndHomePaths(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	m = pickerKey(t, m, "tab", nil)
	if m.picker.focus != pickerFocusPath {
		t.Fatal("tab must move the focus to the path line")
	}

	// A relative path resolves against the current folder.
	m.picker.path, m.picker.caret = "alpha", len([]rune("alpha"))
	m = pickerKey(t, m, "enter", nil)
	if m.picker.dir != tr.sub {
		t.Fatalf("relative jump landed on %q, want %q", m.picker.dir, tr.sub)
	}

	// An absolute path jumps anywhere.
	m.picker.path, m.picker.caret = tr.root, len([]rune(tr.root))
	m = pickerKey(t, m, "enter", nil)
	if m.picker.dir != tr.root {
		t.Fatalf("absolute jump landed on %q, want %q", m.picker.dir, tr.root)
	}

	// "~" resolves to the user's home directory.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if _, err := os.ReadDir(home); err != nil {
		t.Skipf("the home directory is not readable: %v", err)
	}
	m.picker.path, m.picker.caret = "~", 1
	m = pickerKey(t, m, "enter", nil)
	if m.picker.dir != home {
		t.Fatalf("~ jumped to %q, want %q", m.picker.dir, home)
	}

	// In file mode a file path is picked instead of entered.
	m.picker.path, m.picker.caret = tr.sql, len([]rune(tr.sql))
	m = pickerKey(t, m, "enter", nil)
	if m.picker.open || m.picker.result != tr.sql {
		t.Fatalf("open=%v result=%q, a file path must pick the file", m.picker.open, m.picker.result)
	}
}

func TestFilePickerPathLineEditsAndTheListKeepsTheFilter(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")

	m = pickerKey(t, m, "tab", nil)
	m = pickerType(t, m, "z")
	if want := tr.root + "z"; m.picker.path != want {
		t.Fatalf("path = %q, want %q", m.picker.path, want)
	}
	if m.picker.filter != "" {
		t.Fatalf("filter = %q, typing on the path line must not filter", m.picker.filter)
	}

	// A path that does not exist reports inline and keeps the picker open.
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open || m.picker.err == nil {
		t.Fatalf("open=%v err=%v, a missing path must report inline", m.picker.open, m.picker.err)
	}
	if view := pickerView(t, m); !strings.Contains(view, m.t(i18n.KeyPickerUnreadable)) {
		t.Fatalf("the failure must be visible:\n%s", view)
	}

	// Backspace edits the path; Enter on the restored path reads fine.
	m = pickerKey(t, m, "backspace", nil)
	if m.picker.path != tr.root {
		t.Fatalf("path = %q, want %q", m.picker.path, tr.root)
	}
	m = pickerKey(t, m, "enter", nil)
	if m.picker.err != nil {
		t.Fatalf("the restored path must be readable: %v", m.picker.err)
	}

	// Tab walks to the button row and then back to the list, where typing
	// filters again.
	m = pickerKey(t, m, "tab", nil)
	if m.picker.focus != pickerFocusActions {
		t.Fatal("tab must reach the button row")
	}
	m = pickerKey(t, m, "tab", nil)
	if m.picker.focus != pickerFocusList {
		t.Fatal("tab must move the focus back to the list")
	}
	m = pickerType(t, m, "z")
	if m.picker.filter != "z" || m.picker.path != tr.root {
		t.Fatalf("filter=%q path=%q, typing must filter now", m.picker.filter, m.picker.path)
	}
	pickerWantOrder(t, pickerNames(m), []string{"zeta"})
}

func TestFilePickerLongPathKeepsTheCaretVisible(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	m.picker.focus = pickerFocusPath
	// Longer than the visible window of the path line, so only the window
	// around the caret can show the file name.
	m.picker.path = strings.Repeat("segment/", 12) + filepath.Base(tr.sql)
	m.picker.caret = len([]rune(m.picker.path))
	// The focused line slides its window so the caret and the tail of the
	// path stay on screen instead of the head.
	if view := pickerView(t, m); !strings.Contains(view, filepath.Base(tr.sql)) {
		t.Fatalf("the end of a long path must stay on screen:\n%s", view)
	}
}

// ---- Mouse ----

func TestFilePickerClickHoverWheelAndCancel(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folder, "keep.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("file%02d.sql", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	m := openPicker(t, pickerFile, root, "")
	l := m.pickerLayout()

	// A click on a row picks it and activates it, like Enter: the folder
	// below the cursor is entered.
	m, _ = m.handleFilePickerMouse(mousePress(l.x+2, l.listY+1))
	if !m.picker.open || m.picker.dir != folder {
		t.Fatalf("open=%v dir=%q, a click on a folder must enter it", m.picker.open, m.picker.dir)
	}
	m = pickerKey(t, m, "backspace", nil)
	l = m.pickerLayout()

	// Clicking the path line focuses it; clicking the filter line hands the
	// keyboard back to the list.
	m, _ = m.handleFilePickerMouse(mousePress(l.x+2, l.pathY))
	if m.picker.focus != pickerFocusPath {
		t.Fatalf("focus = %d, the path line must take the click", m.picker.focus)
	}
	m, _ = m.handleFilePickerMouse(mousePress(l.x+2, l.filterY))
	if m.picker.focus != pickerFocusList {
		t.Fatalf("focus = %d, the filter line must return to the list", m.picker.focus)
	}

	// Hover highlights the row under the pointer and never moves the
	// selection.
	m, _ = m.handleFilePickerMouse(mouseMotion(l.x+2, l.listY+3))
	if m.picker.hover != 3 {
		t.Fatalf("hover = %d, want row 3", m.picker.hover)
	}
	if m.picker.sel != 0 {
		t.Fatalf("hover moved the selection to %d", m.picker.sel)
	}

	// The wheel scrolls: the selection moves, and the list follows it.
	for range 5 {
		m, _ = m.handleFilePickerMouse(mouseWheel(true, l.x+2, l.listY+3))
	}
	if m.picker.scroll == 0 {
		t.Fatalf("scroll = 0, the wheel must scroll the long list (sel=%d)", m.picker.sel)
	}
	if m.picker.sel != 15 {
		t.Fatalf("sel = %d, want 15 after five wheel steps", m.picker.sel)
	}
	// Hover after the scroll still only reports the pointer.
	m, _ = m.handleFilePickerMouse(mouseMotion(l.x+2, l.listY+2))
	if want := m.picker.scroll + 2; m.picker.hover != want {
		t.Fatalf("hover = %d, want %d", m.picker.hover, want)
	}
	if m.picker.sel != 15 {
		t.Fatalf("hover changed the selection to %d", m.picker.sel)
	}

	// A click outside the dialog is swallowed: the picker is modal.
	m, _ = m.handleFilePickerMouse(mousePress(0, 0))
	if !m.picker.open {
		t.Fatal("a click outside the picker must not close it")
	}

	// The Cancel button closes without a result.
	m, _ = m.handleFilePickerMouse(mousePress(l.cancel.X, l.cancel.Y))
	if m.picker.open || m.picker.result != "" {
		t.Fatalf("open=%v result=%q, cancel must close without a result", m.picker.open, m.picker.result)
	}
}

// ---- Geometry ----

func TestFilePickerStaysInsideTheScreenAndButtonsMatchTheirRects(t *testing.T) {
	tr := newPickerTree(t)
	for _, size := range []struct{ w, h int }{{40, 80}, {120, 40}} {
		m := dialogModel(t, size.w, size.h)
		m.openFilePicker(pickerDir, tr.root, "")
		area := m.paneRects().area
		x, y, block, ok := m.renderFilePicker()
		if !ok {
			t.Fatalf("%dx%d: the picker did not render", size.w, size.h)
		}
		blockInsideRect(t, fmt.Sprintf("picker %dx%d", size.w, size.h), x, y, block, area)

		// The button row is drawn from the same rectangles the mouse uses.
		l := m.pickerLayout()
		line := stripANSI(block[l.h-2]) // block[0] is the top border
		content := strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│")
		pad := strings.Repeat(" ", l.choose.X-l.x-1)
		primary, cancel := m.pickerPrimaryLabel(), m.t(i18n.KeyConnCancel)
		want := pad + " " + primary + " " + "  " + " " + cancel + " "
		if !strings.HasPrefix(content, want) {
			t.Fatalf("%dx%d: button row = %q, want prefix %q", size.w, size.h, content, want)
		}
		if l.choose.W != lipgloss.Width(" "+primary+" ") {
			t.Fatalf("choose width = %d, want %d", l.choose.W, lipgloss.Width(" "+primary+" "))
		}
		if got := strings.Index(content, primary); got != l.choose.X-l.x {
			t.Fatalf("%dx%d: primary button drawn at column %d, its rect says %d",
				size.w, size.h, got, l.choose.X-l.x)
		}
		if got := strings.Index(content, cancel); got != l.cancel.X-l.x {
			t.Fatalf("%dx%d: cancel button drawn at column %d, its rect says %d",
				size.w, size.h, got, l.cancel.X-l.x)
		}
	}
}

// The frame needs nine rows (borders, title, blank, path, filter, one list
// row, hint, buttons). On a screen shorter than that the box must refuse to
// draw itself: the buttons used to be painted one row below the row their
// hit rectangle named, so a press on a list row submitted the current
// folder in directory mode and the drawn buttons were unclickable.
func TestFilePickerDoesNotRenderWhenTheScreenIsTooShortForTheFrame(t *testing.T) {
	tr := newPickerTree(t)
	// area.H is the screen minus the three chrome rows: 7 and 8 here, both
	// under pickerMinH.
	for _, size := range []struct{ w, h int }{{40, 10}, {40, 11}} {
		m := dialogModel(t, size.w, size.h)
		m.openFilePicker(pickerDir, tr.root, "")
		if area := m.paneRects().area; area.H != size.h-3 {
			t.Fatalf("%dx%d: area.H = %d, want the screen minus three chrome rows",
				size.w, size.h, area.H)
		}

		l := m.pickerLayout()
		if l.ok {
			t.Fatalf("%dx%d: h=%d is below the %d-row frame, the layout must refuse it",
				size.w, size.h, l.h, pickerMinH)
		}
		if x, y, block, ok := m.renderFilePicker(); ok || x != 0 || y != 0 || len(block) != 0 {
			t.Fatalf("%dx%d: render = (%d,%d,%v,%d lines), want no block at all",
				size.w, size.h, x, y, ok, len(block))
		}

		// Nothing on screen means nothing to click: every press lands
		// outside the box and is swallowed without touching the listing.
		dir := m.picker.dir
		for _, msg := range []tea.MouseMsg{
			mouseMotion(l.x+2, l.listY+1),
			mousePress(l.x+2, l.listY+1),       // a list row: used to activate an entry
			mousePress(l.x+3, l.y+7),           // where the buttons used to be painted
			mousePress(l.choose.X, l.choose.Y), // the empty button rectangles
			mouseWheel(true, l.x+2, l.listY+2),
		} {
			m, _ = m.handleFilePickerMouse(msg)
		}
		if !m.picker.open || m.picker.result != "" || m.picker.dir != dir {
			t.Fatalf("%dx%d: open=%v result=%q dir=%q (was %q), a press must change nothing",
				size.w, size.h, m.picker.open, m.picker.result, m.picker.dir, dir)
		}

		// The keyboard keeps working on the model: no key panics and none
		// of them closes the picker before Esc does.
		m = pickerKey(t, m, "down", nil)
		m = pickerKey(t, m, "enter", nil)
		m = pickerType(t, m, "q")
		if !m.picker.open || m.picker.result != "" {
			t.Fatalf("%dx%d: open=%v result=%q, the keys must stay inside the modal",
				size.w, size.h, m.picker.open, m.picker.result)
		}
		m = pickerKey(t, m, "esc", nil)
		if m.picker.open {
			t.Fatalf("%dx%d: esc must still close the picker", size.w, size.h)
		}
	}
}

// The other half of the contract: while the frame fits, the block holds
// exactly l.h lines and the buttons are painted in the row and the columns
// their hit rectangles name — on the shortest screen that still fits (nine
// rows, one list row) as well as on a roomy one.
func TestFilePickerBlockHeightMatchesTheLayoutAndButtonRects(t *testing.T) {
	tr := newPickerTree(t)
	for _, size := range []struct{ w, h int }{{40, 12}, {40, 80}, {120, 40}} {
		m := dialogModel(t, size.w, size.h)
		m.openFilePicker(pickerDir, tr.root, "")
		l := m.pickerLayout()
		if !l.ok {
			t.Fatalf("%dx%d: the frame must fit here (h=%d >= %d)",
				size.w, size.h, l.h, pickerMinH)
		}
		_, _, block, ok := m.renderFilePicker()
		if !ok {
			t.Fatalf("%dx%d: the picker did not render", size.w, size.h)
		}
		if len(block) != l.h {
			t.Fatalf("%dx%d: block has %d lines, the layout says h=%d",
				size.w, size.h, len(block), l.h)
		}

		// Both buttons share the row one above the bottom border.
		if l.choose.Y != l.y+l.h-2 || l.cancel.Y != l.choose.Y {
			t.Fatalf("%dx%d: button rects at y=%d/%d, the drawn row is %d",
				size.w, size.h, l.choose.Y, l.cancel.Y, l.y+l.h-2)
		}
		line := stripANSI(block[l.choose.Y-l.y])
		content := strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│")
		primary, cancel := m.pickerPrimaryLabel(), m.t(i18n.KeyConnCancel)
		if got := strings.Index(content, primary); got != l.choose.X-l.x {
			t.Fatalf("%dx%d: primary button drawn at column %d, its rect says %d",
				size.w, size.h, got, l.choose.X-l.x)
		}
		if got := strings.Index(content, cancel); got != l.cancel.X-l.x {
			t.Fatalf("%dx%d: cancel button drawn at column %d, its rect says %d",
				size.w, size.h, got, l.cancel.X-l.x)
		}
		// The list the clicks map to lies inside the drawn block: it ends
		// where the hint row begins, above the buttons.
		if end := l.listY + l.listH; end > l.y+l.h-2 {
			t.Fatalf("%dx%d: list ends at row %d, past the hint/buttons frame row %d",
				size.w, size.h, end, l.y+l.h-2)
		}
	}
}

func TestFilePickerHoverRowIsDrawnWithTheHoverStyle(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	row := pickerIndexOf(t, m, "query.sql")

	// Lip Gloss prints no ANSI without a TTY; force colors so the styles
	// can be told apart, like the menu tests do.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	plain := pickerView(t, m)
	m.picker.hover = row
	m.picker.sel = 0
	hovered := pickerView(t, m)
	if plain == hovered {
		t.Fatalf("the hovered row must be drawn differently:\n%s", hovered)
	}
	if m.picker.sel != 0 {
		t.Fatalf("setting the hover must not move the selection (sel=%d)", m.picker.sel)
	}
	// The selection wins when both land on one row: hovering the selected
	// row changes nothing.
	m.picker.sel = row
	selected := pickerView(t, m) // hover is still on the same row
	m.picker.hover = -1
	again := pickerView(t, m)
	if selected != again {
		t.Fatalf("a hovered row that is also selected must show the selection style:\n%s", selected)
	}
}

// ---- File names from the filesystem ----

func TestFilePickerStripsControlCharactersFromNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows forbids control characters in file names, so the fixture cannot be created")
	}
	dir := t.TempDir()
	raw := "\x1b[31mred.sql"
	if err := os.WriteFile(filepath.Join(dir, raw), []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	m := openPicker(t, pickerFile, dir, "")
	view := pickerView(t, m)
	if strings.Contains(view, "\x1b[31m") {
		t.Fatalf("the escape sequence must not reach the screen:\n%s", view)
	}
	if !strings.Contains(view, "red.sql") {
		t.Fatalf("the name must stay readable:\n%s", view)
	}

	// Sanitizing is a display rule: the choice keeps the real file name so
	// the caller can open the file that exists.
	m.picker.sel = pickerIndexOf(t, m, raw)
	m = pickerKey(t, m, "enter", nil)
	if want := filepath.Join(dir, raw); m.picker.result != want {
		t.Fatalf("result = %q, want %q", m.picker.result, want)
	}
}

// ---- Selection and status after the list changes ----

// Typing narrows the list, so a selection (and a hover) that pointed past
// the new end must be brought back into range instead of left dangling on
// a row that is no longer drawn.
func TestFilePickerClampsSelectionAndDropsHoverWhenTheFilterShortensTheList(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	last := len(m.pickerVisible()) - 1
	if last < 1 {
		t.Fatalf("the listing needs at least two rows to test the clamp: %v", pickerNames(m))
	}
	m.picker.sel = last
	m.picker.hover = last // a row the filter is about to remove

	m = pickerType(t, m, "z")
	pickerWantOrder(t, pickerNames(m), []string{"zeta"})
	if m.picker.sel != 0 {
		t.Fatalf("sel = %d after the list shrank to one row, want 0", m.picker.sel)
	}
	if m.picker.hover != -1 {
		t.Fatalf("hover = %d, the stale index must be dropped", m.picker.hover)
	}

	// The clamped selection is a real row: Enter descends into it.
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open || m.picker.dir != tr.zeta {
		t.Fatalf("open=%v dir=%q, enter must act on the clamped row (%q)",
			m.picker.open, m.picker.dir, tr.zeta)
	}
}

// Enter acts on a highlighted row; with no row at all it must be a no-op
// rather than a panic or a picker that closes itself with no result.
func TestFilePickerEnterOnAnEmptyListClosesNothing(t *testing.T) {
	// Directory mode on a folder that holds no directory: the list shows
	// nothing beyond the parent row, and Enter walks up at most.
	empty := t.TempDir()
	m := openPicker(t, pickerDir, empty, "")
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open || m.picker.result != "" {
		t.Fatalf("open=%v result=%q, enter on a listing without entries must stay open",
			m.picker.open, m.picker.result)
	}

	// A filter without a match leaves no visible row at all — not even a
	// selection index carried over from before the filter is a problem.
	tr := newPickerTree(t)
	m = openPicker(t, pickerFile, tr.root, "")
	m = pickerType(t, m, "no-such-entry")
	if len(pickerNames(m)) != 0 {
		t.Fatalf("no-such-entry must match nothing: %v", pickerNames(m))
	}
	m.picker.sel = 99
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open || m.picker.result != "" {
		t.Fatalf("open=%v result=%q, enter on an empty visible list must stay open",
			m.picker.open, m.picker.result)
	}
	if view := pickerView(t, m); !strings.Contains(view, m.t(i18n.KeyPickerNoMatch)) {
		t.Fatalf("the empty filter result must be named:\n%s", view)
	}
}

// A folder whose only entries are files is not empty; the directory mode
// hides the files, so it has nothing to pick — which is the no-match
// wording, not "this folder is empty".
func TestFilePickerDirModeWithOnlyFilesDoesNotCallTheFolderEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "query.sql"), []byte("SELECT 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	m := openPicker(t, pickerDir, dir, "")
	view := pickerView(t, m)
	if !strings.Contains(view, m.t(i18n.KeyPickerNoMatch)) {
		t.Fatalf("a folder of only files must not offer anything to pick:\n%s", view)
	}
	if strings.Contains(view, m.t(i18n.KeyPickerEmpty)) {
		t.Fatalf("a folder with files must not be called empty:\n%s", view)
	}

	// A folder that really holds nothing keeps the empty wording.
	m = openPicker(t, pickerDir, t.TempDir(), "")
	if view = pickerView(t, m); !strings.Contains(view, m.t(i18n.KeyPickerEmpty)) {
		t.Fatalf("an empty folder must be named:\n%s", view)
	}
}

// ---- The path line: delete and the empty jump ----

func TestFilePickerPathLineDeleteWorksAtTheEndAndInTheMiddle(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	m = pickerKey(t, m, "tab", nil)
	runes := []rune(tr.root)

	// At the end of the line there is nothing under the caret: delete is
	// a no-op instead of dropping the last rune.
	m.picker.caret = len(runes)
	m = pickerKey(t, m, "delete", nil)
	if m.picker.path != tr.root {
		t.Fatalf("path = %q at the end of the line, want it unchanged", m.picker.path)
	}
	if m.picker.caret != len(runes) {
		t.Fatalf("caret = %d, want it at the end", m.picker.caret)
	}

	// In the middle it drops exactly the rune under the caret.
	m.picker.caret = 1
	m = pickerKey(t, m, "delete", nil)
	if want := string(runes[:1]) + string(runes[2:]); m.picker.path != want {
		t.Fatalf("path = %q, want %q", m.picker.path, want)
	}
	if m.picker.caret != 1 {
		t.Fatalf("caret = %d, want it left where the rune was removed", m.picker.caret)
	}

	// The shortened path does not exist: Enter reports it inline and the
	// picker stays open.
	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open || m.picker.err == nil {
		t.Fatalf("open=%v err=%v, a missing path must report inline", m.picker.open, m.picker.err)
	}
}

// An empty path line has nothing to jump to; it reports inline instead of
// panicking on the empty string or closing the dialog.
func TestFilePickerJumpOnAnEmptyPathLineReportsInline(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	m = pickerKey(t, m, "tab", nil)
	m.picker.path, m.picker.caret = "", 0

	m = pickerKey(t, m, "enter", nil)
	if !m.picker.open {
		t.Fatal("an empty path line must not close the picker")
	}
	if m.picker.err == nil {
		t.Fatal("an empty path line must report inline")
	}
	if m.picker.dir != tr.root {
		t.Fatalf("dir = %q, the folder must not move", m.picker.dir)
	}
	if m.picker.result != "" {
		t.Fatalf("result = %q, nothing may be picked", m.picker.result)
	}
}

// ---- The result across openings ----

// The integration reuses one model, so a choice from a previous opening must
// never survive into the next one: reopening clears it, cancelling keeps it
// cleared.
func TestFilePickerResultDoesNotSurviveAReopening(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	m.picker.sel = pickerIndexOf(t, m, "query.sql")
	m = pickerKey(t, m, "enter", nil)
	if m.picker.result != tr.sql {
		t.Fatalf("result = %q, want %q", m.picker.result, tr.sql)
	}

	m.openFilePicker(pickerFile, tr.root, "")
	if !m.picker.open {
		t.Fatal("the picker must reopen")
	}
	if m.picker.result != "" {
		t.Fatalf("result = %q, reopening must clear the previous choice", m.picker.result)
	}
	m = pickerKey(t, m, "esc", nil)
	if m.picker.result != "" {
		t.Fatalf("result = %q, cancelling must leave no result", m.picker.result)
	}
}

// Every other modal collapses the status-bar find prompt while it is open,
// so keys typed after it closes do not land in the find input.
func TestFilePickerOpenCollapsesTheFindPrompt(t *testing.T) {
	tr := newPickerTree(t)
	m := dialogModel(t, 100, 30)
	m.find.open = true

	m.openFilePicker(pickerFile, tr.root, "")
	if m.find.open {
		t.Fatal("opening the picker must collapse the find prompt, like the other modals")
	}
	if !m.picker.open {
		t.Fatal("the picker must open")
	}
}

// ---- The folder mark of the icon sets ----

// Directories are marked with the folder glyph of the active set; only the
// set without one falls back to a trailing "/". The schema glyph keeps its
// own meaning (the object browser) and is never the directory mark.
func TestFilePickerFolderIconFollowsTheActiveIconSet(t *testing.T) {
	tr := newPickerTree(t)
	m := openPicker(t, pickerFile, tr.root, "")
	dir := pickerEntry{name: "alpha", dir: true}

	// The factory settings ship with the nerd set on; the plain set is one
	// switch away, so both are checked through the label itself.
	m.settings.Icons = false
	if got := plainIcons().folder; got != "" {
		t.Fatalf("plain folder = %q, the plain set must have no folder glyph", got)
	}
	if got := m.pickerEntryLabel(dir); got != "alpha/" {
		t.Fatalf("plain label = %q, want %q", got, "alpha/")
	}

	if got := nerdIcons().folder; got != "\uf07b" {
		t.Fatalf("nerd folder = %q, want the folder glyph", got)
	}
	m.settings.Icons = true
	if got := m.pickerEntryLabel(dir); got != "\uf07b alpha" {
		t.Fatalf("nerd label = %q, want the folder glyph before the name", got)
	}
	// The mark belongs to directories only.
	if got := m.pickerEntryLabel(pickerEntry{name: "query.sql"}); got != "query.sql" {
		t.Fatalf("file label = %q, files are marked by nothing", got)
	}
}

// TestPickerSuffixHintDropsTheEscapeSequences pins the defensive cleaning of
// the hint line: the suffix is drawn as plain text, so no caller may turn it
// into an escape sequence — and a suffix that cleans away to nothing hides
// the hint instead of announcing an empty restriction.
func TestPickerSuffixHintDropsTheEscapeSequences(t *testing.T) {
	m := dialogModel(t, 100, 30)
	m.openFilePickerWithSuffix(pickerFile, t.TempDir(), "", ".sql"+seqOSC52)
	if !m.picker.open {
		t.Fatal("the picker must open")
	}
	view := pickerView(t, m)
	if want := m.t(i18n.KeyPickerSuffix, ".sql]52;c;aaaa"); !strings.Contains(stripANSI(view), want) {
		t.Fatalf("the hint must say the cleaned suffix %q:\n%s", want, view)
	}
	assertNoPayload(t, view, "the picker suffix")
	assertNoPayload(t, m.View(), "the picker suffix")

	// Nothing but control characters: the restriction disappears with them,
	// rather than becoming "Showing  files" with no file name behind it.
	m.openFilePickerWithSuffix(pickerFile, t.TempDir(), "", seqOSC52)
	view = pickerView(t, m)
	if strings.Contains(stripANSI(view), m.t(i18n.KeyPickerSuffix, "")) {
		t.Fatalf("an emptied suffix must not be announced as a restriction:\n%s", view)
	}
	assertNoPayload(t, view, "the emptied picker suffix")
}
