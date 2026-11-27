package ui

// results_render_test.go covers what the grid draws: a value (or a column
// name) comes from the database, escape sequences included, and the pane must
// never print them — while the clipboard keeps the original bytes.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/p-v-q/tdb/driver"
)

// The payloads a stored value may carry: an OSC 52 (which tells the terminal
// to set its clipboard) and a clear-screen. A terminal executes both when they
// are printed, so neither may survive into the rendered view.
const (
	escapeCellOSC   = "\x1b]52;c;AAAA\x07"
	escapeCellClear = "\x1b[2J"
)

// gridModel puts a synthetic result into a sized model with the results pane
// visible and focused: the pane under test, nothing else about the session.
func gridModel(t *testing.T, columns []driver.ResultColumn, rows [][]*string) Model {
	t.Helper()
	m := sized("SELECT 1")
	m.session.result.setResult(&driver.Result{Columns: columns, Rows: rows}, nil, "SELECT 1")
	m.layout.resultsVisible = true
	m.layout.focus = paneResults
	return m
}

// escapeCellGrid is the grid the sanitizing tests share: one column holding
// "abc", the same "abc" with escape sequences behind it, and NULL.
func escapeCellGrid(t *testing.T) Model {
	t.Helper()
	plain, tainted := "abc", "abc"+escapeCellOSC+escapeCellClear
	var null *string
	return gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&plain}, {&tainted}, {null}})
}

// resultCellWidth is the drawn width of the first cell of a rendered row:
// styles removed, the row-number gutter cut off, up to the column separator.
// It is the number the grid aligns a column on.
func resultCellWidth(m Model, row int) int {
	gutter := m.resultsGutterWidth()
	line := ansi.Strip(m.resultRow(row, row, 400, gutter))
	if len(line) < gutter {
		return -1
	}
	body := line[gutter:]
	at := strings.Index(body, "│")
	if at < 0 {
		return -1
	}
	return lipgloss.Width(body[:at])
}

// TestResultGridCellEscapesNeverReachTheView pins the sanitizing rule for the
// grid: an escape sequence stored in a value must not be printed — the
// terminal would execute it — while the cleaned text stays on screen.
func TestResultGridCellEscapesNeverReachTheView(t *testing.T) {
	m := escapeCellGrid(t)
	view := m.View()
	for _, want := range []string{escapeCellOSC, escapeCellClear} {
		if strings.Contains(view, want) {
			t.Fatalf("the view carries %q:\n%q", want, stripANSI(view))
		}
	}
	if strings.ContainsRune(stripANSI(view), '\x1b') {
		t.Fatalf("the view must hold no escape character at all:\n%q", stripANSI(view))
	}
	// The value is still drawn, cleaned: the assertions above must not pass
	// because the grid showed nothing.
	if plain := stripANSI(view); !strings.Contains(plain, "abc") ||
		!strings.Contains(plain, "]52;c;AAAA[2J") {
		t.Fatalf("the cleaned cell must stay on screen:\n%s", plain)
	}
}

// TestResultGridCellsAlignAfterSanitizing pins the order of the steps:
// sanitize first, then measure, truncate and pad. The cleaned "abc" and the
// cleaned escape-sequence cell must both be drawn on the column width, so the
// rows line up instead of drifting apart.
func TestResultGridCellsAlignAfterSanitizing(t *testing.T) {
	m := escapeCellGrid(t)
	r := m.session.result
	want := r.colWidths[0]
	if want < 1 {
		t.Fatalf("column width = %d", want)
	}
	for i := range r.res.Rows {
		if got := resultCellWidth(m, i); got != want {
			t.Fatalf("row %d cell width = %d, want the column width %d (rows must line up)",
				i, got, want)
		}
	}
}

// TestResultGridStillSpellsOutNULL is the regression guard: NULL is a spelling
// of its own, not empty text, and cleaning the cells must not touch it.
func TestResultGridStillSpellsOutNULL(t *testing.T) {
	m := escapeCellGrid(t)
	row := ansi.Strip(m.resultRow(2, 2, 400, m.resultsGutterWidth()))
	if !strings.Contains(row, "NULL") {
		t.Fatalf("the third row must still spell NULL out: %q", row)
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "NULL") {
		t.Fatalf("the view must still show NULL:\n%s", plain)
	}
}

// TestResultGridCopyKeepsTheOriginalCellBytes pins the other half of the
// product decision: the cleaning belongs to the rendering layer only. The
// clipboard gets the stored bytes, escape sequences included, so a paste into
// another program is lossless.
func TestResultGridCopyKeepsTheOriginalCellBytes(t *testing.T) {
	buf := captureClipboard(t)
	m := escapeCellGrid(t)
	m.session.result.cursorRow = 1 // the cell that carries the sequences

	cmd := m.copyResultRow()
	if cmd == nil {
		t.Fatal("copying the row must return a command")
	}
	cmd()

	want := "abc" + escapeCellOSC + escapeCellClear
	got := decodeClipboard(t, buf.String())
	if got != want {
		t.Fatalf("clipboard = %q, want the original bytes %q", got, want)
	}
	if !strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the copy must keep the escape bytes, got %q", got)
	}
}

