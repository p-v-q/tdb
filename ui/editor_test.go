package ui

// editor_test.go covers the two paths that write text into the editor buffer
// without the user pressing a key for every character of it: a completion
// insert and a paste. The buffer is drawn verbatim (see sanitize.go), so both
// are cleaned on the way in — while a line break is a control character too
// and has to survive wherever the SQL needs it: the structure of a snippet
// template, the lines and indentation of a pasted statement.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/textbuf"
)

// TestStripControlKeepLinesKeepsOnlyTheLineBreak pins the rule of the
// cleaner the insert uses: every control character of a catalog name is gone,
// the breaks that structure a template stay.
func TestStripControlKeepLinesKeepsOnlyTheLineBreak(t *testing.T) {
	got := stripControlKeepLines("SELECT *\nFROM \x1b]52;c;AAAA\x07t\x00")
	if got != "SELECT *\nFROM ]52;c;AAAAt" {
		t.Fatalf("stripControlKeepLines = %q", got)
	}
	if strings.ContainsAny(strings.ReplaceAll(got, "\n", ""), "\x1b\x07\x00") {
		t.Fatalf("stripControlKeepLines = %q, want no control character but the break", got)
	}
}

// TestAcceptSnippetKeepsItsLineBreaks guards the carve-out above in the real
// flow: accepting a snippet must paste the template with its line breaks, so
// that the caret lands on the placeholder the snippet asks for and the SQL
// arrives formatted. A plain stripControl would glue the template onto one
// line and shift the caret by the number of dropped breaks.
func TestAcceptSnippetKeepsItsLineBreaks(t *testing.T) {
	m := sized("")
	m.welcomeToEditor() // the page swallows plain letters, the editor is where we type
	m = typeText(m, "sel")
	if !m.popup.open {
		t.Fatal("typing sel must open the popup")
	}
	idx := -1
	for i, c := range m.popup.items {
		if c.Kind == complete.KindSnippet && strings.Contains(c.Label, "FROM") {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no snippet candidate for sel: %+v", m.popup.items)
	}
	m.popup.selected = idx

	m = keyByName(m, "tab")
	// The snippet's caret marker sits before the column expression, so the
	// template arrives whole — break included — and the caret rests where the
	// marker was: dropping the break would also shift that offset.
	if got, want := m.buf.Text(), "SELECT *\nFROM "; got != want {
		t.Fatalf("the buffer = %q, want %q", got, want)
	}
	if got, want := m.buf.Cursor(), (textbuf.Pos{Row: 0, Col: len("SELECT ")}); got != want {
		t.Fatalf("the caret = %+v, want %+v", got, want)
	}
}

// ---- The paste path ----

// TestPasteDropsTheEscapeSequencesOfSQL pins the cleaning of the paste branch
// of editor_keys.go: a pasted statement is text from outside the program —
// copied from a web page, a shared file, another terminal — while the buffer
// is drawn verbatim, so an OSC 52 in the paste would write the clipboard and
// a CSI 2J would clear the screen the moment the frame is drawn. Every other
// input of the program cleans its pastes; this is the same rule here. The SQL
// around the payload is what the user wanted to paste and stays.
func TestPasteDropsTheEscapeSequencesOfSQL(t *testing.T) {
	m := editorModel("")
	paste := "SELECT '" + seqOSC52 + seqClearScreen + "evil'"
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})

	// Only the control characters are gone: "]52;c;aaaa" and "[2J" are
	// ordinary text once their introducer is dropped — the same half of the
	// payload stripControl leaves everywhere else. What must not survive is
	// any byte the terminal acts on, which the frame assertion checks.
	if got, want := m.buf.Text(), "SELECT ']52;c;aaaa[2Jevil'"; got != want {
		t.Fatalf("the buffer = %q, want %q", got, want)
	}
	assertNoPayload(t, m.View(), "a hostile paste into the editor")
	assertViewClean(t, m, "a hostile paste into the editor")
}

// TestPasteKeepsTheStructureOfTheStatement pins the other half of the rule:
// '\n' and '\t' are control characters too, but they are how the statement is
// written — drop them and a multi-line paste arrives glued into one line with
// its indentation gone. So the buffer holds the pasted bytes unchanged, and
// the frame still draws one row per buffer line with the second row indented.
func TestPasteKeepsTheStructureOfTheStatement(t *testing.T) {
	m := editorModel("")
	paste := "SELECT *\n\tFROM 中文😀"
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})

	if got := m.buf.Text(); got != paste {
		t.Fatalf("the buffer = %q, want the pasted bytes unchanged (%q)", got, paste)
	}
	if got, want := string(m.buf.Line(1)), "\tFROM 中文😀"; got != want {
		t.Fatalf("the second buffer line = %q, want %q", got, want)
	}

	lines := strings.Split(m.View(), "\n")
	y := editorRowY(m, 1) // the screen row of the second buffer line
	if y >= len(lines) {
		t.Fatalf("the frame has no row for the second line:\n%s", m.View())
	}
	plain := stripANSI(lines[y])
	i := strings.Index(plain, "FROM 中文😀")
	if i < 0 {
		t.Fatalf("the second row must be drawn in full: %q", plain)
	}
	// One space is the gutter's, the next belongs to the indentation the tab
	// renders as: without it the two rows of the paste would read as one.
	if i < 2 || plain[i-1] != ' ' || plain[i-2] != ' ' {
		t.Fatalf("the second row = %q, want it indented", plain)
	}
}

// TestPasteNormalizesCarriageReturns pins the line-ending rule of the paste:
// a statement copied from Windows arrives with "\r\n", a terminal copy may
// carry a lone "\r", and neither may stay — '\r' is a control character the
// frame would carry. Both become the break that starts a buffer line.
func TestPasteNormalizesCarriageReturns(t *testing.T) {
	m := editorModel("")
	paste := "SELECT 1\r\nFROM\rusers"
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(paste), Paste: true})

	if got, want := m.buf.Text(), "SELECT 1\nFROM\nusers"; got != want {
		t.Fatalf("the buffer = %q, want %q", got, want)
	}
	if lines := m.buf.Lines(); lines != 3 {
		t.Fatalf("buffer lines = %d, want 3", lines)
	}
}

// TestTypedKeysReachTheBufferUnchanged pins that only the paste branch is
// cleaned: typing goes through the plain-rune path of editor_keys.go, which
// stays verbatim. A keystroke is what the user meant to type, a terminal
// never sends a control character as a KeyRunes press, and cleaning there
// would change the behaviour of ordinary input for nothing.
func TestTypedKeysReachTheBufferUnchanged(t *testing.T) {
	m := editorModel("")
	m = typeText(m, "SELECT 1;")
	if got, want := m.buf.Text(), "SELECT 1;"; got != want {
		t.Fatalf("after typing = %q, want %q", got, want)
	}
	// Enter starts a new line, Tab indents by two spaces while no completion
	// popup is open: the keys around typing keep their meaning.
	m = keyByName(m, "enter")
	m = keyByName(m, "tab")
	if got, want := m.buf.Text(), "SELECT 1;\n  "; got != want {
		t.Fatalf("after enter and tab = %q, want %q", got, want)
	}
}
