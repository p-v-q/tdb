package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/history"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/textbuf"
)

// historyModel is an end-to-end model with an isolated history file.
func historyModel(t *testing.T, text string) (Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	m := e2eModel(t, e2eConn(t), text)
	m.histStore = history.New(path)
	return m, path
}

func TestHistoryRecordsSuccessfulQuery(t *testing.T) {
	m, path := historyModel(t, "SELECT id FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if len(m.historyEntries) != 1 {
		t.Fatalf("history = %+v, want one entry", m.historyEntries)
	}
	entry := m.historyEntries[0]
	if !strings.Contains(entry.SQL, "SELECT id FROM users") {
		t.Fatalf("SQL = %q", entry.SQL)
	}
	if entry.Connection != "shop.db" || !entry.RowSet || entry.Rows != 3 || entry.Error != "" || entry.At.IsZero() {
		t.Fatalf("entry = %+v", entry)
	}

	// History is persisted as soon as the query completes.
	stored, err := history.New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Rows != 3 {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestHistorySurvivesRestart(t *testing.T) {
	m, path := historyModel(t, "SELECT 42")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	restarted := New(Config{
		Title:       "TDB",
		HistoryPath: path,
		Translator:  i18n.New(i18n.En),
	})
	if len(restarted.historyEntries) != 1 {
		t.Fatalf("restarted history = %+v, want one entry", restarted.historyEntries)
	}
	if got := restarted.historyEntries[0].SQL; got != "SELECT 42" {
		t.Fatalf("SQL = %q", got)
	}
}

func TestHistoryRecordsFailedQuery(t *testing.T) {
	m, _ := historyModel(t, "SELECT * FROM missing_table")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if len(m.historyEntries) != 1 {
		t.Fatalf("history = %+v", m.historyEntries)
	}
	if m.historyEntries[0].Error == "" {
		t.Fatal("a failed query must retain its error in history")
	}
}

func TestHistoryDialogOpensAndLoadsNewTab(t *testing.T) {
	m, _ := historyModel(t, "SELECT email FROM users WHERE id = 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})

	if !m.historyDlg.open {
		t.Fatal("Ctrl+H did not open query history")
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Query History (1)", "shop.db", "SELECT email"} {
		if !strings.Contains(view, want) {
			t.Fatalf("history dialog is missing %q:\n%s", want, view)
		}
	}

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.historyDlg.open {
		t.Fatal("Enter must close the history dialog")
	}
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want a new tab", len(m.tabs))
	}
	if got := m.buf.Text(); got != m.historyEntries[0].SQL {
		t.Fatalf("new tab text = %q, want %q", got, m.historyEntries[0].SQL)
	}
}

func TestHistoryDialogSwallowsEditorKeys(t *testing.T) {
	m, _ := historyModel(t, "SELECT 1")
	before := m.buf.Text()
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("xyz")})
	if got := m.buf.Text(); got != before {
		t.Fatalf("typing behind the dialog changed the editor: %q", got)
	}
	if !m.historyDlg.open {
		t.Fatal("the dialog must stay open while keys are swallowed")
	}
}

func TestHistoryCtrlEnterRunsInNewTab(t *testing.T) {
	m, _ := historyModel(t, "SELECT 7 AS answer")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlJ})

	if m.historyDlg.open {
		t.Fatal("running an entry must close the dialog")
	}
	if m.session.result.err != nil {
		t.Fatalf("history query failed: %v", m.session.result.err)
	}
	if rows := m.session.result.res.Rows; len(rows) != 1 || *rows[0][0] != "7" {
		t.Fatalf("result = %+v", rows)
	}
	if len(m.historyEntries) != 2 {
		t.Fatalf("history entries = %d, want the re-run recorded too", len(m.historyEntries))
	}
}

