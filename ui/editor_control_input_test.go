package ui

// editor_control_input_test.go pins the rule that a key event's text runes are
// cleaned before they reach the buffer: a terminal can report control
// characters inside them (Windows reports Alt+NUL for a lone escape byte, a
// terminal that sends Esc with a modified key hands over Escape itself). Those
// bytes are not text and must never land in a statement, where they would be
// invisible and break the SQL.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/textbuf"
)

func controlInputModel(t *testing.T, text string) Model {
	t.Helper()
	m := sized("")
	m.buf = textbuf.New(text)
	m.buf.MoveBufferEnd()
	m.layout.focus = paneEditor
	return m
}

func TestControlRunesAreNotTyped(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{"alt-nul", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}, Alt: true}},
		{"alt-esc", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0x1b}, Alt: true}},
		{"plain-nul", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}},
		{"bell", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\a'}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := controlInputModel(t, "select 1;")
			m = send(m, tc.msg)
			if got := m.buf.Text(); got != "select 1;" {
				t.Fatalf("buffer = %q, a control rune must not be typed", got)
			}
		})
	}
}

func TestControlRunesMixedWithTextAreStripped(t *testing.T) {
	// A paste-like key message can carry text and control characters together
	// (some terminals deliver the escape byte with a modified key). The text
	// survives, the control characters do not.
	m := controlInputModel(t, "")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 0, 'b', 0x1b, 'c'}})
	if got := m.buf.Text(); got != "abc" {
		t.Fatalf("buffer = %q, want %q", got, "abc")
	}
}

func TestWindowsKeyAliases(t *testing.T) {
	// The names Windows input paths produce must reach the documented
	// shortcuts: Ctrl+/ arrives as a unit separator and Ctrl+Space as a lone
	// NUL rune.
	cases := map[string]string{
		"ctrl+_": "ctrl+/",
		"\x00":   "ctrl+space",
		"f13":    "shift+f10",
		"ctrl+a": "ctrl+a",
	}
	for in, want := range cases {
		if got := normalizeKeyName(in); got != want {
			t.Errorf("normalizeKeyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCtrlSpaceOpensCompletion(t *testing.T) {
	// On Windows Ctrl+Space arrives as a key whose only rune is NUL: it must
	// open the completion popup, like the documented shortcut does.
	m := controlInputModel(t, "")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}})
	if got := m.buf.Text(); got != "" {
		t.Fatalf("buffer = %q, NUL must not be typed", got)
	}
}
