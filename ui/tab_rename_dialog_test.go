package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// openRenameDialog opens the rename input of a tab.
func openRenameDialog(m Model, index int) Model {
	m.openTabRename(index)
	return m
}

// TestRenameAppliesTheName pins the happy path: the typed name becomes the
// name of the tab and survives the dialog.
func TestRenameAppliesTheName(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].name = "public.users"
	m = openRenameDialog(m, 0)
	m = typeText(m, "my query")
	m = keyByName(m, "enter")

	if m.tabRename.open {
		t.Fatal("Enter must close the rename input")
	}
	if got := m.tabs[0].userName; got != "my query" {
		t.Fatalf("user name = %q, want the typed name", got)
	}
	if got := m.tabLabel(0); got != "my query" {
		t.Fatalf("tab label = %q, want the typed name", got)
	}
}

// TestRenameSurvivesTheTabSwitch pins that the name belongs to the tab, not
// to the view: switching away and back keeps it.
func TestRenameSurvivesTheTabSwitch(t *testing.T) {
	m := sized("SELECT 1")
	m = openRenameDialog(m, 0)
	m = typeText(m, "mine")
	m = keyByName(m, "enter")

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if got := m.tabLabel(1); got == "mine" {
		t.Fatal("the name must belong to the tab it was given to")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if got := m.tabLabel(0); got != "mine" {
		t.Fatalf("tab label = %q, want the name to survive the switch", got)
	}
}

// TestRenameCancelKeepsTheName pins that Esc changes nothing.
func TestRenameCancelKeepsTheName(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].userName = "keep me"
	m = openRenameDialog(m, 0)
	m = typeText(m, "x")
	m = keyByName(m, "esc")

	if m.tabRename.open {
		t.Fatal("Esc must close the rename input")
	}
	if got := m.tabLabel(0); got != "keep me" {
		t.Fatalf("tab label = %q, want the old name", got)
	}
}

// TestEmptyNameRestoresTheAutomaticName pins the promise of the prompt: an
// empty name goes back to the name the tab would have without one.
func TestEmptyNameRestoresTheAutomaticName(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].name = "public.users"
	m = openRenameDialog(m, 0)
	m = typeText(m, "mine")
	m = keyByName(m, "enter")
	if got := m.tabLabel(0); got != "mine" {
		t.Fatalf("tab label = %q, want the typed name", got)
	}

	// Rename it again, but empty the input this time.
	m = openRenameDialog(m, 0)
	for range "mine" {
		m = keyByName(m, "backspace")
	}
	m = keyByName(m, "enter")

	if got := m.tabs[0].userName; got != "" {
		t.Fatalf("user name = %q, want it cleared", got)
	}
	if got := m.tabLabel(0); got != "public.users" {
		t.Fatalf("tab label = %q, want the automatic name back", got)
	}
}

// TestRenameDoesNotLeakKeysIntoTheEditor pins that the input is modal: what
// is typed goes into the name, never into the SQL behind it.
func TestRenameDoesNotLeakKeysIntoTheEditor(t *testing.T) {
	m := sized("SELECT 1")
	before := m.buf.Text()
	m = openRenameDialog(m, 0)
	m = typeText(m, "abc")

	if got := m.buf.Text(); got != before {
		t.Fatalf("the editor changed behind the dialog: %q", got)
	}
	if got := m.tabRename.value; got != "abc" {
		t.Fatalf("input = %q, want the typed text", got)
	}
}

// TestRenameEditsTheText pins the caret: the text can be corrected, not only
// typed.
func TestRenameEditsTheText(t *testing.T) {
	m := sized("")
	m = openRenameDialog(m, 0)
	m = typeText(m, "abc")

	m = keyByName(m, "left")
	m = keyByName(m, "backspace")
	if got := m.tabRename.value; got != "ac" {
		t.Fatalf("input = %q, want %q", got, "ac")
	}
	m = keyByName(m, "home")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	if got := m.tabRename.value; got != "Xac" {
		t.Fatalf("input = %q, want %q", got, "Xac")
	}
	m = keyByName(m, "end")
	m = keyByName(m, "left")
	m = keyByName(m, "delete")
	if got := m.tabRename.value; got != "Xa" {
		t.Fatalf("input = %q, want %q", got, "Xa")
	}
}

// TestRenameAcceptsAPaste pins that a pasted name arrives whole.
func TestRenameAcceptsAPaste(t *testing.T) {
	m := sized("")
	m = openRenameDialog(m, 0)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted name"), Paste: true})

	if got := m.tabRename.value; got != "pasted name" {
		t.Fatalf("input = %q, want the pasted text", got)
	}
}

// TestRenameShowsThePromptAndTheAutomaticName pins what the dialog says: the
// prompt, the name an empty input restores and the keys that work.
func TestRenameShowsThePromptAndTheAutomaticName(t *testing.T) {
	m := sized("")
	// The fixture starts on the welcome page (automatic name "Welcome");
	// this dialog is pinned on a tab derived from an object name.
	m.welcomeToEditor()
	m.tabs[0].name = "public.users"
	m = openRenameDialog(m, 0)

	view := stripANSI(m.View())
	for _, want := range []string{
		"New name (empty restores the automatic name):",
		"Automatic name: public.users",
		"Enter confirm · Esc cancel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the rename dialog is missing %q:\n%s", want, view)
		}
	}
}

// TestRenameDropsControlCharacters pins that a pasted escape sequence or a
// newline never reaches the tab bar, which draws names as plain text.
func TestRenameDropsControlCharacters(t *testing.T) {
	m := sized("")
	m = openRenameDialog(m, 0)
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb\x1b[31m"), Paste: true})

	if got := m.tabRename.value; got != "ab[31m" {
		t.Fatalf("input = %q, want the control characters dropped", got)
	}
}

// TestLongUserNameIsShortenedInTheTabBar pins that a name that does not fit
// the tab bar is shortened, with the ellipsis that says that it was cut.
func TestLongUserNameIsShortenedInTheTabBar(t *testing.T) {
	m := sized("")
	m.tabs[0].userName = strings.Repeat("x", 60)

	got := m.tabLabel(0)
	if n := len([]rune(got)); n != maxTabNameLen {
		t.Fatalf("tab label = %q (%d runes), want %d", got, n, maxTabNameLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("tab label = %q, want an ellipsis", got)
	}
}

// TestRenameClickOutsideCancels pins that the input can be dismissed with the
// mouse as well.
func TestRenameClickOutsideCancels(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].userName = "keep me"
	m = openRenameDialog(m, 0)
	next := send(m, mousePress(1, 1))

	if next.tabRename.open {
		t.Fatal("a click outside the dialog must close it")
	}
	if got := next.tabLabel(0); got != "keep me" {
		t.Fatalf("tab label = %q, want the old name", got)
	}
}
