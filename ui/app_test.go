package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/textbuf"
)

// newTestModel builds a model with the sample catalog already injected. The
// language is pinned to English so assertions do not depend on the developer's
// environment, and the zero Settings value makes New fall back to the factory
// preferences (mouse on, gutter on, default layout).
func TestMain(m *testing.M) {
	// Status messages expire on a timer; tests must not wait for it.
	statusTimeout = time.Millisecond
	os.Exit(m.Run())
}

func newTestModel(text string) Model {
	d := dialect.MustGet("postgres")
	m := New(Config{
		Title:          "tdb",
		ConnName:       "demo",
		FileName:       "",
		Dialect:        d,
		Engine:         complete.NewDefaultEngine(d),
		Provider:       catalog.Static{C: catalog.DemoCatalog()},
		Text:           text,
		Translator:     i18n.New(i18n.En),
		InitialCatalog: catalog.DemoCatalog(),
	})
	// Most tests exercise the tree below the connection; production starts
	// folded, which has its own test. Schemas are folded in production too, so
	// open them explicitly for tests that work with table rows.
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	for _, s := range m.cat.Schemas {
		m.objectsSetOpen(objectsRow{kind: rowSchema, schema: s.Name}, true)
		for _, g := range m.objectGroups(s) {
			m.objectsSetOpen(objectsRow{kind: rowGroup, schema: s.Name, group: g.kind}, true)
		}
	}
	return m
}

func send(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func typeText(m Model, text string) Model {
	for _, r := range text {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func keyByName(m Model, name string) Model {
	switch name {
	case "enter":
		return send(m, tea.KeyMsg{Type: tea.KeyEnter})
	case "tab":
		return send(m, tea.KeyMsg{Type: tea.KeyTab})
	case "space":
		return send(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	case "esc":
		return send(m, tea.KeyMsg{Type: tea.KeyEsc})
	case "backspace":
		return send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	case "delete":
		return send(m, tea.KeyMsg{Type: tea.KeyDelete})
	case "up":
		return send(m, tea.KeyMsg{Type: tea.KeyUp})
	case "down":
		return send(m, tea.KeyMsg{Type: tea.KeyDown})
	case "left":
		return send(m, tea.KeyMsg{Type: tea.KeyLeft})
	case "right":
		return send(m, tea.KeyMsg{Type: tea.KeyRight})
	case "home":
		return send(m, tea.KeyMsg{Type: tea.KeyHome})
	case "end":
		return send(m, tea.KeyMsg{Type: tea.KeyEnd})
	case "pgup":
		return send(m, tea.KeyMsg{Type: tea.KeyPgUp})
	case "pgdown":
		return send(m, tea.KeyMsg{Type: tea.KeyPgDown})
	case "ctrl+space":
		return send(m, tea.KeyMsg{Type: tea.KeyCtrlAt})
	case "f2":
		return send(m, tea.KeyMsg{Type: tea.KeyF2})
	case "f10":
		return send(m, tea.KeyMsg{Type: tea.KeyF10})
	default:
		panic("unsupported key: " + name)
	}
}

func TestRenderShowsHeaderAndStatus(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	view := m.View()
	if !strings.Contains(view, "TDB") {
		t.Fatalf("view is missing the app name:\n%s", view)
	}
	if !strings.Contains(view, "conn demo (postgres)") {
		t.Fatalf("view is missing the connection info:\n%s", view)
	}
	if !strings.Contains(view, "Ln 1, Col 1") {
		t.Fatalf("view is missing the caret position:\n%s", view)
	}
}

func TestSyntaxHighlightApplied(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := newTestModel("SELECT * FROM users")
	// The fixture starts on the welcome page (no file is open); this test is
	// about the editor drawing the statement behind it.
	m.welcomeToEditor()
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m.buf.MoveBufferEnd() // keep the caret from splitting the first keyword
	view := m.View()
	// SELECT must be drawn in the keyword style (blue), not bare text.
	if !strings.Contains(view, m.theme.keyword.Render("SELECT")) {
		t.Fatalf("SELECT must use the keyword highlight style:\n%s", view)
	}
}

func TestAutoCompletionOnTyping(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "SELECT * FROM us")
	if !m.popup.open {
		t.Fatal("typing an identifier must open the completion popup")
	}
	if len(m.popup.items) == 0 || m.popup.items[0].Label != "users" {
		t.Fatalf("candidates = %+v", m.popup.items)
	}
	if !strings.Contains(m.View(), "complete") {
		t.Fatalf("view must contain the completion popup:\n%s", m.View())
	}
}

func TestAcceptCompletionReplacesPrefix(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "SELECT * FROM us")
	m = keyByName(m, "tab")
	if got := m.buf.Text(); got != "SELECT * FROM users" {
		t.Fatalf("text = %q", got)
	}
	if m.popup.open {
		t.Fatal("accepting a candidate must close the popup")
	}
}

// TestAcceptCompletionLeavesCaretAtEnd guards the caret position after a
// completion is accepted: it must sit at the end of the inserted word, so that
// what is typed next lands after it rather than before it.
func TestAcceptCompletionLeavesCaretAtEnd(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "SELECT * FROM us")
	m = keyByName(m, "tab")

	want := textbuf.Pos{Row: 0, Col: len([]rune("SELECT * FROM users"))}
	if c := m.buf.Cursor(); c != want {
		t.Fatalf("caret = %+v, want %+v (the end of the inserted word)", c, want)
	}
	m = typeText(m, "x")
	if got := m.buf.Text(); got != "SELECT * FROM usersx" {
		t.Fatalf("typing after a completion must append it: %q", got)
	}
}

func TestCompletionNavigation(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "SELECT ") // an empty prefix does not auto-open
	m = keyByName(m, "ctrl+space")
	if !m.popup.open {
		t.Fatal("ctrl+space must force the popup open")
	}
	first := m.popup.items[0].Label
	m = keyByName(m, "down")
	if m.popup.selected != 1 {
		t.Fatalf("selected = %d", m.popup.selected)
	}
	m = keyByName(m, "up")
	if m.popup.items[m.popup.selected].Label != first {
		t.Fatal("moving up must return to the first item")
	}
	m = keyByName(m, "esc")
	if m.popup.open {
		t.Fatal("esc must close the popup")
	}
}

