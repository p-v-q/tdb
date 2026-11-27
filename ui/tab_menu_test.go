package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- The name of a tab ----

// TestTabLabelPrefersTheUserName pins the order the names are resolved in: a
// name the user gave the tab wins over the file it holds, which wins over the
// automatically derived name.
func TestTabLabelPrefersTheUserName(t *testing.T) {
	m := sized("")
	// The fixture starts on the welcome page, and that tab is named for what
	// it is; this test is about the names an ordinary tab is resolved from.
	m.welcomeToEditor()
	m.tabs[0].fileName = "queries/report.sql"
	m.tabs[0].name = "public.users"
	m.tabs[0].userName = "my report"

	if got := m.tabLabel(0); got != "my report" {
		t.Fatalf("tab label = %q, want the name the user gave", got)
	}

	// Without a name of the user's own, the file wins over the derived name.
	m.tabs[0].userName = ""
	if got := m.tabLabel(0); got != "report.sql" {
		t.Fatalf("tab label = %q, want the file name", got)
	}

	// Without a file, the derived name is used.
	m.tabs[0].fileName = ""
	if got := m.tabLabel(0); got != "public.users" {
		t.Fatalf("tab label = %q, want the derived name", got)
	}

	// Without any name at all, the tab is untitled.
	m.tabs[0].name = ""
	m.tabs[0].createdAt = time.Time{}
	if got := m.tabLabel(0); got != "untitled" {
		t.Fatalf("tab label = %q, want untitled", got)
	}
}

// TestTabWithoutANameKeepsTheOpeningTime covers the tab that has nothing to
// be named after: the time it was opened tells the tabs apart.
func TestTabWithoutANameKeepsTheOpeningTime(t *testing.T) {
	m := sized("")
	// The welcome tab is named "Welcome" (see tabAutoLabel), so the tab that
	// has nothing to be named after is an ordinary one.
	m.welcomeToEditor()
	m.tabs[0].createdAt = time.Date(2026, 10, 20, 15, 4, 5, 0, time.UTC)
	if got := m.tabLabel(0); got != "15:04:05" {
		t.Fatalf("tab label = %q, want the opening time", got)
	}
}

// TestGeneratedStatementTabsAreNamedAfterTheObject pins the names of the tabs
// the object browser opens: the relation, qualified by its schema, with the
// kind of statement in brackets.
func TestGeneratedStatementTabsAreNamedAfterTheObject(t *testing.T) {
	cases := []struct {
		action objectsMenuAction
		want   string
	}{
		{objActGenSelect, "public.users [SELECT]"},
		{objActGenInsert, "public.users [INSERT]"},
		{objActGenUpdate, "public.users [UPDATE]"},
		{objActGenDelete, "public.users [DELETE]"},
		{objActViewData, "public.users [data]"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			m := sized("")
			idx := objectRowIndex(m, "users")
			if idx < 0 {
				t.Fatal("no users row in the tree")
			}
			m = rightClickObjectsRow(m, idx)
			next, cmd := clickContextAction(m, tc.action)
			collectCmds(cmd)
			if len(next.tabs) != 2 {
				t.Fatalf("tabs = %d, want the statement in a second tab", len(next.tabs))
			}
			if got := next.tabLabel(1); got != tc.want {
				t.Fatalf("tab label = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDefinitionTabIsNamedAfterTheObject pins the name of the tab a DDL opens
// in: the object's schema-qualified name.
func TestDefinitionTabIsNamedAfterTheObject(t *testing.T) {
	m := sized("")
	m.connOpen = demoDefinitionConn(t)
	idx, _ := findObjectRow(t, m, rowTable, "users")
	m = rightClickObjectsRow(m, idx)
	next, cmd := clickContextAction(m, objActViewDef)

	found := false
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			found = true
			next = send(next, dm)
		}
	}
	if !found {
		t.Fatal("the definition must be fetched in the background")
	}
	if len(next.tabs) != 2 {
		t.Fatalf("tabs = %d, want the definition in a second tab", len(next.tabs))
	}
	if got := next.tabLabel(1); got != "public.users" {
		t.Fatalf("tab label = %q, want the qualified object name", got)
	}
}

// TestDefinitionTabOfAResourceIsNamedAfterTheObject covers the objects that
// are not relations: they are qualified the same way.
func TestDefinitionTabOfAResourceIsNamedAfterTheObject(t *testing.T) {
	m := sized("")
	m.connOpen = demoDefinitionConn(t)
	idx, _ := findObjectRow(t, m, rowResource, "order_total")
	m = rightClickObjectsRow(m, idx)
	next, cmd := clickContextAction(m, objActViewDef)
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			next = send(next, dm)
		}
	}
	if got := next.tabLabel(1); got != "public.order_total" {
		t.Fatalf("tab label = %q, want the qualified object name", got)
	}
}

// TestHistoryTabIsNamedAfterTheFirstLine pins the name of a tab opened from
// the query history: the first line of the statement.
func TestHistoryTabIsNamedAfterTheFirstLine(t *testing.T) {
	m, _ := historyModel(t, "SELECT id FROM users")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want a new tab", len(m.tabs))
	}
	if got := m.tabLabel(1); got != "SELECT id FROM users" {
		t.Fatalf("tab label = %q, want the first line of the statement", got)
	}
}

