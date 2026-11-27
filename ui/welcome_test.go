package ui

// welcome_test.go covers the welcome tab (docs/ux-round7.md, §2): when it is
// created, how it renders, how it is steered with the keyboard and the
// pointer, and how it turns into a normal editor tab the moment the user
// starts writing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"
)

// welcomeRowY maps an action-row index onto the screen row of the page, or -1
// when the layout does not draw that row: the pane title row comes first, then
// the body row the shared layout gives the action.
func welcomeRowY(m Model, idx int) int {
	l, items := m.welcomeGeometry()
	row := l.firstItem + idx - l.scroll
	if l.firstItem < 0 || idx < 0 || idx >= len(items) ||
		row < l.firstItem || row >= l.firstItem+l.slots {
		return -1
	}
	return m.paneRects().editor.Y + 1 + row
}

// ---- Appearance ----

func TestNewStartsOnTheWelcomeTab(t *testing.T) {
	// Nothing open: the first tab is the welcome page, and it owns an empty
	// buffer so the generic tab machinery works on it untouched.
	m := New(Config{Title: "TDB", ConnName: "demo", Translator: i18n.New(i18n.En)})
	if len(m.tabs) != 1 || !m.welcome {
		t.Fatalf("tabs = %d, welcome = %v, want a single welcome tab", len(m.tabs), m.welcome)
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, the welcome page owns an empty one", got)
	}
	if got := m.tabLabel(0); got != m.t(i18n.KeyWelcomeTab) {
		t.Fatalf("tab label = %q, want %q", got, m.t(i18n.KeyWelcomeTab))
	}

	// A file on the command line: the session starts in the editor.
	withFile := New(Config{FileName: "queries/report.sql", Translator: i18n.New(i18n.En)})
	if withFile.welcome {
		t.Fatal("a session opened on a file must not start on the welcome page")
	}

	// Initial SQL does not change that: the page stands in front of the text
	// (the binary starts a file-less session with a starter template, which is
	// exactly the onboarding the page replaces), and nothing is thrown away —
	// converting the tab shows the very text it was built with.
	withText := New(Config{Text: "SELECT 1", Translator: i18n.New(i18n.En)})
	if !withText.welcome {
		t.Fatal("a session without a file must start on the welcome page")
	}
	if got := withText.buf.Text(); got != "SELECT 1" {
		t.Fatalf("buffer = %q, the page must not lose the text it was given", got)
	}
	withText.welcomeToEditor()
	if got := withText.buf.Text(); got != "SELECT 1" {
		t.Fatalf("buffer = %q, converting the tab must keep the text", got)
	}
}