// TestResultGridHeaderEscapesNeverReachTheView pins the same cleaning for the
// column names: an alias is the database's text too, and it sits on the same
// column width as the cells below it.
func TestResultGridHeaderEscapesNeverReachTheView(t *testing.T) {
	abc := "abc"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v" + escapeCellOSC + escapeCellClear, Type: "TEXT"}},
		[][]*string{{&abc}})

	view := m.View()
	for _, want := range []string{escapeCellOSC, escapeCellClear} {
		if strings.Contains(view, want) {
			t.Fatalf("the view carries %q:\n%q", want, stripANSI(view))
		}
	}
	if plain := stripANSI(view); !strings.Contains(plain, "v]52;c;AAAA[2J") {
		t.Fatalf("the cleaned column name must stay on screen:\n%s", plain)
	}

	// The header line as it is built: the gutter, the name padded to the
	// column width, the mark. Width 0 means no padding to the pane is added.
	gutter := m.resultsGutterWidth()
	header := ansi.Strip(m.resultHeaderRow(0))
	if got, want := lipgloss.Width(header), gutter+m.session.result.colWidths[0]+1; got != want {
		t.Fatalf("header width = %d, want gutter(%d)+column(%d)+mark(1) = %d",
			got, gutter, m.session.result.colWidths[0], gutter+m.session.result.colWidths[0]+1)
	}
	if got := resultCellWidth(m, 0); got != m.session.result.colWidths[0] {
		t.Fatalf("cell width = %d, want the column width %d (header and cells must share it)",
			got, m.session.result.colWidths[0])
	}
}

// assertGridFits pins that the grid neither overflows its pane nor leaks an
// escape sequence, at the terminal width the test set up.
func assertGridFits(t *testing.T, m Model, width int) {
	t.Helper()
	for i, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, escapeCellOSC) || strings.Contains(line, escapeCellClear) {
			t.Fatalf("view line %d carries an escape sequence:\n%q", i, line)
		}
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("view line %d is %d columns wide, want at most %d:\n%q", i, w, width, line)
		}
	}
	rect := m.paneRects().results
	for i, line := range m.renderResultBody(rect.W, rect.H-1) {
		if w := lipgloss.Width(line); w > rect.W {
			t.Fatalf("result line %d is %d columns wide, want at most %d:\n%q",
				i, w, rect.W, line)
		}
	}
}

// TestResultGridWithEscapeSequencesFitsANarrowPane: at 40 columns the grid
// must neither overflow nor panic, whether the escape-carrying cell fits the
// pane or is too wide for it and drops out of the visible columns.
func TestResultGridWithEscapeSequencesFitsANarrowPane(t *testing.T) {
	short := "abc" + escapeCellOSC + escapeCellClear
	long := strings.Repeat("y", 60) + escapeCellOSC + escapeCellClear

	// The escape-carrying cell wide enough to be drawn in a 40-column pane.
	t.Run("cell visible", func(t *testing.T) {
		m := gridModel(t,
			[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
			[][]*string{{&short}})
		m.layout.objectsVisible = false // the whole width belongs to the grid
		m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})

		assertGridFits(t, m, 40)
		if plain := stripANSI(m.View()); !strings.Contains(plain, "]52;c;AAAA[2J") {
			t.Fatalf("the cleaned cell must still be drawn:\n%s", plain)
		}
	})

	// The same cell far wider than the pane: the column drops out of the
	// visible columns instead of pushing the grid over the edge.
	t.Run("cell wider than the pane", func(t *testing.T) {
		m := gridModel(t,
			[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
			[][]*string{{&long}})
		m.layout.objectsVisible = false
		m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})

		assertGridFits(t, m, 40)
	})
}

// ---- Line breaks and tabs inside a value ----