func TestHistoryDeletePersistsEmptyLog(t *testing.T) {
	m, path := historyModel(t, "SELECT 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDelete})

	if m.historyDlg.open || len(m.historyEntries) != 0 {
		t.Fatalf("dialog=%v entries=%+v", m.historyDlg.open, m.historyEntries)
	}
	stored, err := history.New(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored = %+v, want an empty log", stored)
	}
}

func TestHistoryMouseDoubleClickOpensEntry(t *testing.T) {
	m, _ := historyModel(t, "SELECT 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})

	l := m.historyLayout()
	click := tea.MouseMsg{
		X: l.x + 4, Y: l.listY,
		Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	}
	m = runToCompletion(m, click)
	m = runToCompletion(m, click)
	if m.historyDlg.open {
		t.Fatal("double click must open the entry and close the dialog")
	}
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want a new tab", len(m.tabs))
	}
}

func TestHistoryDialogRendersAtNarrowWidths(t *testing.T) {
	m, _ := historyModel(t, "SELECT 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	for _, width := range []int{20, 36, 80} {
		m.width = width
		if got := stripANSI(m.View()); got == "" {
			t.Fatalf("width %d produced an empty view", width)
		}
	}
}

// ---- Filtering the history ----

// runHistory runs the given statements and returns the model with the
// recorded history.
func runHistory(t *testing.T, stmts ...string) Model {
	t.Helper()
	m, _ := historyModel(t, stmts[0])
	for _, stmt := range stmts {
		m.buf = textbuf.New(stmt)
		m.buf.MoveBufferEnd()
		m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	}
	return m
}

// openHistoryFilter opens the history dialog and starts typing a filter.
func openHistoryFilter(m Model, filter string) Model {
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	return typeText(m, filter)
}

// TestHistoryFilterKeepsOnlyTheMatchingEntries checks that the filter hides
// the entries that do not match, and that the hint tells the user how to
// leave the filter again.
func TestHistoryFilterKeepsOnlyTheMatchingEntries(t *testing.T) {
	m := runHistory(t, "SELECT id FROM users", "SELECT email FROM users", "SELECT name FROM users")
	m = openHistoryFilter(m, "email")

	if got := len(m.historyVisible()); got != 1 {
		t.Fatalf("visible entries = %d, want the one that matches", got)
	}
	entry, ok := m.historySelected()
	if !ok || !strings.Contains(entry.SQL, "email") {
		t.Fatalf("selected entry = %+v, want the one that matches", entry)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, m.t(i18n.KeyHistoryFilterHint)) {
		t.Fatalf("the dialog must show the filter hint:\n%s", view)
	}
	// The entries that do not match are gone from the list.
	if strings.Contains(view, "SELECT id FROM users") {
		t.Fatalf("the list must not show the entries that do not match:\n%s", view)
	}
}

// TestHistoryFilterEscClearsTheFilter pins that Esc clears the filter
// instead of closing the dialog: the list is still wanted.
func TestHistoryFilterEscClearsTheFilter(t *testing.T) {
	m := runHistory(t, "SELECT id FROM users", "SELECT email FROM users")
	m = openHistoryFilter(m, "email")

	m = keyByName(m, "esc")
	if m.historyDlg.filter != "" {
		t.Fatalf("filter = %q, want it cleared", m.historyDlg.filter)
	}
	if !m.historyDlg.open {
		t.Fatal("Esc must not close the dialog while a filter is in effect")
	}
	if got := len(m.historyVisible()); got != 2 {
		t.Fatalf("visible entries = %d, want every entry again", got)
	}
}

// TestHistoryFilterSaysWhenNothingMatches covers the empty list: the user
// must learn that the filter hid everything, not that the log is empty.
func TestHistoryFilterSaysWhenNothingMatches(t *testing.T) {
	m := runHistory(t, "SELECT id FROM users")
	m = openHistoryFilter(m, "nothing-matches-this")

	view := stripANSI(m.View())
	if !strings.Contains(view, m.t(i18n.KeyHistoryNoMatch)) {
		t.Fatalf("the dialog must say that nothing matches:\n%s", view)
	}
}

// TestHistoryFilterOpensTheOriginalEntry pins that the filter is a view of
// the log: the entry that is opened is the stored one, not a rewritten
// statement.
func TestHistoryFilterOpensTheOriginalEntry(t *testing.T) {
	m := runHistory(t, "SELECT id FROM users", "SELECT email FROM users")
	m = openHistoryFilter(m, "email")

	// Enter leaves the filter input but keeps the filter in effect.
	m = keyByName(m, "enter")
	if m.historyDlg.filtering {
		t.Fatal("Enter must leave the filter input")
	}
	if m.historyDlg.filter != "email" {
		t.Fatalf("filter = %q, want it kept", m.historyDlg.filter)
	}
	// Enter again opens the selected entry.
	m = keyByName(m, "enter")
	if m.historyDlg.open {
		t.Fatal("Enter must close the dialog")
	}
	if got := m.buf.Text(); got != "SELECT email FROM users" {
		t.Fatalf("new tab text = %q, want the stored statement", got)
	}
}

// TestHistoryFilterMovesTheSelection checks that the arrow keys still move
// the selection while the filter is being typed: the filter is a view of the
// list, not a mode of its own.
func TestHistoryFilterMovesTheSelection(t *testing.T) {
	m := runHistory(t, "SELECT id FROM users", "SELECT email FROM users")
	m = openHistoryFilter(m, "SELECT")

	if got := len(m.historyVisible()); got != 2 {
		t.Fatalf("visible entries = %d, want both", got)
	}
	m = keyByName(m, "down")
	if m.historyDlg.sel != 1 {
		t.Fatalf("selection = %d, want the second entry", m.historyDlg.sel)
	}
	entry, ok := m.historySelected()
	if !ok {
		t.Fatal("the selection must point at an entry")
	}
	// The log is newest first, so the second entry is the older statement.
	if !strings.Contains(entry.SQL, "SELECT id FROM users") {
		t.Fatalf("selected entry = %+v, want the older statement", entry)
	}
}

// TestHistoryFilterMatchesTheConnectionName checks the second half of the
// filter: an entry is found by the connection it ran on as well.
func TestHistoryFilterMatchesTheConnectionName(t *testing.T) {
	m, _ := historyModel(t, "SELECT 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlH})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(m, "shop")

	if got := len(m.historyVisible()); got != 1 {
		t.Fatalf("visible entries = %d, want the one of that connection", got)
	}
}

// ---- Hostile text in the log ----

// The log stores what the database and the user produced, control bytes
// included: a connection name from a settings file and a statement pasted
// from another program both reach history.json as they are. The dialog
// therefore cleans every field it draws and never the entry itself — the
// tests below pin that rule from the outside: the frame must be clean and the
// text still readable, while the stored bytes stay untouched.

// hostileHistoryEntry builds an entry for those tests; over poisons the
// fields under test.
func hostileHistoryEntry(over func(*history.Entry)) history.Entry {
	e := history.Entry{
		At:         time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		Connection: "conn",
		SQL:        "SELECT 1",
		RowSet:     true,
		Rows:       3,
		DurationMS: 12,
	}
	if over != nil {
		over(&e)
	}
	return e
}

// openHostileHistory opens the dialog on a single poisoned entry.
func openHostileHistory(t *testing.T, e history.Entry) Model {
	t.Helper()
	m := dialogModel(t, 120, 40)
	m.historyEntries = []history.Entry{e}
	m.openHistoryDialog()
	if !m.historyDlg.open {
		t.Fatal("the history dialog must be open")
	}
	return m
}

// TestHistoryDialogDrawsHostileEntryFieldsCleanly covers the two entry fields
// the dialog draws from stored text: the connection name (list row and
// preview meta line) and the statement (preview rows). Both must be drawn
// cleaned — no sequence the terminal would act on — and still readable, so a
// renderer that passes by drawing nothing fails here instead.
func TestHistoryDialogDrawsHostileEntryFieldsCleanly(t *testing.T) {
	for _, c := range []struct {
		name string
		over func(*history.Entry)
		want []string
	}{
		{
			name: "connection name",
			over: func(e *history.Entry) { e.Connection = "conn " + hostileViewText },
			// stripControl drops the ESC of every sequence but keeps the
			// printable bytes around it, so the name still reads as text.
			want: []string{"conn ]52;c;AAAA"},
		},
		{
			name: "statement preview",
			over: func(e *history.Entry) { e.SQL = "SELECT '" + hostileViewText + "'" },
			// foldBreaks keeps the statement's words in place (the list row,
			// which oneLine()s the statement, would not match this head), so
			// the preview is the line this substring proves readable.
			want: []string{"SELECT ']52;c;AAAA"},
		},
		{
			// One entry poisoned in both fields, the way a hostile history
			// file carries it: both draw points must be clean at once.
			name: "connection name and statement together",
			over: func(e *history.Entry) {
				e.Connection = "conn " + hostileViewText
				e.SQL = "SELECT '" + hostileViewText + "'"
			},
			want: []string{"conn ]52;c;AAAA", "SELECT ']52;c;AAAA"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := openHostileHistory(t, hostileHistoryEntry(c.over))
			view := m.View()
			assertViewClean(t, m, "history dialog with a hostile "+c.name)
			for _, seq := range []string{escapeCellOSC, escapeCellClear} {
				if strings.Contains(view, seq) {
					t.Fatalf("the dialog drew %q into the frame:\n%s", seq, stripANSI(view))
				}
			}
			plain := stripANSI(view)
			for _, want := range c.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("the cleaned text must still be readable (want %q):\n%s", want, plain)
				}
			}
		})
	}
}