func TestClosingTheLastTabReturnsToTheWelcomePage(t *testing.T) {
	// An ordinary tab: closing the last one falls back to the welcome page
	// instead of exiting (quitting is Ctrl+Q's job). The fixture starts on
	// the welcome page itself, so it is turned into an editor tab first.
	m := sized("SELECT 1")
	m.welcomeToEditor()
	if m.welcome {
		t.Fatal("the fixture must be an ordinary editor tab")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m.tabs) != 1 || !m.tabs[0].welcome {
		t.Fatalf("tabs = %d, welcome = %v, want the welcome page back",
			len(m.tabs), m.tabs[0].welcome)
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, the welcome page owns an empty one", got)
	}

	// Typing converted it into an editor tab; closing that one as well still
	// comes back to the welcome page.
	m = typeText(m, "x")
	if m.welcome {
		t.Fatal("typing must turn the welcome page into an editor tab")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if !m.welcome {
		t.Fatal("closing the last tab must open the welcome page again")
	}
}

// ---- Keyboard ----

func TestWelcomeNavigationAndActivation(t *testing.T) {
	m := sized("")
	items := m.welcomeItems()
	if m.welcomeSel != 0 {
		t.Fatalf("welcomeSel = %d, want 0 (New query is the default row)", m.welcomeSel)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.welcomeSel != 1 {
		t.Fatalf("welcomeSel = %d, want 1 after ↓", m.welcomeSel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.welcomeSel != 2 {
		t.Fatalf("welcomeSel = %d, want 2 after Tab", m.welcomeSel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.welcomeSel != 1 {
		t.Fatalf("welcomeSel = %d, want 1 after ↑", m.welcomeSel)
	}

	// The list does not wrap around.
	m.welcomeSel = 0
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.welcomeSel != 0 {
		t.Fatalf("welcomeSel = %d, want 0 (the first row does not wrap)", m.welcomeSel)
	}
	m.welcomeSel = len(items) - 1
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.welcomeSel != len(items)-1 {
		t.Fatalf("welcomeSel = %d, want %d (the last row does not wrap)",
			m.welcomeSel, len(items)-1)
	}

	// Enter on the first row runs "New query": this very tab becomes the
	// editor, so no welcome page is left behind.
	m.welcomeSel = 0
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.welcome {
		t.Fatal("New query must turn the tab into an editor tab")
	}
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1 — the tab is converted, not copied", len(m.tabs))
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, want an empty editor", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
}

func TestWelcomeAcceleratorOpensSettings(t *testing.T) {
	m := sized("")
	m = typeText(m, "s") // an accelerator, not text
	if !m.dlg.open {
		t.Fatal(`the accelerator "s" must open the settings dialog`)
	}
	if !m.welcome {
		t.Fatal("an accelerator must not turn the page into an editor tab")
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, an accelerator types nothing", got)
	}
}

func TestWelcomeAcceleratorOpensTheFilePicker(t *testing.T) {
	m := sized("")
	m = typeText(m, "o")
	if !m.picker.open {
		t.Fatal(`the accelerator "o" must open the file picker`)
	}
	if m.pickerPurpose != pickerForOpenSQL {
		t.Fatalf("purpose = %v, want the open-SQL-file pick", m.pickerPurpose)
	}
	if !m.welcome {
		t.Fatal("the page stays up while the picker is open")
	}
}

func TestWelcomeNewQueryAccelerator(t *testing.T) {
	m := sized("")
	m = typeText(m, "n")
	if m.welcome {
		t.Fatalf(`the accelerator "n" must turn the tab into an editor tab`)
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, an accelerator types nothing", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
}

func TestWelcomeTypingTurnsThePageIntoAnEditor(t *testing.T) {
	m := sized("")
	m = typeText(m, "x")
	if m.welcome {
		t.Fatal("a printable character must turn the page into an editor tab")
	}
	if got := m.buf.Text(); got != "x" {
		t.Fatalf("buffer = %q, want %q — the key that converted the tab is typed", got, "x")
	}
	if c := m.buf.Cursor(); c.Row != 0 || c.Col != 1 {
		t.Fatalf("cursor = %+v, want the caret after the typed character", c)
	}

	// The tab is an ordinary one from here on: everything else types as usual.
	m = typeText(m, "ELECT 1")
	if got := m.buf.Text(); got != "xELECT 1" {
		t.Fatalf("buffer = %q, want the editor to keep the text", got)
	}
}

func TestWelcomeF1OpensTheKeyBindingsOverlay(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.keysDlg.open {
		t.Fatal("F1 must open the key-bindings overlay")
	}
	if !m.welcome {
		t.Fatal("the page stays up behind the overlay")
	}
}

func TestWelcomeEsc(t *testing.T) {
	// Alone on the screen the page stays: closing the workspace is Ctrl+Q's.
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.welcome || len(m.tabs) != 1 || m.quitting {
		t.Fatalf("tabs = %d, welcome = %v, quitting = %v — Esc alone must do nothing",
			len(m.tabs), m.welcome, m.quitting)
	}
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, want an untouched page", got)
	}

	// With another tab open, Esc closes the welcome page and leaves it.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(m.tabs))
	}
	m.switchTab(0)
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1 — Esc must close the welcome page", len(m.tabs))
	}
	if m.welcome {
		t.Fatal("the editor tab is what must be left")
	}
}

func TestWelcomeGlobalKeysKeepTheirMeaning(t *testing.T) {
	// Ctrl+O is resolved by the key map long before the page sees a key.
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !m.picker.open {
		t.Fatal("Ctrl+O must open the file picker on the welcome page")
	}
	if !m.welcome {
		t.Fatal("the page stays up behind the picker")
	}

	// Ctrl+T opens an ordinary tab next to the welcome page.
	m = sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(m.tabs))
	}
	if m.welcome {
		t.Fatal("Ctrl+T must land on a plain editor tab")
	}

	// Ctrl+Q still quits, without a panic on the way out.
	m = sized("")
	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlQ})
	if !next.quitting {
		t.Fatal("Ctrl+Q must quit from the welcome page")
	}
	if cmd == nil {
		t.Fatal("Ctrl+Q must return the quit command")
	}
}

