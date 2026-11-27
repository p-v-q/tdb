package ui

// editor_keys_test.go covers the key names a terminal reaches in another
// shape than the application documents them.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestF13IsTheShiftF10Alias pins the alias itself: a terminal sends
// Shift+F10 as the F13 sequence ("\x1b[25~", "\x1b[1;2P"), which Bubble Tea
// v1.3.10 decodes as KeyF13, so the application has to treat "f13" as the
// documented "shift+f10".
func TestF13IsTheShiftF10Alias(t *testing.T) {
	if got := keyName(tea.KeyMsg{Type: tea.KeyF13}); got != shiftF10Key {
		t.Fatalf("keyName(f13) = %q, want %q", got, shiftF10Key)
	}
	// The neighbouring function keys keep their own names: the terminals
	// number the shifted function keys F13…F24 after F1…F12, so F14 is
	// Shift+F2 and not Shift+F11.
	if got := keyName(tea.KeyMsg{Type: tea.KeyF14}); got != "f14" {
		t.Fatalf("keyName(f14) = %q, want f14", got)
	}
}

// TestF13TypesNothing pins that the alias stays a control key: it must not
// type its own name into a buffer.
func TestF13TypesNothing(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if got := m.buf.Text(); got != "SELECT 1" {
		t.Fatalf("buffer = %q, want the text unchanged", got)
	}
	if !m.tabMenu.open {
		t.Fatal("F13 must open the tab menu of the active tab")
	}
}

// TestF13DoesNotTypeIntoTheObjectFilter pins the same for the tree's filter
// input, which owns the keyboard while it is open.
func TestF13DoesNotTypeIntoTheObjectFilter(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.objects.filtering {
		t.Fatal("the filter input must be open")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if m.objects.filter != "" {
		t.Fatalf("filter = %q, want it empty", m.objects.filter)
	}
}

// TestF13DoesNotTypeIntoTheResultFilter pins the same for the grid's filter
// input.
func TestF13DoesNotTypeIntoTheResultFilter(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.session.result.filtering {
		t.Fatal("the grid filter must be open")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if m.session.result.filter != "" {
		t.Fatalf("filter = %q, want it empty", m.session.result.filter)
	}
}

// TestF13KeepsTheGridKeys pins that the alias does not disturb the result
// grid: the caret stays where it was and the other keys keep working.
func TestF13KeepsTheGridKeys(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m.session.result.cursorRow, m.session.result.cursorCol = 1, 1

	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	m = keyByName(m, "esc") // close the context menu again
	if m.resultMenu.open {
		t.Fatal("Esc must close the result menu")
	}
	if m.session.result.cursorRow != 1 || m.session.result.cursorCol != 1 {
		t.Fatalf("caret = (%d, %d), want it unmoved at (1, 1)",
			m.session.result.cursorRow, m.session.result.cursorCol)
	}
	m = keyByName(m, "down")
	if m.session.result.cursorRow != 2 {
		t.Fatalf("caret row = %d, want 2", m.session.result.cursorRow)
	}
}