// TestHistoryFilterHintDrawsTheFilterCleanly pins the third draw point: the
// hint line shows the filter. Only the drawing is cleaned — the field keeps
// its bytes, so the filter still matches an entry that holds the same bytes.
func TestHistoryFilterHintDrawsTheFilterCleanly(t *testing.T) {
	m := openHostileHistory(t, hostileHistoryEntry(func(e *history.Entry) {
		e.SQL = "SELECT '" + hostileViewText + "'"
	}))
	m.historyDlg.filter = hostileViewText
	m.historyDlg.filtering = true

	if got := len(m.historyVisible()); got != 1 {
		t.Fatalf("visible entries = %d, want the raw filter to still match the statement", got)
	}
	hint := m.historyHint()
	if strings.ContainsRune(hint, '\x1b') {
		t.Fatalf("hint = %q, want the filter cleaned for the frame", hint)
	}
	if !strings.Contains(hint, "evil") {
		t.Fatalf("hint = %q, want the cleaned filter still readable", hint)
	}
	assertViewClean(t, m, "history dialog with a hostile filter")
	if got := stripANSI(m.View()); !strings.Contains(got, "/]52;c;AAAA") {
		t.Fatalf("the hint must show the cleaned filter:\n%s", got)
	}
}

// TestHistoryDialogFitsFortyColumnsWithHostileFields is the geometry half of
// the rule: at a narrow panel the box stays rectangular inside the pane, and
// the connection column is measured from the cleaned name — the width the
// truncation takes is the width of the bytes that are drawn, so a field whose
// escapes are dropped cannot shift the rest of the row.
func TestHistoryDialogFitsFortyColumnsWithHostileFields(t *testing.T) {
	const conn = "conn " + hostileViewText
	m := dialogModel(t, 40, 24)
	m.historyEntries = []history.Entry{hostileHistoryEntry(func(e *history.Entry) {
		e.Connection = conn
		e.SQL = "SELECT '" + hostileViewText + "'"
	})}
	m.openHistoryDialog()
	if !m.historyDlg.open {
		t.Fatal("the history dialog must be open")
	}

	l := m.historyLayout()
	x, y, block, ok := m.renderHistoryDialog()
	if !ok {
		t.Fatal("the dialog must render")
	}
	blockInsideRect(t, "history dialog", x, y, block, m.paneRects().area)
	// Both fields of the single entry are poisoned here, so the whole frame
	// is what has to come out clean.
	assertViewClean(t, m, "history dialog with hostile fields at 40 columns")

	row := stripANSI(m.renderHistoryRow(0, 0, l))
	want := padANSI(truncateANSI(stripControl(conn), l.connW), l.connW)
	if !strings.Contains(row, want) {
		t.Fatalf("the connection column must hold %q:\n%q", want, row)
	}
	if w := lipgloss.Width(row); w != l.innerW {
		t.Fatalf("row measures %d cells, want the inner width %d:\n%q", w, l.innerW, row)
	}
}

// TestHistoryDialogOpensHostileEntryVerbatim is the data side of the same
// rule: the dialog cleans what it draws, never what it holds. Enter hands the
// stored statement to the new tab byte for byte — the reason the grid, too,
// keeps the original bytes for the clipboard (see ui/results_render.go).
func TestHistoryDialogOpensHostileEntryVerbatim(t *testing.T) {
	const sql = "SELECT '" + hostileViewText + "'"
	m := openHostileHistory(t, hostileHistoryEntry(func(e *history.Entry) { e.SQL = sql }))

	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.historyDlg.open {
		t.Fatal("Enter must open the entry and close the dialog")
	}
	if got := m.buf.Text(); got != sql {
		t.Fatalf("new tab holds %q, want the stored statement %q", got, sql)
	}
}
