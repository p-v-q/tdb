package ui

// sanitize_test.go covers the cleaning of the text that comes from outside
// the program: a database error, a pasted fragment, a name from the catalog.
// Such a text must never carry an escape sequence into the terminal —
// lipgloss styles a string, it does not escape it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
)

// escPayload is an escape sequence of the kind a careless or hostile text
// would carry: a colour (an SGR sequence) and an unknown escape. The second
// one survives the SGR-stripping of the tests, so a leak is visible in the
// rendered view.
const escPayload = "\x1b[31m\x1bX"

// ---- The cleaning itself ----

// TestStripControlDropsTheEscapeSequences pins the rule of the cleaner: the
// control characters are gone, the rest of the text is not.
func TestStripControlDropsTheEscapeSequences(t *testing.T) {
	got := stripControl("before" + escPayload + "after")
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("stripControl = %q, want no escape character", got)
	}
	if got != "before[31mXafter" {
		t.Fatalf("stripControl = %q", got)
	}
}

// TestOneLineFoldsTheTextIntoOneLine pins that the one-line cleaner keeps the
// words of the text and drops the sequences.
func TestOneLineFoldsTheTextIntoOneLine(t *testing.T) {
	got := oneLine(" first line\nsecond\tline" + escPayload)
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("oneLine = %q, want no escape character", got)
	}
	if got != "first line second line [31m X" {
		t.Fatalf("oneLine = %q", got)
	}
}

// TestFoldBreaksFoldsBreaksIntoSpaces pins the grid's cell rule: a break is
// turned into a space, not deleted ("a\nb" must not read "ab") and not left
// as a second line (the grid draws one line per row), and every escape
// sequence is gone with the control characters.
func TestFoldBreaksFoldsBreaksIntoSpaces(t *testing.T) {
	got := foldBreaks("a\r\nb\nc\td" + escPayload)
	if got != "a b c d[31mX" {
		t.Fatalf("foldBreaks = %q", got)
	}
	if strings.ContainsAny(got, "\r\n\t\x1b") {
		t.Fatalf("foldBreaks = %q, want no control character", got)
	}
}

// TestSanitizeMultilineKeepsTheLineBreaks pins the cleaner for a message
// that is laid out over several lines: the breaks survive — wrapText builds
// the lines from them — while every other control character, the ESC of an
// escape sequence among them, becomes a space instead of vanishing, so words
// separated by a tab do not merge into one.
func TestSanitizeMultilineKeepsTheLineBreaks(t *testing.T) {
	got := sanitizeMultiline("first line\nsecond\tline" + escPayload + "\r\nthird\x07")
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("sanitizeMultiline = %q, want no escape character", got)
	}
	if got != "first line\nsecond line [31m X \nthird " {
		t.Fatalf("sanitizeMultiline = %q", got)
	}
	if n := strings.Count(got, "\n"); n != 2 {
		t.Fatalf("sanitizeMultiline = %q, want the two line breaks kept", got)
	}
}

// TestErrTextKeepsTheWordsOfTheError pins that an error stays readable after
// the cleaning: the user needs the words of the server.
func TestErrTextKeepsTheWordsOfTheError(t *testing.T) {
	got := errText(errors.New("pq: syntax error at or near \"FROM\"\nLINE 1: SELECT"))
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("errText = %q, want no escape character", got)
	}
	if !strings.Contains(got, "syntax error at or near") {
		t.Fatalf("errText = %q, want the words of the error", got)
	}
	if strings.ContainsRune(got, '\n') {
		t.Fatalf("errText = %q, want one line", got)
	}
	if errText(nil) != "" {
		t.Fatalf("errText(nil) = %q, want an empty text", errText(nil))
	}
}

// ---- The status bar ----

// TestStatusMessageOfAFailedCatalogLoadHasNoEscapeSequence pins that a
// database error cannot repaint the terminal through the status bar.
func TestStatusMessageOfAFailedCatalogLoadHasNoEscapeSequence(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, catalogMsg{err: errors.New("pq: could not connect" + escPayload)})

	if strings.ContainsRune(m.status.message, '\x1b') {
		t.Fatalf("status = %q, want no escape character", m.status.message)
	}
	if !strings.Contains(m.status.message, "could not connect") {
		t.Fatalf("status = %q, want the error text", m.status.message)
	}
}