func TestQualifiedCompletionAfterDot(t *testing.T) {
	m := newTestModel("SELECT u.id FROM users u")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	// Move the caret right after "u." (column 9).
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 9})
	m = typeText(m, "e")
	if !m.popup.open {
		t.Fatal("typing u.e must offer column completion")
	}
	var inserts []string
	for _, c := range m.popup.items {
		inserts = append(inserts, c.Insert)
	}
	if len(inserts) == 0 || inserts[0] != "u.email" {
		t.Fatalf("candidate inserts = %v", inserts)
	}
}

func TestBackspaceClosesCompletionWhenPrefixEmpty(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "SELECT us")
	if !m.popup.open {
		t.Fatal("the popup should be open")
	}
	m = keyByName(m, "backspace")
	m = keyByName(m, "backspace") // deleting "s" and "u" leaves the prefix empty
	if m.popup.open {
		t.Fatal("an empty prefix must close the popup")
	}
}

func TestUndoRedoThroughUI(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = typeText(m, "abc")
	before := m.buf.Text()
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	// Consecutive typing forms a single undo group.
	if m.buf.Text() != "" {
		t.Fatalf("after undo = %q", m.buf.Text())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlY})
	if m.buf.Text() != before {
		t.Fatalf("after redo = %q", m.buf.Text())
	}
}