// ---- Rendering ----

func TestWelcomeFitsANarrowWindow(t *testing.T) {
	// 40x24 and 40x80 are the narrow windows of the acceptance; 100x20 is the
	// window the fixtures use, where the body is only six rows tall.
	for _, size := range []struct{ w, h int }{{40, 24}, {40, 80}, {100, 20}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m := sized("")
			m = send(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})

			view := m.View()
			for i, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got != size.w {
					t.Fatalf("line %d is %d columns wide, want %d:\n%s", i, got, size.w, view)
				}
			}

			plain := stripANSI(view)
			// Title, orientation, the first actions and the hint: every part
			// of the page must be there. The lead and the hint are longer
			// than a narrow window, so they are asserted by their beginnings.
			for _, want := range []string{
				m.t(i18n.KeyWelcomeTitle),
				m.t(i18n.KeyWelcomeTab),
				head(m.t(i18n.KeyWelcomeLead), 12),
				head(m.t(i18n.KeyWelcomeNewQuery), 10),
				head(m.t(i18n.KeyMenuOpenFile), 10),
				head(m.t(i18n.KeyWelcomeNoConns), 10),
				head(m.t(i18n.KeyWelcomeHint), 8),
			} {
				if !strings.Contains(plain, want) {
					t.Fatalf("the welcome page is missing %q:\n%s", want, plain)
				}
			}

			// A body too short for the whole list scrolls with the selection,
			// so the last actions are drawn once they are selected.
			for range m.welcomeItems() {
				m = send(m, tea.KeyMsg{Type: tea.KeyDown})
			}
			plain = stripANSI(m.View())
			for _, want := range []string{
				head(m.t(i18n.KeyWelcomeSettings), 8),
				head(m.t(i18n.KeyWelcomeKeys), 8),
			} {
				if !strings.Contains(plain, want) {
					t.Fatalf("the scrolled page is missing %q:\n%s", want, plain)
				}
			}
		})
	}
}

// head returns the first n runes of s: the assertions use it for the strings a
// narrow window truncates.
func head(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func TestWelcomeRendersInSimplifiedChinese(t *testing.T) {
	m := New(Config{
		Title:      "TDB",
		Translator: i18n.New(i18n.ZhHans),
	})
	if !m.welcome {
		t.Fatal("a session with nothing open must start on the welcome page")
	}
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})

	plain := stripANSI(m.View())
	for _, key := range []i18n.Key{
		i18n.KeyWelcomeTitle, i18n.KeyWelcomeLead, i18n.KeyWelcomeNewQuery,
		i18n.KeyMenuOpenFile, i18n.KeyWelcomeSettings, i18n.KeyWelcomeKeys,
		i18n.KeyWelcomeNoConns, i18n.KeyWelcomeHint,
	} {
		// Head of every string: a long line may be shortened to the pane.
		want := head(m.t(key), 8)
		if !strings.Contains(plain, want) {
			t.Fatalf("the welcome page is missing %q:\n%s", want, plain)
		}
	}
}

func TestWelcomeLockedVaultReplacesTheConnections(t *testing.T) {
	s := settings.Default()
	s.Vault = &settings.Vault{} // sealed, passphrase never proven
	s.Connections = nil

	m := New(Config{
		Title:      "TDB",
		ConnName:   "demo",
		Translator: i18n.New(i18n.En),
		Settings:   s,
	})
	m.closeVault() // the startup unlock dialog is not what this test is about
	if !m.settings.Locked() {
		t.Fatal("the settings must be locked for this test")
	}

	items := m.welcomeItems()
	var notice *welcomeItem
	for i := range items {
		if items[i].kind == welcomeConnection {
			t.Fatalf("a locked vault must show no connection row, got %q", items[i].label)
		}
		if items[i].kind == welcomeNotice {
			it := items[i]
			notice = &it
		}
	}
	if notice == nil {
		t.Fatal("the locked page must carry a notice row")
	}
	if notice.label != m.t(i18n.KeyVaultLockedTree) {
		t.Fatalf("notice = %q, want %q", notice.label, m.t(i18n.KeyVaultLockedTree))
	}
	// The notice is not an action.
	if cmd := m.welcomeActivate(indexOfItem(items, welcomeNotice)); cmd != nil {
		t.Fatal("the locked notice must not activate anything")
	}

	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	if plain := stripANSI(m.View()); !strings.Contains(plain, m.t(i18n.KeyVaultLockedTree)) {
		t.Fatalf("the rendered page is missing %q:\n%s", m.t(i18n.KeyVaultLockedTree), plain)
	}
}