// TestHistoryTabNameIsTrimmed covers a long statement: the name is cut to
// what fits in the tab bar, and the ellipsis says that it was cut.
func TestHistoryTabNameIsTrimmed(t *testing.T) {
	stmt := "SELECT id, email, name, age FROM users ORDER BY id"
	m, _ := historyModel(t, stmt)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})

	got := m.tabLabel(1)
	if n := len([]rune(got)); n != maxTabNameLen {
		t.Fatalf("tab label = %q (%d runes), want %d", got, n, maxTabNameLen)
	}
	if !strings.HasPrefix(got, "SELECT id, email") {
		t.Fatalf("tab label = %q, want the beginning of the statement", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("tab label = %q, want an ellipsis at the end", got)
	}
}

// ---- The tab context menu ----

// rightClickTab right-clicks a tab of the tab bar.
func rightClickTab(m Model, index int) Model {
	slots := m.tabSlots(m.paneRects().tabbar)
	for _, s := range slots {
		if s.kind == slotTab && s.index == index {
			return send(m, tea.MouseMsg{
				X: s.x + 1, Y: m.paneRects().tabbar.Y,
				Action: tea.MouseActionPress, Button: tea.MouseButtonRight,
			})
		}
	}
	return m
}

// clickTabMenuAction clicks an item of the open tab context menu.
func clickTabMenuAction(m Model, action tabMenuAction) (Model, tea.Cmd) {
	for i, a := range m.tabMenu.actions {
		if a != action {
			continue
		}
		r, ok := m.tabMenuRect()
		if !ok {
			return m, nil
		}
		return sendCmd(m, mousePress(r.X+2, r.Y+1+i))
	}
	return m, nil
}

// TestRightClickOnATabOpensItsMenu pins the mouse entry point of the tab
// context menu and the actions it offers.
func TestRightClickOnATabOpensItsMenu(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT}) // a second tab
	m = rightClickTab(m, 0)

	if !m.tabMenu.open {
		t.Fatal("right-clicking a tab must open its menu")
	}
	if m.tabMenu.tab != 0 {
		t.Fatalf("menu tab = %d, want the clicked one", m.tabMenu.tab)
	}
	want := []tabMenuAction{
		tabActRename, tabActDuplicate, tabActClose,
		tabActCloseOthers, tabActCloseRight, tabActCloseAll,
	}
	if !slices.Equal(m.tabMenu.actions, want) {
		t.Fatalf("actions = %v, want %v", m.tabMenu.actions, want)
	}
	view := stripANSI(m.View())
	for _, label := range []string{"Rename…", "Duplicate", "Close", "Close others", "Close to the right", "Close all"} {
		if !strings.Contains(view, label) {
			t.Fatalf("the menu is missing %q:\n%s", label, view)
		}
	}
	// The first tab has nothing to its left, so that entry is left out.
	if slices.Contains(m.tabMenu.actions, tabActCloseLeft) {
		t.Fatalf("the first tab must not offer Close to the left: %v", m.tabMenu.actions)
	}
}

// TestRightClickOnAnotherTabMovesTheMenu pins that the menu follows the
// pointer: right-clicking a second tab opens its menu, not the first one's.
func TestRightClickOnAnotherTabMovesTheMenu(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = rightClickTab(m, 0)
	m = rightClickTab(m, 1)

	if !m.tabMenu.open {
		t.Fatal("the menu must be open for the second tab")
	}
	if m.tabMenu.tab != 1 {
		t.Fatalf("menu tab = %d, want the second one", m.tabMenu.tab)
	}
}

// TestShiftF10OpensTheTabMenu pins the keyboard entry point: with the editor
// focused, Shift+F10 opens the menu of the active tab.
func TestShiftF10OpensTheTabMenu(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+f10")})

	if !m.tabMenu.open {
		t.Fatal("Shift+F10 must open the tab menu")
	}
	if m.tabMenu.tab != m.active {
		t.Fatalf("menu tab = %d, want the active one (%d)", m.tabMenu.tab, m.active)
	}
	m = keyByName(m, "esc")
	if m.tabMenu.open {
		t.Fatal("Esc must close the tab menu")
	}
}

