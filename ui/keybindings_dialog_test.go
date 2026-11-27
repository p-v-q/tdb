package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
)

// txStubConn implements driver.TransactionManager without a database. The
// embedded Connection is never called: the tests only exercise the capability
// check the overlay uses to decide whether transaction bindings are listed.
type txStubConn struct{ driver.Connection }

func (txStubConn) Begin(context.Context) error    { return nil }
func (txStubConn) Commit(context.Context) error   { return nil }
func (txStubConn) Rollback(context.Context) error { return nil }
func (txStubConn) InTx() bool                     { return false }

// sizedKeybindings opens the overlay on a sized screen and checks that it is
// really open, so every test starts from the same state.
func sizedKeybindings(t *testing.T, width, height int) Model {
	t.Helper()
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: width, Height: height})
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.keysDlg.open {
		t.Fatal("F1 must open the Key Bindings overlay")
	}
	return m
}

// keybindingsText renders every row as plain text, independent of the visible
// scroll window. Tests that check the whole table use it instead of View().
func keybindingsText(m Model) string {
	var b strings.Builder
	for _, line := range m.keybindingsLines() {
		b.WriteString(line.label)
		b.WriteString(" ")
		b.WriteString(line.keys)
		b.WriteString("\n")
	}
	return b.String()
}

func TestF1OpensKeybindingsOverlay(t *testing.T) {
	m := sizedKeybindings(t, 100, 30)
	if m.keysDlg.scroll != 0 {
		t.Fatalf("scroll = %d, want the top of the list", m.keysDlg.scroll)
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		m.t(i18n.KeyKeysTitle), // Key Bindings
		m.t(i18n.KeyKeysHint),  // ↑↓ scroll  Esc close
		"┌",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the overlay is missing %q:\n%s", want, view)
		}
	}
}

func TestActKeymapOpensKeybindings(t *testing.T) {
	m := sized("")
	m.runMenuAction(actKeymap)
	if !m.keysDlg.open {
		t.Fatal("the actKeymap action must open the overlay")
	}
}

func TestKeybindingsDismissesOtherOverlays(t *testing.T) {
	m := sized("")
	m.openMenu(1)
	m.popup.open = true
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.keysDlg.open {
		t.Fatal("F1 must open the overlay")
	}
	if m.menu.open != -1 {
		t.Fatal("the menu must close when the overlay opens")
	}
	if m.popup.open {
		t.Fatal("the completion popup must close when the overlay opens")
	}
}

func TestKeybindingsListMenuShortcuts(t *testing.T) {
	m := sizedKeybindings(t, 120, 70)
	text := keybindingsText(m)

	// Every advertised shortcut comes from menuDefs, so walking the table here
	// proves the overlay cannot drift away from the command table.
	for _, def := range menuDefs {
		for _, it := range def.items {
			if it.shortcut == "" {
				continue
			}
			if it.needsTx && !m.supportsTransactions() {
				continue
			}
			if !strings.Contains(text, m.t(it.labelKey)) {
				t.Errorf("the overlay is missing the label %q", m.t(it.labelKey))
			}
			if !strings.Contains(text, displayItemShortcuts(it)) {
				t.Errorf("the overlay is missing the shortcut %q of %q",
					displayItemShortcuts(it), m.t(it.labelKey))
			}
		}
	}

	// The bindings named by the task must be visible without scrolling on a
	// normal terminal.
	view := stripANSI(m.View())
	for _, want := range []string{"F5", "Alt+1", "^S"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the rendered overlay is missing %q:\n%s", want, view)
		}
	}
}

func TestKeybindingsTransactionItemsFollowCapability(t *testing.T) {
	m := sizedKeybindings(t, 120, 70)
	text := keybindingsText(m)
	if !m.supportsTransactions() {
		if strings.Contains(text, m.t(i18n.KeyMenuCommit)) {
			t.Fatal("Commit must not be listed without driver.TransactionManager")
		}
		if strings.Contains(text, "F9") {
			t.Fatal("F9 must not be listed without driver.TransactionManager")
		}
	}

	// A connection that supports transactions reveals the hidden suffix.
	m.connOpen = txStubConn{}
	if !m.supportsTransactions() {
		t.Fatal("the stub connection must satisfy driver.TransactionManager")
	}
	text = keybindingsText(m)
	for _, want := range []string{
		m.t(i18n.KeyMenuCommit), "F9",
		m.t(i18n.KeyMenuRollback), "F11",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("a transaction-capable connection must list %q", want)
		}
	}
}