// TestStatusMessageOfALostConnectionHasNoEscapeSequence pins the same for the
// query failure that quotes the error of the driver.
func TestStatusMessageOfALostConnectionHasNoEscapeSequence(t *testing.T) {
	conn := newFakeConn()
	conn.queryErr = errors.New("dial tcp 127.0.0.1:5432: connection refused" + escPayload)
	m := sized("SELECT 1")
	m.connOpen = conn
	m.cfg.Provider = conn

	m = runQuery(m)
	if strings.ContainsRune(m.status.message, '\x1b') {
		t.Fatalf("status = %q, want no escape character", m.status.message)
	}
	if !strings.Contains(m.status.message, "connection refused") {
		t.Fatalf("status = %q, want the error text", m.status.message)
	}
}

// TestStatusMessageOfAFailedGridApplyHasNoEscapeSequence pins the same for
// the failure of an applied grid change.
func TestStatusMessageOfAFailedGridApplyHasNoEscapeSequence(t *testing.T) {
	m := sized("SELECT 1")
	next, _ := m.finishApply(queryMsg{err: errors.New("constraint failed" + escPayload)}, nil)
	got := next.(Model).status.message

	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("status = %q, want no escape character", got)
	}
	if !strings.Contains(got, "constraint failed") {
		t.Fatalf("status = %q, want the error text", got)
	}
}

// TestStatusMessageIsCleanedWhateverItHolds pins the last net: whatever
// reaches the status bar, it cannot put an escape sequence into the
// terminal.
func TestStatusMessageIsCleanedWhateverItHolds(t *testing.T) {
	m := sized("SELECT 1")
	m.setMessage("boom" + escPayload)

	if strings.ContainsRune(m.status.message, '\x1b') {
		t.Fatalf("status = %q, want no escape character", m.status.message)
	}
	if !strings.Contains(m.status.message, "boom") {
		t.Fatalf("status = %q, want the message", m.status.message)
	}
}

// ---- The cell editor ----

// TestCellEditorDropsTheEscapeSequencesOfAPaste pins that a pasted fragment
// cannot put an escape sequence into the value being edited: the editor
// draws the text as it is.
func TestCellEditorDropsTheEscapeSequencesOfAPaste(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	m.cat = cat
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}

	// A paste arrives as one message that carries all its runes.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(escPayload + "new@example.com"), Paste: true})
	if m.session.result.editing == nil {
		t.Fatal("the paste must not close the editor")
	}
	if got := m.session.result.editing.text; strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the edited value = %q, want no escape character", got)
	}
	if !strings.Contains(m.session.result.editing.text, "new@example.com") {
		t.Fatalf("the edited value = %q, want the pasted text", m.session.result.editing.text)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}
}

// TestResultsFilterDropsTheEscapeSequencesOfAPaste pins the same for the
// filter of the result grid.
func TestResultsFilterDropsTheEscapeSequencesOfAPaste(t *testing.T) {
	m := sized("SELECT 1")
	m.layout.focus = paneResults
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.session.result.filtering {
		t.Fatal("the filter must be open")
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(escPayload + "ada"), Paste: true})
	if got := m.session.result.filter; strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the filter = %q, want no escape character", got)
	}
	if !strings.Contains(m.session.result.filter, "ada") {
		t.Fatalf("the filter = %q, want the pasted text", m.session.result.filter)
	}
}

// TestObjectFilterTitleDropsTheEscapeSequences pins the same for the filter
// of the object browser, which is drawn in the title of the pane.
func TestObjectFilterTitleDropsTheEscapeSequences(t *testing.T) {
	m := sized("SELECT 1")
	m.objects.filter = escPayload + "users"

	if got := m.objectsFilterTitle(); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the filter title = %q, want no escape character", got)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}
}

// ---- The names of the catalog ----

// TestTabLabelDropsTheControlCharactersOfACatalogName pins that a name from
// the catalog cannot put an escape sequence into the tab bar: a database may
// put control characters into an identifier.
func TestTabLabelDropsTheControlCharactersOfACatalogName(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].name = "public." + escPayload

	if got := m.tabLabel(0); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the tab label = %q, want no escape character", got)
	}
	if view := stripANSI(m.View()); strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence:\n%q", view)
	}
}

// TestContextMenuTabNameDropsTheControlCharacters pins the same for the name
// the context menu gives to the tab of a generated statement.
func TestContextMenuTabNameDropsTheControlCharacters(t *testing.T) {
	m := sized("SELECT 1")
	row := objectsRow{kind: rowTable, table: &catalog.Table{
		Schema: "public", Name: escPayload, Type: catalog.TypeTable,
	}}

	got := m.tabNameForStatement(row, objActGenSelect)
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the tab name = %q, want no escape character", got)
	}
}