func TestWelcomeListsTheSavedConnections(t *testing.T) {
	m := sized("")
	m.settings.Connections = []settings.Connection{
		{Name: "analytics", Driver: "postgres"},
		{Name: "shop.db", Driver: "sqlite"},
	}

	items := m.welcomeItems()
	var conns []welcomeItem
	for _, it := range items {
		if it.kind == welcomeConnection {
			conns = append(conns, it)
		}
	}
	if len(conns) != 2 {
		t.Fatalf("connection rows = %d, want 2", len(conns))
	}
	if conns[0].label != "analytics (postgres)" {
		t.Fatalf("first row = %q, want the name and the driver", conns[0].label)
	}
	if conns[1].label != "shop.db (sqlite)" {
		t.Fatalf("second row = %q, want the name and the driver", conns[1].label)
	}
	if conns[0].conn != 0 || conns[1].conn != 1 {
		t.Fatalf("connection indices = %d/%d, want 0/1", conns[0].conn, conns[1].conn)
	}

	// With connections there is no "none saved" line.
	for _, it := range items {
		if it.label == m.t(i18n.KeyWelcomeNoConns) {
			t.Fatal("a saved connection list must not report that there are none")
		}
	}

	// Activating a row hands the saved connection to the same entry point the
	// object browser uses, which opens it in the background.
	idx := indexOfItem(items, welcomeConnection)
	if cmd := m.welcomeActivate(idx); cmd == nil {
		t.Fatal("a connection row must return the connect command")
	}
}

// indexOfItem returns the position of the first item of a kind.
func indexOfItem(items []welcomeItem, kind welcomeKind) int {
	for i, it := range items {
		if it.kind == kind {
			return i
		}
	}
	return -1
}

func TestWelcomeEnterActivatesTheSelectedRow(t *testing.T) {
	m := sized("")
	idx := indexOfItem(m.welcomeItems(), welcomeSettings)
	if idx < 0 {
		t.Fatal("the page must offer a settings row")
	}
	for range idx {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.dlg.open {
		t.Fatal("Enter on the settings row must open the settings dialog")
	}
	if !m.welcome {
		t.Fatal("an action must not convert the page")
	}
}

// TestWelcomeScrollsTheActionsWhenTheWindowIsShort pins that every action
// stays reachable when the pane cannot draw the whole list: the selection
// scrolls the rows instead of pointing at one that is off screen.
func TestWelcomeScrollsTheActionsWhenTheWindowIsShort(t *testing.T) {
	m := sized("")
	for i := 0; i < 10; i++ {
		m.settings.Connections = append(m.settings.Connections,
			settings.Connection{Name: fmt.Sprintf("conn-%02d", i), Driver: "postgres"})
	}
	// 100x20 leaves the editor body six rows: title plus five slots at best,
	// while the list has fourteen rows.
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	l, items := m.welcomeGeometry()
	if l.slots >= len(items) {
		t.Fatalf("slots = %d, list = %d, the window must clip the list for this test",
			l.slots, len(items))
	}

	for i := 1; i < len(items); i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
		l, _ := m.welcomeGeometry()
		row := m.welcomeSel - l.scroll
		if row < 0 || row >= l.slots {
			t.Fatalf("selection %d drawn at slot %d/%d — the list must scroll with it",
				m.welcomeSel, row, l.slots)
		}
	}
	if m.welcomeSel != len(items)-1 {
		t.Fatalf("welcomeSel = %d, want %d", m.welcomeSel, len(items)-1)
	}
}

// ---- Pointer ----