func TestSpaceKeyIsInserted(t *testing.T) {
	// In Bubble Tea the space key has Type KeySpace, so Runes is the reliable
	// signal; this is a regression test for treating it as plain input.
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = send(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if got := m.buf.Text(); got != " " {
		t.Fatalf("text = %q, want a single space", got)
	}
}

func TestNewlineKeepsIndent(t *testing.T) {
	m := newTestModel("    SELECT 1")
	// The fixture starts on the welcome page, where Enter activates an action
	// row; this test is about the editor's auto-indent.
	m.welcomeToEditor()
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m.buf.MoveLineEnd()
	m = keyByName(m, "enter")
	if got := m.buf.LineString(1); got != "    " {
		t.Fatalf("indentation of the new line = %q", got)
	}
}

func TestCatalogLoadedMessage(t *testing.T) {
	d := dialect.MustGet("postgres")
	// No initial snapshot, so the asynchronous load path runs.
	m := New(Config{
		ConnName:   "demo",
		Dialect:    d,
		Provider:   catalog.Static{C: catalog.DemoCatalog()},
		Translator: i18n.New(i18n.En),
	})
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("without an initial snapshot Init must return a load command")
	}
	msg := cmd()
	catMsg, ok := msg.(catalogMsg)
	if !ok || catMsg.err != nil {
		t.Fatalf("msg = %+v", msg)
	}
	m = send(m, catMsg)
	if m.cat == nil {
		t.Fatal("the metadata should be loaded")
	}
	if !strings.Contains(m.status.message, "Metadata loaded") {
		t.Fatalf("status = %q", m.status.message)
	}
}

func TestStatusBarShowsContext(t *testing.T) {
	m := newTestModel("SELECT * FROM users WHERE ")
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 20})
	m.buf.MoveBufferEnd()
	m.updateContext()
	if m.lastCtx.Kind != sql.CtxColumn {
		t.Fatalf("ctx kind = %v", m.lastCtx.Kind)
	}
	if view := m.View(); !strings.Contains(view, "context column") {
		t.Fatalf("the status bar must show the context:\n%s", view)
	}
}

func TestDefaultLanguageIsEnglish(t *testing.T) {
	// A model built without an explicit translator still follows the system
	// locale, but the factory default must be English.
	if i18n.Default != i18n.En {
		t.Fatalf("i18n.Default = %q, want %q", i18n.Default, i18n.En)
	}
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	view := m.View()
	for _, want := range []string{"SQL Editor", "Objects", "Results"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the English UI is missing %q:\n%s", want, view)
		}
	}
}

func TestResolveLocale(t *testing.T) {
	cases := []struct {
		pref string
		want i18n.Locale
	}{
		{"", i18n.En},
		{"zh-Hans", i18n.ZhHans},
		{"zh_CN.UTF-8", i18n.ZhHans},
		{"en_US", i18n.En},
		{"fr_FR", i18n.En}, // unsupported: fall back to the given locale
	}
	for _, c := range cases {
		if got := resolveLocale(c.pref, i18n.En); got != c.want {
			t.Errorf("resolveLocale(%q) = %q, want %q", c.pref, got, c.want)
		}
	}
}

// TestMenuShortcutsAreDispatched pins the central invariant of the menu bar:
// every shortcut a menu advertises must be the shortcut that actually works.
func TestMenuShortcutsAreDispatched(t *testing.T) {
	seen := map[string]menuAction{}
	for _, def := range menuDefs {
		for _, it := range def.items {
			for _, shortcut := range []string{it.shortcut, it.shortcut2} {
				if shortcut == "" {
					continue
				}
				if prev, dup := seen[shortcut]; dup {
					t.Fatalf("shortcut %q is bound twice (%v and %v)", shortcut, prev, it.action)
				}
				seen[shortcut] = it.action
				got, ok := keymapAction(shortcut)
				if !ok || got != it.action {
					t.Fatalf("keymapAction(%q) = (%v, %v), want %v", shortcut, got, ok, it.action)
				}
			}
		}
	}
}

