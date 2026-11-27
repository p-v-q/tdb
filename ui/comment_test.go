package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/textbuf"
)

func selectLines(m Model, start, end int) Model {
	m.sel.anchor = &textbuf.Pos{Row: start, Col: 0}
	m.buf.SetCursor(textbuf.Pos{Row: end, Col: 0})
	return m
}

func TestToggleCommentSelection(t *testing.T) {
	m := sized("  select 1\nselect 2\n")
	m = selectLines(m, 0, 2) // includes lines 0 and 1, stops at line 2 col 0

	// Ctrl+/ arrives as Ctrl+_ in most terminals; the key alias maps it.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlUnderscore})
	if got, want := m.buf.Text(), "  -- select 1\n-- select 2\n"; got != want {
		t.Fatalf("commented text = %q, want %q", got, want)
	}

	m = selectLines(m, 0, 2)
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlUnderscore})
	if got, want := m.buf.Text(), "  select 1\nselect 2\n"; got != want {
		t.Fatalf("uncommented text = %q, want %q", got, want)
	}
}

func TestToggleCommentCurrentLineAndUndo(t *testing.T) {
	m := sized("select 1\nselect 2")
	m.buf.SetCursor(textbuf.Pos{Row: 1, Col: 3})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlUnderscore})
	if got, want := m.buf.LineString(1), "-- select 2"; got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	if got := m.buf.LineString(0); got != "select 1" {
		t.Fatalf("the unselected line changed to %q", got)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if got, want := m.buf.Text(), "select 1\nselect 2"; got != want {
		t.Fatalf("undo = %q, want %q", got, want)
	}
}