// TestResultGridCellBreaksFoldIntoSpaces pins the third form of the cell
// cleaning: a break in a value becomes a single space, so the words on its
// two sides stay apart and the grid stays one line per row — while the
// clipboard still gets the original bytes, breaks included.
func TestResultGridCellBreaksFoldIntoSpaces(t *testing.T) {
	plain, broken := "a b", "a\nb\tc"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&plain}, {&broken}})

	if plainView := stripANSI(m.View()); !strings.Contains(plainView, "a b c") {
		t.Fatalf("the broken cell must read as separate words:\n%s", plainView)
	}
	// The folded cell is measured on the cleaned text and padded to the
	// column width, so it lines up with the row above it instead of
	// breaking out of the grid.
	want := m.session.result.colWidths[0]
	for i := range m.session.result.res.Rows {
		if got := resultCellWidth(m, i); got != want {
			t.Fatalf("row %d cell width = %d, want the column width %d (rows must line up)",
				i, got, want)
		}
	}

	// The clipboard keeps the original bytes: the cleaning belongs to the
	// rendering layer only.
	buf := captureClipboard(t)
	m.session.result.cursorRow = 1
	cmd := m.copyResultRow()
	if cmd == nil {
		t.Fatal("copying the row must return a command")
	}
	cmd()
	if got := decodeClipboard(t, buf.String()); got != "a\nb\tc" {
		t.Fatalf("clipboard = %q, want the original bytes %q", got, "a\nb\tc")
	}
}

// TestResultGridHeaderAndTypeFoldBreaks pins the same for the two header
// lines: a column named or typed with a break stays on its line and keeps
// the width the grid aligns on.
func TestResultGridHeaderAndTypeFoldBreaks(t *testing.T) {
	abc := "abc"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v\nx", Type: "NUM\nBER"}},
		[][]*string{{&abc}})

	plain := stripANSI(m.View())
	if !strings.Contains(plain, "v x") {
		t.Fatalf("the column name must fold into one line:\n%s", plain)
	}
	if !strings.Contains(plain, "NUM BER") {
		t.Fatalf("the column type must fold into one line:\n%s", plain)
	}

	// The header still spans gutter + column + separator, like every other
	// row of the grid.
	gutter := m.resultsGutterWidth()
	header := ansi.Strip(m.resultHeaderRow(0))
	if got, want := lipgloss.Width(header), gutter+m.session.result.colWidths[0]+1; got != want {
		t.Fatalf("header width = %d, want gutter(%d)+column(%d)+mark(1) = %d",
			got, gutter, m.session.result.colWidths[0], gutter+m.session.result.colWidths[0]+1)
	}
}

// TestResultGridWithBreaksFitsANarrowPane: at 40 columns a value holding
// breaks must neither overflow the pane nor lose the fold that keeps its
// words apart.
func TestResultGridWithBreaksFitsANarrowPane(t *testing.T) {
	broken := "ab\ncd\tef"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&broken}})
	m.layout.objectsVisible = false
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})

	assertGridFits(t, m, 40)
	if plain := stripANSI(m.View()); !strings.Contains(plain, "ab cd ef") {
		t.Fatalf("the folded cell must still be drawn:\n%s", plain)
	}
}

// ---- The filter and the cell editor take over the summary row ----

// TestResultFilterSeededFromACellDrawsClean pins the right-click "filter by
// this cell": the filter is seeded with the cell's stored bytes — break and
// escape sequences included — and rowMatches keeps comparing that text with
// the cells as they are stored, so the row the filter came from stays in the
// view. The input draws a cleaned copy of the very same text, folded onto
// the one line the summary row has.
func TestResultFilterSeededFromACellDrawsClean(t *testing.T) {
	plain, tainted := "abc", "a\nb"+escapeCellOSC+escapeCellClear
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&plain}, {&tainted}})

	// The menu path the user takes: the cell menu (a right click, or
	// Shift+F10 on the keyboard) offers "Filter by this value" for the cell
	// under the caret, and the action seeds the filter with the cell's
	// stored bytes.
	m.session.result.cursorRow, m.session.result.cursorCol = 1, 0
	m.openResultMenuForCursor()
	if !m.resultMenu.open {
		t.Fatal("the cell menu must open on the caret's cell")
	}
	offered := false
	for _, a := range m.resultMenu.actions {
		offered = offered || a == resultActFilterCell
	}
	if !offered {
		t.Fatalf("the menu must offer the filter action, got %v", m.resultMenu.actions)
	}
	m, _ = clickResultAction(m, resultActFilterCell)
	if m.session.result.filter == "" {
		t.Fatal("the action must seed the filter with the cell")
	}

	// The matching semantics: the filter holds the cell's bytes and
	// rowMatches compares them with the cell's bytes, so the row the filter
	// was seeded from is still the row being shown — the cleaning happens
	// only where the text is drawn.
	if view := m.session.result.viewRows(); !slices.Contains(view, 1) {
		t.Fatalf("the row the filter was seeded from must stay in the view, got %v", view)
	}

	m.layout.objectsVisible = false
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	// Reopening the input with "/" draws it clean, on one line.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.session.result.filtering {
		t.Fatal("the filter input must reopen")
	}
	row := m.resultFilterRow(40)
	if strings.Contains(row, escapeCellOSC) || strings.Contains(row, escapeCellClear) {
		t.Fatalf("the filter row carries an escape sequence: %q", row)
	}
	if strings.Contains(row, "\n") {
		t.Fatalf("the filter row must stay one line: %q", row)
	}
	assertGridFits(t, m, 40)
	if plainView := stripANSI(m.View()); !strings.Contains(plainView, "a b]52;c;AAAA[2J") {
		t.Fatalf("the cleaned filter text must be on screen:\n%s", plainView)
	}

	// Editing the query keeps matching the stored cells: a backspace leaves
	// a filter the row still passes, and the drawing stays clean.
	m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if view := m.session.result.viewRows(); !slices.Contains(view, 1) {
		t.Fatalf("the row must still match after a backspace, got %v", view)
	}
	assertGridFits(t, m, 40)
}