// TestObjectTreeLabelDropsTheControlCharacters pins the same for the label of
// a row of the object browser.
func TestObjectTreeLabelDropsTheControlCharacters(t *testing.T) {
	m := sized("SELECT 1")
	row := objectsRow{kind: rowTable, table: &catalog.Table{
		Schema: "public", Name: escPayload, Type: catalog.TypeTable,
	}}

	got := m.renderObjectRow(row, 0, 40)
	if strings.ContainsRune(stripANSI(got), '\x1b') {
		t.Fatalf("the tree row = %q, want no escape character", got)
	}
}

// ---- The header ----

// The two sequences a hostile name carries into the header: a screen clear
// that repaints the whole terminal, and an OSC 52 that writes the clipboard
// without a word — the one TDB itself uses to copy, so a file name that can
// set it can set up a paste the user believes came from the program.
const (
	seqClearScreen = "\x1b[2J"
	seqOSC52       = "\x1b]52;c;aaaa\x07"
)

// assertNoPayload pins the frame-level rule: neither of the header payloads
// may appear in the rendered view, and no escape character may survive the
// SGR styling the frame itself produces.
func assertNoPayload(t *testing.T, view, what string) {
	t.Helper()
	if strings.Contains(view, "\x1b]52") || strings.Contains(view, seqClearScreen) {
		t.Fatalf("the view must not carry the sequence of %s:\n%q", what, view)
	}
	if got := stripANSI(view); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the view must not carry an escape sequence (%s):\n%q", what, got)
	}
}

// TestHeaderFileDropsTheEscapeSequencesOfAFileName pins that the file name of
// the header cannot repaint the terminal or write the clipboard: the menu bar
// draws the name as plain text, so the sequences of an opened ".sql" file are
// gone before the frame is built.
func TestHeaderFileDropsTheEscapeSequencesOfAFileName(t *testing.T) {
	m := sized("SELECT 1")
	m.fileName = "q" + seqOSC52 + seqClearScreen + ".sql"

	info := m.sessionInfo()
	if strings.ContainsRune(info, '\x1b') {
		t.Fatalf("the session info = %q, want no escape character", info)
	}
	if !strings.Contains(info, "q]52;c;aaaa[2J.sql") {
		t.Fatalf("the session info = %q, want the file name with the sequences dropped", info)
	}
	assertNoPayload(t, m.View(), "the file name")
}

// TestHeaderConnDropsTheEscapeSequencesOfAConnectionName pins the same for the
// connection name, in both the full summary and the shortest one the bar
// falls back to.
func TestHeaderConnDropsTheEscapeSequencesOfAConnectionName(t *testing.T) {
	m := sized("SELECT 1")
	m.cfg.ConnName = "shop" + seqOSC52 + seqClearScreen

	for _, info := range []string{m.sessionInfo(), m.sessionInfoShort(), m.sessionConnInfo()} {
		if strings.ContainsRune(info, '\x1b') {
			t.Fatalf("the session info = %q, want no escape character", info)
		}
		if !strings.Contains(info, "shop]52;c;aaaa[2J") {
			t.Fatalf("the session info = %q, want the connection name with the sequences dropped", info)
		}
	}
	view := m.View()
	assertNoPayload(t, view, "the connection name")
	// The connection opens the summary, so the cleaned name must be the part
	// of it the bar really draws — the assertions above would pass on a bar
	// that never showed it.
	if !strings.Contains(view, "conn shop]52;c;aaaa[2J") {
		t.Fatalf("the view must show the cleaned connection name:\n%s", view)
	}
}

// TestHeaderSchemaDropsTheEscapeSequencesOfADefaultSchema pins the third value
// of the header: the schema that says where unqualified names resolve.
func TestHeaderSchemaDropsTheEscapeSequencesOfADefaultSchema(t *testing.T) {
	m := sized("SELECT 1")
	m.conn.Schema = "pub" + seqOSC52 + seqClearScreen

	info := m.sessionInfo()
	if strings.ContainsRune(info, '\x1b') {
		t.Fatalf("the session info = %q, want no escape character", info)
	}
	if !strings.Contains(info, "pub]52;c;aaaa[2J") {
		t.Fatalf("the session info = %q, want the schema with the sequences dropped", info)
	}
	if got := m.sessionInfoShort(); strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the short session info = %q, want no escape character", got)
	}
	assertNoPayload(t, m.View(), "the default schema")
}