func TestWelcomeHoverAndClick(t *testing.T) {
	m := sized("")
	r := m.paneRects().editor
	idx := 1 // "Open SQL File…"
	x, y := r.X+3, welcomeRowY(m, idx)
	if y < 0 {
		t.Fatal("the second action row must be on screen in the fixture window")
	}

	// Hovering highlights the row under the pointer and never moves the row
	// Enter acts on (round 7 §1).
	m = send(m, mouseHover(x, y))
	if m.welcomeHover != idx {
		t.Fatalf("welcomeHover = %d, want %d (the row under the pointer)", m.welcomeHover, idx)
	}
	if m.welcomeSel != 0 {
		t.Fatalf("welcomeSel = %d, want 0 — hover must not move the selection", m.welcomeSel)
	}

	// Moving off the list clears the highlight only.
	m = send(m, mouseHover(x, r.Y)) // the pane title row
	if m.welcomeHover != -1 {
		t.Fatalf("welcomeHover = %d, want -1 off the rows", m.welcomeHover)
	}
	if m.welcomeSel != 0 {
		t.Fatalf("welcomeSel = %d, want 0 — leaving the list changes nothing", m.welcomeSel)
	}

	// A click picks the row (the click moves the selection, not the pointer)
	// and runs it.
	m = send(m, mouseHover(x, y))
	m = send(m, mousePress(x, y))
	if m.welcomeSel != idx {
		t.Fatalf("welcomeSel = %d, want %d — a click picks its row", m.welcomeSel, idx)
	}
	if !m.picker.open || m.pickerPurpose != pickerForOpenSQL {
		t.Fatalf("picker = %v, purpose = %v, want the open-SQL-file pick",
			m.picker.open, m.pickerPurpose)
	}
	if !m.welcome {
		t.Fatal("the page stays up behind the picker")
	}
}

func TestWelcomeBodyClickTurnsItIntoAnEditor(t *testing.T) {
	m := sized("")
	r := m.paneRects().editor

	// A click on the empty body below the list is where the caret goes: the
	// page becomes an editor tab first, then the click places it.
	m = send(m, mousePress(r.X+3, r.Y+1))
	m = send(m, mouseRelease(r.X+3, r.Y+1))
	if m.welcome {
		t.Fatal("a click in the body must turn the page into an editor tab")
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
	if m.hasSelection() {
		t.Fatal("a single click must not leave a selection")
	}

	// From here it behaves like any other editor tab.
	m = typeText(m, "SELECT 1")
	if got := m.buf.Text(); got != "SELECT 1" {
		t.Fatalf("buffer = %q, want the typed statement", got)
	}
}

// ---- Actions that act on the text behind the page ----
// The page is only a layer drawn in front of the buffer, so every action that
// reads or rewrites that buffer reveals the editor first (revealEditor) and
// then runs unchanged: what the action operates on is what is on screen. Each
// test below pins one entry point — the shortcut, the menu, the toolbar, the
// object tree and the completion popup — while the page's own rows
// (welcomeKey/welcomeActivate) are covered by the tests above and never
// reveal anything.

// TestWelcomeRunRevealsTheEditorAndRunsTheStatement: F5 is resolved by the key
// map before the page sees a key, so the query used to run against the hidden
// starter SQL with the editor still drawing the welcome page.
func TestWelcomeRunRevealsTheEditorAndRunsTheStatement(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users ORDER BY id")
	if !m.welcome {
		t.Fatal("precondition: the fixture must start on the welcome page")
	}

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.welcome {
		t.Fatal("F5 must turn the page into an editor tab before it runs")
	}
	if m.session.result.err != nil {
		t.Fatalf("the statement behind the page must run: %v", m.session.result.err)
	}
	if got := len(m.session.result.res.Rows); got != 3 {
		t.Fatalf("rows = %d, want 3 — the query really executed", got)
	}
	plain := stripANSI(strings.Join(m.renderEditor(), "\n"))
	if !strings.Contains(plain, "SELECT id FROM users") {
		t.Fatalf("the executed statement must be visible in the editor:\n%s", plain)
	}
}

// TestWelcomeExplainRevealsTheEditor: F6 (actExplain) is the twin of Run and
// takes the same route through the key map.
func TestWelcomeExplainRevealsTheEditor(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users")

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF6})

	if m.welcome {
		t.Fatal("F6 must turn the page into an editor tab before it runs")
	}
	if m.session.result.err != nil {
		t.Fatalf("EXPLAIN behind the page must run: %v", m.session.result.err)
	}
	if len(m.session.result.res.Columns) == 0 {
		t.Fatal("EXPLAIN must produce a row set")
	}
	plain := stripANSI(strings.Join(m.renderEditor(), "\n"))
	if !strings.Contains(plain, "SELECT id FROM users") {
		t.Fatalf("the explained statement must be visible in the editor:\n%s", plain)
	}
}