// TestResultEditRowDrawsCleanEvenWhenTheStateCarriesBytes pins the second
// layer of the cell editor: the row is cleaned on the way out as well, so
// the text of an open edit — data, a paste, anything a path did not clean —
// can never reach the terminal, neither in the summary row nor in the cell
// under the caret.
func TestResultEditRowDrawsCleanEvenWhenTheStateCarriesBytes(t *testing.T) {
	plain := "abc"
	m := gridModel(t,
		[]driver.ResultColumn{{Name: "v", Type: "TEXT"}},
		[][]*string{{&plain}})
	m.layout.objectsVisible = false
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	raw := plain + escapeCellOSC + escapeCellClear + "\nnext"
	m.session.result.editing = &cellEdit{row: 0, col: 0, text: raw, at: 3}

	for what, line := range map[string]string{
		"the edit row":    m.resultEditRow(80),
		"the edited cell": m.resultRow(0, 0, 200, m.resultsGutterWidth()),
	} {
		if strings.Contains(line, escapeCellOSC) || strings.Contains(line, escapeCellClear) {
			t.Fatalf("%s carries an escape sequence: %q", what, line)
		}
		if strings.Contains(line, "\n") {
			t.Fatalf("%s must stay one line: %q", what, line)
		}
	}
	assertGridFits(t, m, 40)
	view := stripANSI(m.View())
	if strings.ContainsRune(view, '\x1b') {
		t.Fatalf("the view must hold no escape character:\n%q", view)
	}
	// The cleaned text stays on screen — the assertions above must not pass
	// because the editor showed nothing.
	if !strings.Contains(view, "]52;c;AAAA[2J") || !strings.Contains(view, "next") {
		t.Fatalf("the cleaned value must be drawn:\n%s", view)
	}
}

// ---- The error of a statement ----

// TestResultGridErrorEscapesNeverReachTheView pins the third text of the
// results pane: the error of a statement is the server's own words (a
// PostgreSQL DETAIL, a MySQL message) and cannot be the place an escape
// sequence reaches the terminal — while it stays readable over the lines it
// was written on, since the wrapping builds them from its line breaks.
func TestResultGridErrorEscapesNeverReachTheView(t *testing.T) {
	m := sized("SELECT 1")
	m.session.result.setResult(nil, errors.New(
		"pq: division by zero\nDETAIL: column \""+escapeCellOSC+"\"\nHINT: "+escapeCellClear),
		"SELECT 1")
	m.layout.resultsVisible = true
	m.layout.focus = paneResults

	view := m.View()
	for _, want := range []string{escapeCellOSC, escapeCellClear} {
		if strings.Contains(view, want) {
			t.Fatalf("the view carries %q:\n%q", want, stripANSI(view))
		}
	}
	if strings.ContainsRune(stripANSI(view), '\x1b') {
		t.Fatalf("the view must hold no escape character at all:\n%q", stripANSI(view))
	}
	// The words of the server are still on screen, cleaned: the assertions
	// above must not pass because the pane showed nothing.
	if plain := stripANSI(view); !strings.Contains(plain, "division by zero") ||
		!strings.Contains(plain, "]52;c;AAAA") || !strings.Contains(plain, "[2J") {
		t.Fatalf("the cleaned error must stay on screen:\n%s", plain)
	}

	// The message keeps its lines: DETAIL and HINT must not be glued into
	// one run of words by the cleaning or by the wrapping.
	rect := m.paneRects().results
	detail, hint := -1, -1
	for i, line := range m.renderResultBody(rect.W, rect.H-1) {
		s := stripANSI(line)
		if strings.Contains(s, "DETAIL:") {
			detail = i
		}
		if strings.Contains(s, "HINT:") {
			hint = i
		}
	}
	if detail < 0 || hint < 0 {
		t.Fatalf("the error must stay readable: DETAIL at %d, HINT at %d", detail, hint)
	}
	if detail == hint {
		t.Fatalf("DETAIL and HINT must sit on different lines, both on line %d", detail)
	}
}