// TestKeybindingsListsTheF13Alias pins that the context-menu shortcut is
// advertised with both of its names: the terminals send Shift+F10 as F13 and
// the application takes either for the same shortcut.
func TestKeybindingsListsTheF13Alias(t *testing.T) {
	m := sizedKeybindings(t, 120, 70)
	text := keybindingsText(m)

	// The object browser and the editor have a context menu of their own.
	if got := strings.Count(text, "⇧F10/F13"); got != 2 {
		t.Fatalf("the overlay lists the F13 alias %d times, want 2:\n%s", got, text)
	}
}

// TestKeybindingsListsTheApplyKeys pins that the result grid advertises both
// of the keys that apply the pending cell changes: Ctrl+S and its Ctrl+Enter
// fallback for the terminals whose tty swallows 0x13.
func TestKeybindingsListsTheApplyKeys(t *testing.T) {
	m := sizedKeybindings(t, 120, 70)
	text := keybindingsText(m)

	if !strings.Contains(text, m.t(i18n.KeyResultApplyChanges)) {
		t.Fatalf("the overlay must label the apply binding:\n%s", text)
	}
	if !strings.Contains(text, "^S / ^Enter") {
		t.Fatalf("the overlay must advertise both apply keys:\n%s", text)
	}
}

func TestKeybindingsEscClosesAndSwallowsKeys(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	before := m.buf.Text()

	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if !m.keysDlg.open {
		t.Fatal("F1 must open the overlay")
	}

	// Typing must not reach the editor behind the modal overlay.
	m = typeText(m, "x")
	if got := m.buf.Text(); got != before {
		t.Fatalf("keys leaked into the editor: %q", got)
	}
	if !m.keysDlg.open {
		t.Fatal("a plain key must not close the overlay")
	}

	m = keyByName(m, "esc")
	if m.keysDlg.open {
		t.Fatal("Esc must close the overlay")
	}
	if got := m.buf.Text(); got != before {
		t.Fatalf("Esc leaked into the editor: %q", got)
	}

	// F1 is a toggle as well: it closes an overlay it opened.
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	m = send(m, tea.KeyMsg{Type: tea.KeyF1})
	if m.keysDlg.open {
		t.Fatal("F1 must also close the overlay")
	}
}

func TestKeybindingsScrollClamps(t *testing.T) {
	m := sizedKeybindings(t, 100, 20)
	maxScroll := m.keybindingsMaxScroll()
	if maxScroll <= 0 {
		t.Fatalf("the test needs a scrollable list, maxScroll = %d", maxScroll)
	}
	listH := m.keybindingsLayout().listH

	m = keyByName(m, "end")
	if m.keysDlg.scroll != maxScroll {
		t.Fatalf("End: scroll = %d, want %d", m.keysDlg.scroll, maxScroll)
	}
	m = keyByName(m, "down")
	if m.keysDlg.scroll != maxScroll {
		t.Fatalf("Down past the end: scroll = %d, want %d", m.keysDlg.scroll, maxScroll)
	}

	m = keyByName(m, "up")
	if m.keysDlg.scroll != maxScroll-1 {
		t.Fatalf("Up: scroll = %d, want %d", m.keysDlg.scroll, maxScroll-1)
	}

	m = keyByName(m, "home")
	if m.keysDlg.scroll != 0 {
		t.Fatalf("Home: scroll = %d, want 0", m.keysDlg.scroll)
	}
	m = keyByName(m, "up")
	if m.keysDlg.scroll != 0 {
		t.Fatalf("Up past the start: scroll = %d, want 0", m.keysDlg.scroll)
	}

	m = keyByName(m, "pgdown")
	want := min(listH, maxScroll)
	if m.keysDlg.scroll != want {
		t.Fatalf("PgDn: scroll = %d, want %d", m.keysDlg.scroll, want)
	}
	m = keyByName(m, "pgup")
	if m.keysDlg.scroll != 0 {
		t.Fatalf("PgUp: scroll = %d, want 0", m.keysDlg.scroll)
	}

	// Scrolling really changes the visible window: at the end the editor keys
	// are on screen and the first menu items are not.
	m = keyByName(m, "end")
	view := stripANSI(m.View())
	if !strings.Contains(view, m.t(i18n.KeyKeysAccept)) {
		t.Fatalf("the end of the list is not visible:\n%s", view)
	}
	if strings.Contains(view, m.t(i18n.KeyMenuNewTab)) {
		t.Fatalf("the top of the list must have scrolled away:\n%s", view)
	}
}

