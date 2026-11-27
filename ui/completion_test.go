package ui

// completion_test.go covers the popup drawing text that comes from the
// catalog: a database may put control characters into an identifier
// (PostgreSQL allows them inside a quoted name), and the popup draws the
// candidate's label and detail as plain text. The row is measured with the
// same cleaned text it is drawn with, so the block can neither carry an
// escape sequence to the terminal nor be sized for text it never shows.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/i18n"
)

// hostileTableName is an identifier of the kind a careless or hostile catalog
// hands over: an OSC 52 sequence that would write the clipboard without a
// word (the sequence TDB itself uses to copy, so a name that can set it can
// set up a paste the user believes came from the program). The printable
// tail keeps the name long enough to be the widest candidate of the popup —
// that is what makes the width assertion below tell a measured-on-the-drawn-
// text popup from one measured on the raw bytes.
const hostileTableName = "odd\x1b]52;c;AAAA\x07table12345678901"

// addHostileTable puts a relation named hostileTableName into the snapshot
// the model completes against.
func addHostileTable(m Model) {
	m.cat.Schemas[0].Tables = append(m.cat.Schemas[0].Tables, &catalog.Table{
		Schema: "public", Name: hostileTableName, Type: catalog.TypeTable,
		Columns: []*catalog.Column{{Name: "id", Type: "int"}},
	})
}

// rawPopupWidth recomputes the width render used to measure before the
// candidates were cleaned: the rule of render, applied to the raw texts.
func rawPopupWidth(m Model) int {
	ic := m.icons()
	w := lipgloss.Width(m.translator.T(i18n.KeyPopupHeader, m.popup.selected+1, len(m.popup.items)))
	for _, c := range m.popup.items[m.popup.scroll : m.popup.scroll+m.popup.rows()] {
		itemW := lipgloss.Width(ic.kind(c.Kind) + " " + c.Label + "  " + c.Detail)
		if itemW > w {
			w = itemW
		}
	}
	return w
}

// cleanedPopupWidth recomputes the width render must measure: the same rule,
// applied to the cleaned texts (see cleanedCandidates), and the same cap the
// popup has for the room of the editor.
func cleanedPopupWidth(m Model) int {
	ic := m.icons()
	w := lipgloss.Width(m.translator.T(i18n.KeyPopupHeader, m.popup.selected+1, len(m.popup.items)))
	for _, c := range m.popup.items[m.popup.scroll : m.popup.scroll+m.popup.rows()] {
		itemW := lipgloss.Width(ic.kind(c.Kind) + " " + stripControl(c.Label) + "  " + stripControl(c.Detail))
		if itemW > w {
			w = itemW
		}
	}
	if limit := m.editorWidth() - 1; w > limit {
		w = limit
	}
	return w
}

// TestCompletionPopupDrawsCleanedCandidatesAndMeasuresThem pins the whole
// popup rule at once: the escape sequence of a catalog name never reaches the
// terminal, the cleaned name is what the popup shows, and every line of the
// block is exactly as wide as the cleaned rows are — not the width the raw
// bytes would have measured.
func TestCompletionPopupDrawsCleanedCandidatesAndMeasuresThem(t *testing.T) {
	m := sized("")
	addHostileTable(m)
	m = typeText(m, "SELECT * FROM ")
	m = keyByName(m, "ctrl+space")
	if !m.popup.open {
		t.Fatal("an empty prefix must open the popup after FROM")
	}
	if len(m.popup.items) == 0 || !strings.Contains(m.popup.items[0].Label, "odd") {
		t.Fatalf("the hostile table must be the first candidate: %+v", m.popup.items[:1])
	}

	// The escape sequence must really change what the raw bytes measure, or
	// this test could not tell a popup measured on the raw text from one
	// measured on the cleaned text.
	rawW, cleanW := rawPopupWidth(m), cleanedPopupWidth(m)
	if rawW == cleanW {
		t.Fatalf("precondition failed: raw width %d == cleaned width %d, "+
			"the popup cannot drift", rawW, cleanW)
	}
	if got := cleanedPopupWidth(m); got != cleanW {
		t.Fatalf("the cleaned width must be stable: %d != %d", got, cleanW)
	}

	// Every line of the block — header, candidates, hint — is padded to the
	// width measured from the cleaned rows.
	for i, line := range m.popup.render(m.theme, m.icons(), m.translator, m.editorWidth()-1) {
		if w := lipgloss.Width(line); w != cleanW {
			t.Fatalf("popup line %d is %d columns wide, want %d (the width of the "+
				"cleaned rows): %q", i, w, cleanW, stripANSI(line))
		}
		if plain := stripANSI(line); strings.ContainsRune(plain, '\x1b') {
			t.Fatalf("popup line %d carries an escape character: %q", i, plain)
		}
	}

	view := m.View()
	assertNoPayload(t, view, "the completion popup")
	// The cleaned name is what the popup shows — the assertions above would
	// pass on a popup that dropped the candidate altogether.
	if plain := stripANSI(view); !strings.Contains(plain, "odd]52;c;AAAAtable12345678901") {
		t.Fatalf("the view must show the cleaned name:\n%s", plain)
	}
}

// TestCompletionPopupFitsANarrowPanel: on a 40-column terminal the popup is
// capped to the editor pane and clips instead of pushing the frame wider.
// The candidate rows are the ones drawn through that cap (the header and
// hint rows are only padded — a pre-existing detail this test leaves alone);
// what must hold for the screen as a whole is that no line of the frame
// overflows and no escape payload reaches it.
func TestCompletionPopupFitsANarrowPanel(t *testing.T) {
	m := sized("")
	addHostileTable(m)
	m = typeText(m, "SELECT * FROM ")
	m = keyByName(m, "ctrl+space")
	if !m.popup.open {
		t.Fatal("the popup must be open")
	}

	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	assertViewLinesFit(t, m, 40)
	assertNoPayload(t, m.View(), "the completion popup on a narrow panel")

	lines := m.popup.render(m.theme, m.icons(), m.translator, m.editorWidth()-1)
	if len(lines) < 3 {
		t.Fatalf("popup lines = %d, want header, candidates and hint", len(lines))
	}
	limit := m.editorWidth() - 1
	// lines[0] is the header, the last line is the hint, everything between
	// them is a candidate row drawn clipped to the popup's width.
	for i, line := range lines[1 : len(lines)-1] {
		if w := lipgloss.Width(line); w > limit {
			t.Fatalf("candidate row %d is %d columns wide, want at most %d: %q",
				i, w, limit, stripANSI(line))
		}
	}
}

// TestAcceptCompletionInsertsTheCleanedName: the editor draws its buffer
// verbatim, so the name a completion writes into it must be cleaned on the
// way in — a raw insert would be executed by the terminal the moment it is
// drawn.
func TestAcceptCompletionInsertsTheCleanedName(t *testing.T) {
	m := sized("")
	addHostileTable(m)
	m = typeText(m, "SELECT * FROM ")
	m = keyByName(m, "ctrl+space")
	if !m.popup.open {
		t.Fatal("the popup must be open")
	}

	m = keyByName(m, "tab")
	got := m.buf.Text()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the buffer = %q, want no escape character", got)
	}
	want := `SELECT * FROM "odd]52;c;AAAAtable12345678901"`
	if got != want {
		t.Fatalf("the buffer = %q, want %q", got, want)
	}
}
