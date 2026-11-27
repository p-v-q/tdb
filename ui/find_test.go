package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/textbuf"
)

func TestFindSelectsAndCyclesMatches(t *testing.T) {
	m := sized("select one\nselect two\nselect one")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if !m.find.open {
		t.Fatal("Ctrl+F must open the find prompt")
	}
	m = typeText(m, "one")
	if m.find.matches != 2 {
		t.Fatalf("matches = %d, want 2", m.find.matches)
	}
	if got := m.selectedText(); got != "one" {
		t.Fatalf("selected text = %q", got)
	}
	if got := m.buf.Cursor().Row; got != 0 {
		t.Fatalf("first match row = %d, want 0", got)
	}

	m = keyByName(m, "enter")
	if got := m.buf.Cursor().Row; got != 2 {
		t.Fatalf("second match row = %d, want 2", got)
	}
	m = keyByName(m, "enter") // wraps to the first match
	if got := m.buf.Cursor().Row; got != 0 {
		t.Fatalf("wrapped match row = %d, want 0", got)
	}
	m = keyByName(m, "up")
	if got := m.buf.Cursor().Row; got != 2 {
		t.Fatalf("previous match row = %d, want 2", got)
	}

	view := stripANSI(m.View())
	for _, want := range []string{"Find:", "Matches: 2", "Esc close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("find prompt is missing %q:\n%s", want, view)
		}
	}

	before := m.buf.Text()
	m = keyByName(m, "esc")
	if m.find.open {
		t.Fatal("Esc must close the find prompt")
	}
	if got := m.buf.Text(); got != before {
		t.Fatalf("find must never edit the buffer:\n%q\n%q", before, got)
	}
}

func TestFindIsCaseInsensitiveAndSeedsFromSelection(t *testing.T) {
	m := sized("SELECT alpha\nselect beta\nSELECT gamma")
	m.sel.anchor = &textbuf.Pos{Row: 1, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 1, Col: 11}) // "beta"
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.find.query != "beta" {
		t.Fatalf("query = %q, want the selected text", m.find.query)
	}
	if m.find.matches != 1 {
		t.Fatalf("matches = %d, want 1", m.find.matches)
	}

	// Replace the seeded query with an uppercase SQL keyword: all three
	// occurrences must match regardless of case.
	for range []rune("beta") {
		m = keyByName(m, "backspace")
	}
	m = typeText(m, "select")
	if m.find.matches != 3 {
		t.Fatalf("case-insensitive matches = %d, want 3", m.find.matches)
	}
	if got := m.selectedText(); got != "SELECT" {
		t.Fatalf("selected text = %q, want the first match", got)
	}
}

// TestFindHasNoShiftEnterBranch pins that the find prompt does not pretend to
// know a key the terminals cannot send: Bubble Tea v1 has no "shift+enter"
// name, so the previous match is reached with the up arrow, and that is what
// the hint in the status bar says.
func TestFindHasNoShiftEnterBranch(t *testing.T) {
	m := sized("select one\nselect two\nselect one")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = typeText(m, "one")
	row := m.buf.Cursor().Row

	m, cmd, handled := m.handleFindKey("shift+enter", nil)
	if handled {
		t.Fatal("the find prompt must not handle the unreachable key shift+enter")
	}
	if cmd != nil {
		t.Fatal("the unreachable key must not run a command")
	}
	if got := m.buf.Cursor().Row; got != row {
		t.Fatalf("the match moved to row %d, want it to stay at %d", got, row)
	}
}

func TestFindConsumesTab(t *testing.T) {
	m := sized("select one")
	focus := m.layout.focus
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = typeText(m, "one")
	m = keyByName(m, "tab")
	if !m.find.open {
		t.Fatal("Tab must not close the find prompt")
	}
	if m.layout.focus != focus {
		t.Fatal("Tab must not move focus away from the find prompt")
	}
}

// TestFindPasteDropsControlCharactersButKeepsTyping: the query is drawn in
// the status bar, so a paste may not put control characters into it — the
// prompt cleans it on the way in and once more when it draws it — while
// ordinary typing keeps working and stays visible on a narrow screen.
func TestFindPasteDropsControlCharactersButKeepsTyping(t *testing.T) {
	m := sized("select one\nselect two")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if !m.find.open {
		t.Fatal("Ctrl+F must open the find prompt")
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes,
		Runes: []rune(escPayload + "lect"), Paste: true})
	if strings.ContainsRune(m.find.query, '\x1b') {
		t.Fatalf("the query = %q, want no escape character", m.find.query)
	}
	// Only the control characters drop: the printable residue of the
	// sequence stays, exactly like every other one-line input does it.
	if m.find.query != "[31mXlect" {
		t.Fatalf("the query = %q, want the cleaned pasted text", m.find.query)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}

	// Ordinary typing still appends to what was pasted, and the prompt
	// shows it.
	m = typeText(m, "2")
	if m.find.query != "[31mXlect2" {
		t.Fatalf("the query = %q, want the pasted text plus the typed rune", m.find.query)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "lect2") {
		t.Fatalf("the prompt must show the typed text:\n%s", view)
	}

	// The status line the prompt draws stays inside a narrow terminal.
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	for _, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("view line is %d columns wide, want at most 40: %q", w, line)
		}
	}
}