func TestKeybindingsMouseScrollsAndDismisses(t *testing.T) {
	m := sizedKeybindings(t, 100, 20)
	r := m.keybindingsRect()

	m = send(m, mouseWheel(true, r.X+1, r.Y+1))
	if m.keysDlg.scroll == 0 {
		t.Fatal("the wheel must scroll the overlay")
	}

	// A click inside is swallowed; clicking outside dismisses without reaching
	// the pane underneath.
	before := m.buf.Text()
	m = send(m, mousePress(r.X+1, r.Y+1))
	if !m.keysDlg.open {
		t.Fatal("a click inside the overlay must not close it")
	}
	outsideX := min(m.width-1, r.X+r.W+1)
	m = send(m, mousePress(outsideX, r.Y))
	if m.keysDlg.open {
		t.Fatal("a click outside the overlay must close it")
	}
	if got := m.buf.Text(); got != before {
		t.Fatalf("a click leaked into the editor: %q", got)
	}
}

func TestKeybindingsRenderFitsTerminals(t *testing.T) {
	sizes := [][2]int{{40, 80}, {60, 200}, {40, 200}, {60, 80}}
	for _, size := range sizes {
		width, height := size[0], size[1]
		m := sizedKeybindings(t, width, height)

		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != height {
			t.Fatalf("%dx%d: View returned %d rows", width, height, len(lines))
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w != width {
				t.Fatalf("%dx%d: row %d is %d columns wide", width, height, i, w)
			}
		}

		x, y, block, ok := m.renderKeybindingsDialog()
		if !ok {
			t.Fatalf("%dx%d: the overlay must render", width, height)
		}
		l := m.keybindingsLayout()
		if len(block) != l.h {
			t.Fatalf("%dx%d: block has %d rows, want %d", width, height, len(block), l.h)
		}
		for i, line := range block {
			if w := lipgloss.Width(line); w != l.w {
				t.Fatalf("%dx%d: block row %d is %d columns wide, want %d",
					width, height, i, w, l.w)
			}
		}
		area := m.paneRects().area
		if x < area.X || y < area.Y || x+l.w > area.X+area.W || y+l.h > area.Y+area.H {
			t.Fatalf("%dx%d: the box at (%d,%d,%d,%d) leaves the area %+v",
				width, height, x, y, l.w, l.h, area)
		}
	}
}

// TestKeybindingsSkipsGroupsWithoutShortcuts pins that a heading without any
// binding under it is not shown: the application menu has no shortcut, so
// its heading would take a line and tell the reader nothing.
func TestKeybindingsSkipsGroupsWithoutShortcuts(t *testing.T) {
	m := sizedKeybindings(t, 120, 70)
	text := keybindingsText(m)

	if strings.Contains(text, m.t(i18n.KeyAppName)) {
		t.Fatalf("the empty application group must be skipped:\n%s", text)
	}
	// The groups that do have bindings are still there, in the same order.
	for _, want := range []string{
		m.t(i18n.KeyMenuFile),
		m.t(i18n.KeyMenuEdit),
		m.t(i18n.KeyMenuQuery),
		m.t(i18n.KeyKeysObjects),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the overlay is missing the group %q:\n%s", want, text)
		}
	}
	// The order of the groups did not change.
	file := strings.Index(text, m.t(i18n.KeyMenuFile))
	edit := strings.Index(text, m.t(i18n.KeyMenuEdit))
	query := strings.Index(text, m.t(i18n.KeyMenuQuery))
	if !(file < edit && edit < query) {
		t.Fatalf("the groups are out of order: file=%d edit=%d query=%d", file, edit, query)
	}
}