// TestF13OpensTheTabMenu pins the same entry point for the terminals that
// send Shift+F10 as F13 ("\x1b[25~", "\x1b[1;2P").
func TestF13OpensTheTabMenu(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})

	if !m.tabMenu.open {
		t.Fatal("F13 must open the tab menu")
	}
	if m.tabMenu.tab != m.active {
		t.Fatalf("menu tab = %d, want the active one (%d)", m.tabMenu.tab, m.active)
	}
}

// TestTabMenuEscapeAndArrowsDriveTheSelection pins that the menu is usable
// with the keyboard alone.
func TestTabMenuEscapeAndArrowsDriveTheSelection(t *testing.T) {
	m := sized("")
	m = rightClickTab(m, 0)
	if m.tabMenu.sel != 0 {
		t.Fatalf("selection = %d, want the first item", m.tabMenu.sel)
	}
	m = keyByName(m, "down")
	if m.tabMenu.sel != 1 {
		t.Fatalf("selection = %d, want the second item", m.tabMenu.sel)
	}
	m = keyByName(m, "up")
	if m.tabMenu.sel != 0 {
		t.Fatalf("selection = %d, want the first item again", m.tabMenu.sel)
	}
	m = keyByName(m, "end")
	if m.tabMenu.sel != 2 {
		t.Fatalf("selection = %d, want the last item", m.tabMenu.sel)
	}
}

// TestTabMenuDuplicateCopiesTheTab pins the Duplicate action: the copy holds
// the same text and the same name, and it becomes the active tab.
func TestTabMenuDuplicateCopiesTheTab(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].name = "public.users"
	m = rightClickTab(m, 0)
	next, _ := clickTabMenuAction(m, tabActDuplicate)

	if len(next.tabs) != 2 {
		t.Fatalf("tabs = %d, want the copy in a second tab", len(next.tabs))
	}
	if got := next.tabs[1].buf.Text(); got != "SELECT 1" {
		t.Fatalf("copy text = %q, want the text of the original", got)
	}
	if got := next.tabLabel(1); got != "public.users" {
		t.Fatalf("copy label = %q, want the name of the original", got)
	}
	if next.active != 1 {
		t.Fatalf("active = %d, want the copy", next.active)
	}
	// The copy is a new buffer: it has no file of its own.
	if next.tabs[1].fileName != "" {
		t.Fatalf("copy file = %q, want none", next.tabs[1].fileName)
	}
}

// TestTabMenuCloseClosesTheTab pins the Close action.
func TestTabMenuCloseClosesTheTab(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = rightClickTab(m, 0)
	next, _ := clickTabMenuAction(m, tabActClose)

	if len(next.tabs) != 1 {
		t.Fatalf("tabs = %d, want the tab closed", len(next.tabs))
	}
	if next.tabMenu.open {
		t.Fatal("the menu must be closed after the action")
	}
}

// TestTabMenuRenameOpensTheDialog pins that Rename opens the rename input
// for the tab the menu was opened on.
func TestTabMenuRenameOpensTheDialog(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = rightClickTab(m, 1)
	next, _ := clickTabMenuAction(m, tabActRename)

	if !next.tabRename.open {
		t.Fatal("Rename must open the rename input")
	}
	if next.tabRename.tab != 1 {
		t.Fatalf("rename tab = %d, want the one of the menu", next.tabRename.tab)
	}
}

// TestTabMenuActionRunsFromTheKeyboard pins that the menu can be used
// without the mouse: the arrows pick the item and Enter runs it.
func TestTabMenuActionRunsFromTheKeyboard(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].name = "public.users"
	m = rightClickTab(m, 0)
	m = keyByName(m, "down") // Duplicate
	m = keyByName(m, "enter")

	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want the copy", len(m.tabs))
	}
	if got := m.tabLabel(1); got != "public.users" {
		t.Fatalf("copy label = %q, want the name of the original", got)
	}
	if m.tabMenu.open {
		t.Fatal("the menu must be closed after the action")
	}
}

// TestTabMenuClickOutsideClosesIt pins that a click outside the menu
// dismisses it without reaching the pane behind it.
func TestTabMenuClickOutsideClosesIt(t *testing.T) {
	m := sized("SELECT 1")
	m = rightClickTab(m, 0)
	next := send(m, mousePress(1, 1))
	if next.tabMenu.open {
		t.Fatal("a click outside the menu must close it")
	}
}