// TestMenuAcceleratorsAreUnique keeps Alt+letter opening the right menu and the
// letter accelerators inside a dropdown unambiguous.
func TestMenuAcceleratorsAreUnique(t *testing.T) {
	seen := map[rune]bool{}
	for _, def := range menuDefs {
		if seen[def.accel] {
			t.Fatalf("menu accelerator %q is used twice", def.accel)
		}
		seen[def.accel] = true
		items := map[rune]bool{}
		for _, it := range def.items {
			if items[it.accel] {
				t.Fatalf("item accelerator %q is used twice in one menu", it.accel)
			}
			items[it.accel] = true
		}
	}
}

// TestMenuDismissDoesNotType pins two behaviours: a printable key that matches
// no accelerator closes the menu without being typed into the document, while a
// key combination still falls through to the global key map.
func TestMenuDismissDoesNotType(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m = keyByName(m, "f10") // File menu
	m = typeText(m, "z")    // no item has "z" as its accelerator
	if m.menu.open != -1 {
		t.Fatal("a key with no accelerator must close the menu")
	}
	if m.buf.Text() != "" {
		t.Fatalf("the key that closed the menu must not reach the buffer: %q", m.buf.Text())
	}

	m = keyByName(m, "f10")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.layout.objectsVisible {
		t.Fatal("ctrl+b must still work while a menu is open")
	}
	if m.menu.open != -1 {
		t.Fatal("the menu must be closed afterwards")
	}
}

func TestSettingsPersistToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	d := dialect.MustGet("postgres")
	m := New(Config{
		Dialect:        d,
		Translator:     i18n.New(i18n.En),
		InitialCatalog: catalog.DemoCatalog(),
		Settings:       settings.Default(),
		SettingsPath:   path,
	})
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})

	// Ctrl+B hides the objects pane and must be written through to the file.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.layout.objectsVisible {
		t.Fatal("ctrl+b must hide the objects pane")
	}
	cmd := m.persistLayout()
	if cmd == nil {
		t.Fatal("persistLayout must return a command when a path is set")
	}
	cmd()

	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Layout.ObjectsVisible {
		t.Fatalf("the hidden objects pane was not persisted: %+v", got.Layout)
	}
	if got.Layout.ObjectsWidth != m.layout.objectsWidth {
		t.Fatalf("ObjectsWidth = %d, want %d", got.Layout.ObjectsWidth, m.layout.objectsWidth)
	}
}

func TestNoSettingsPathSkipsPersistence(t *testing.T) {
	m := newTestModel("SELECT 1")
	if cmd := m.persistSettings(); cmd != nil {
		t.Fatal("without a settings path there must be nothing to persist")
	}
}

func TestWriteFileContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "query.sql")
	m := newTestModel("SELECT 1")
	m.fileName = path // the file belongs to the tab, not to the whole app
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	cmd := m.saveFile()
	if cmd == nil {
		t.Fatal("saveFile must return a command")
	}
	if msg := cmd(); msg != nil {
		if s, ok := msg.(statusMessageMsg); !ok || !strings.Contains(string(s), "Saved") {
			t.Fatalf("unexpected save message: %#v", msg)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "SELECT 1" {
		t.Fatalf("file contents = %q", data)
	}
}

func TestFormatSQLFormatsWholeBufferAndSelection(t *testing.T) {
	m := sized("select a,b from t")
	m.formatSQL()
	want := "SELECT\n  a,\n  b\nFROM t"
	if got := m.buf.Text(); got != want {
		t.Fatalf("whole buffer = %q, want %q", got, want)
	}

	m = sized("select a,b from t -- keep")
	m.sel.anchor = &textbuf.Pos{}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: len("select a,b")})
	m.formatSQL()
	got := m.buf.Text()
	if !strings.HasPrefix(got, "SELECT\n  a,\n  b") || !strings.Contains(got, "from t -- keep") {
		t.Fatalf("selection format = %q", got)
	}
}