// TestWelcomeToggleCommentRevealsTheEditor: Ctrl+/ (actToggleComment)
// rewrites the text, so the text it rewrites has to be on screen.
func TestWelcomeToggleCommentRevealsTheEditor(t *testing.T) {
	m := sized("select 1")
	// Ctrl+/ arrives as Ctrl+_ in most terminals; the key alias maps it.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlUnderscore})

	if m.welcome {
		t.Fatal("commenting must reveal the text it comments")
	}
	if got, want := m.buf.Text(), "-- select 1"; got != want {
		t.Fatalf("text = %q, want %q — the comment toggle ran on the visible line", got, want)
	}
}

// TestWelcomeFormatRevealsTheEditor: Format has no global shortcut, so the
// test goes the way a user does — open the Edit menu and press its mnemonic.
func TestWelcomeFormatRevealsTheEditor(t *testing.T) {
	m := sized("select a,b from t")

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e"), Alt: true})
	if m.menu.open != 2 {
		t.Fatalf("menu.open = %d, want the Edit menu (2)", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})

	if m.welcome {
		t.Fatal("formatting must reveal the text it formats")
	}
	if got, want := m.buf.Text(), "SELECT\n  a,\n  b\nFROM t"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// TestWelcomeFindRevealsTheEditor: the find prompt searches the buffer and
// moves a selection in it, so it opens on text the user can see.
func TestWelcomeFindRevealsTheEditor(t *testing.T) {
	m := sized("select 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})

	if m.welcome {
		t.Fatal("Ctrl+F must turn the page into an editor tab")
	}
	if !m.find.open {
		t.Fatal("the find prompt must open on the revealed text")
	}
	if got := m.find.matches; got != 0 {
		t.Fatalf("matches = %d, the prompt starts with an empty query", got)
	}
}

// TestWelcomeSelectAllRevealsTheEditor: the selection is buffer state the
// editor draws; selecting behind the page would show nothing.
func TestWelcomeSelectAllRevealsTheEditor(t *testing.T) {
	m := sized("select 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})

	if m.welcome {
		t.Fatal("Ctrl+A must turn the page into an editor tab")
	}
	if !m.hasSelection() {
		t.Fatal("select-all must select the buffer")
	}
	if got := m.selectedText(); got != "select 1" {
		t.Fatalf("selection = %q, want the whole buffer", got)
	}
}

// TestWelcomeSaveRevealsTheEditorBeforeWriting: a tab can carry a file name
// while the page stands in front of its text, and Save would write exactly
// that hidden text to disk.
func TestWelcomeSaveRevealsTheEditorBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.sql")
	m := sized("select 1")
	m.fileName = path

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.welcome {
		t.Fatal("saving must reveal the text it writes")
	}
	if cmd == nil {
		t.Fatal("saving a named tab must return the write command")
	}
	cmd() // the status message; the file is what this test is about

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "select 1" {
		t.Fatalf("file = %q, want the text that was on screen when it was written", data)
	}
}

// TestWelcomeObjectDoubleClickOpensData: a double click on a table name from
// the welcome page opens the relation's data, and the tab that opens is a
// normal editor tab — the page steps aside because the data tab takes the focus.
func TestWelcomeObjectDoubleClickOpensData(t *testing.T) {
	m := sized("SELECT 1")
	m.buf.MoveBufferEnd()
	idx, _ := findObjectRow(t, m, rowTable, "users")
	r := m.paneRects()
	x, y := r.objects.X+20, objectRowY(m, idx)

	// Press/release, press/release: the second release is the double click
	// that accepts the row.
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))

	if m.welcome {
		t.Fatal("opening an object's data must leave the welcome page")
	}
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want the starter tab and the data tab", len(m.tabs))
	}
	if got := m.buf.Text(); !strings.Contains(got, `SELECT * FROM "public"."users" LIMIT 200;`) {
		t.Fatalf("text = %q, want the bounded SELECT", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
}

// TestWelcomeCompletionRevealsTheEditorAndAcceptsVisibleText: Ctrl+Space used
// to arm the popup over the page — render.go never draws it there, but the
// hit test still accepted a click into the hidden buffer. The page now steps
// aside before completion runs, so the popup sits on the editor it completes
// and a click inserts into the text on screen.
func TestWelcomeCompletionRevealsTheEditorAndAcceptsVisibleText(t *testing.T) {
	m := sized("SELECT * FROM us")
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: len([]rune("SELECT * FROM us"))})

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlAt}) // Ctrl+Space, Bubble Tea's name for NUL

	if m.welcome {
		t.Fatal("Ctrl+Space must turn the page into an editor tab first")
	}
	if !m.popup.open {
		t.Fatal("the popup must open on the revealed editor")
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "complete") {
		t.Fatalf("the popup must be drawn over the visible editor:\n%s", plain)
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "SELECT * FROM us") {
		t.Fatalf("the text completion works on must be visible:\n%s", plain)
	}

	x, y, _, _, ok := m.popupGeometry()
	if !ok {
		t.Fatal("the popup geometry must be available")
	}
	m = send(m, mousePress(x+1, y+1)) // the first candidate row, below the header

	if m.welcome {
		t.Fatal("accepting a candidate must not bring the page back")
	}
	if got, want := m.buf.Text(), "SELECT * FROM users"; got != want {
		t.Fatalf("text = %q, want %q — the click inserts into the visible text", got, want)
	}
}

// TestWelcomeToolbarRunRevealsTheEditorAndRuns: the toolbar dispatches through
// runMenuAction like F5 does, so its Run (and Save, and Format) reveal the
// page the same way.
func TestWelcomeToolbarRunRevealsTheEditorAndRuns(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users")
	slot, ok := toolSlotAt(m, "Run")
	if !ok {
		t.Fatal("no Run button on the toolbar")
	}

	m = runToCompletion(m, mousePress(slot.x+1, m.paneRects().toolbar.Y))

	if m.welcome {
		t.Fatal("the Run button must turn the page into an editor tab before it runs")
	}
	if m.session.result.err != nil {
		t.Fatalf("the statement behind the page must run: %v", m.session.result.err)
	}
	if got := len(m.session.result.res.Rows); got != 3 {
		t.Fatalf("rows = %d, want 3 — the query really executed", got)
	}
}

// TestWelcomeAcceleratorsWinOverTheFocusedResultsGrid: n/o/s are the page's
// accelerator rows, and the grid claims the same letters while it has the
// focus ("s" sorts the column under the caret). While the page is up it owns
// the tab, so its rows are resolved first — the grid's one-line inputs (cell
// edit, filter) keep every key, accelerators included.
func TestWelcomeAcceleratorsWinOverTheFocusedResultsGrid(t *testing.T) {
	t.Run("s opens the settings dialog", func(t *testing.T) {
		m := sized("")
		// A result behind the page — the state a run through the hidden text
		// used to leave behind. If the grid stole the key it would sort this
		// instead of opening the dialog.
		m.session.result.setResult(&driver.Result{Columns: []driver.ResultColumn{{Name: "id"}}}, nil, "SELECT 1")
		m.layout.focus = paneResults

		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})

		if !m.dlg.open {
			t.Fatal(`the accelerator "s" must open the settings dialog`)
		}
		if m.session.result.sortCol != -1 {
			t.Fatalf("sortCol = %d, want -1 — the grid must not sort on the page's key", m.session.result.sortCol)
		}
		if !m.welcome {
			t.Fatal("the page stays up behind the dialog")
		}
		if got := m.buf.Text(); got != "" {
			t.Fatalf("buffer = %q, an accelerator types nothing", got)
		}
	})

	t.Run("n becomes the editor", func(t *testing.T) {
		m := sized("")
		m.layout.focus = paneResults
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		if m.welcome {
			t.Fatal(`the accelerator "n" must turn the tab into an editor tab`)
		}
		if m.layout.focus != paneEditor {
			t.Fatalf("focus = %v, want the editor", m.layout.focus)
		}
		if got := m.buf.Text(); got != "" {
			t.Fatalf("buffer = %q, an accelerator types nothing", got)
		}
	})

	t.Run("o opens the file picker", func(t *testing.T) {
		m := sized("")
		m.layout.focus = paneResults
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
		if !m.picker.open || m.pickerPurpose != pickerForOpenSQL {
			t.Fatalf("picker = %v purpose = %v, want the open-SQL-file pick",
				m.picker.open, m.pickerPurpose)
		}
		if !m.welcome {
			t.Fatal("the page stays up behind the picker")
		}
	})

	t.Run("f1 opens the key bindings", func(t *testing.T) {
		m := sized("")
		m.layout.focus = paneResults
		m = send(m, tea.KeyMsg{Type: tea.KeyF1})
		if !m.keysDlg.open {
			t.Fatal("F1 must open the key-bindings overlay")
		}
		if !m.welcome {
			t.Fatal("the page stays up behind the overlay")
		}
	})
}
