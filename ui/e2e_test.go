package ui

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"

	// The end-to-end test runs against a real database: SQLite needs no server
	// and the driver is pure Go.
	_ "github.com/p-v-q/tdb/driver/sqlite"
)

// e2eConn opens a private in-memory SQLite database with a small schema.
func e2eConn(t *testing.T) driver.Connection {
	t.Helper()
	conn, err := driver.Open(context.Background(), "sqlite", driver.Config{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	for _, stmt := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT, age INTEGER)",
		"INSERT INTO users VALUES (1, 'ada@example.com', 'Ada', 36)",
		"INSERT INTO users VALUES (2, 'bob@example.com', NULL, NULL)",
		"INSERT INTO users VALUES (3, 'lin@example.com', 'Lin', 41)",
	} {
		if _, err := conn.Query(context.Background(), stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return conn
}

// e2eModel builds a model on the database with the caret at the end of text.
func e2eModel(t *testing.T, conn driver.Connection, text string) Model {
	t.Helper()
	m := New(Config{
		Title:      "TDB",
		ConnName:   "shop.db",
		Dialect:    conn.Dialect(),
		Provider:   conn,
		Conn:       conn,
		Text:       text,
		Translator: i18n.New(i18n.En),
	})
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 24})
	m.buf.MoveBufferEnd()
	// The browser starts folded in production; the query tests need the tree
	// available, so open the active connection explicitly.
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	return m
}

// runToCompletion sends a message and drains the commands it produces, the way
// the Bubble Tea loop would. BatchMsg is expanded here because tests call
// commands directly instead of running the event loop.
func runToCompletion(m Model, msg tea.Msg) Model {
	next, cmd := m.Update(msg)
	m = next.(Model)
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 64; steps++ {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		next, nextCmd := m.Update(msg)
		m = next.(Model)
		if nextCmd != nil {
			queue = append(queue, nextCmd)
		}
	}
	return m
}

// confirmIfAsked accepts the execution-confirmation dialog when it is open,
// the way a user presses Enter to run a DDL or destructive statement.
func confirmIfAsked(m Model) Model {
	if m.confirm.open {
		return runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	return m
}

func TestEndToEndQueryShowsRows(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email, name, age FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"id", "email", "name", "age",
		"ada@example.com", "bob@example.com", "lin@example.com",
		"3 rows"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the grid is missing %q:\n%s", want, view)
		}
	}
	// NULL is rendered as such, never as an empty cell.
	if !strings.Contains(view, "NULL") {
		t.Fatalf("NULL values must be shown as NULL:\n%s", view)
	}
	// The caret sits on the first cell and the status bar reports the run.
	if m.session.result.cursorRow != 0 || m.session.result.cursorCol != 0 {
		t.Fatalf("caret = %d,%d", m.session.result.cursorRow, m.session.result.cursorCol)
	}
}

func TestEndToEndStatementAtCaret(t *testing.T) {
	conn := e2eConn(t)
	// Two statements; the caret sits in the second one, so only it runs.
	m := e2eModel(t, conn, "SELECT 'first' AS a;\nSELECT email FROM users WHERE id = 3;")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	rows := m.session.result.res.Rows
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the single row of the second statement", rows)
	}
	if got := *rows[0][0]; got != "lin@example.com" {
		t.Fatalf("cell = %q", got)
	}
}

func TestEndToEndCtrlEnterRunsSelectedQuery(t *testing.T) {
	conn := e2eConn(t)
	text := "SELECT 'first' AS a;\nSELECT email FROM users WHERE id = 3;"
	m := e2eModel(t, conn, text)
	// Select only the second statement; Ctrl+J is what terminals commonly send
	// for Ctrl+Enter, and the key alias maps it to the Run action.
	m.sel.anchor = &textbuf.Pos{Row: 1, Col: 0}
	m.buf.SetCursor(textbuf.Pos{Row: 1, Col: len(m.buf.Line(1))})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlJ})

	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	rows := m.session.result.res.Rows
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want the single row of the selected statement", rows)
	}
	if got := *rows[0][0]; got != "lin@example.com" {
		t.Fatalf("cell = %q", got)
	}
}

func TestEndToEndScriptRunsEveryStatement(t *testing.T) {
	conn := e2eConn(t)
	// A whole script runs statement by statement: DDL + DML + the SELECT.
	m := e2eModel(t, conn, "CREATE TABLE t (x INTEGER);\nINSERT INTO t VALUES (1), (2);\nSELECT x FROM t")
	// Select everything, so the whole script is executed. The CREATE TABLE
	// is DDL, so the confirmation dialog appears once; accept it.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = confirmIfAsked(runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5}))

	if m.session.result.err != nil {
		t.Fatalf("script failed: %v", m.session.result.err)
	}
	rows := m.session.result.res.Rows
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want the two rows of the last statement", rows)
	}
	if got := m.resultSummary(m.session.result); !strings.Contains(got, "2 rows") {
		t.Fatalf("summary = %q", got)
	}
}

func TestEndToEndScriptKeepsEveryResultSet(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 'first' AS value;\nSELECT 'second' AS value")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if got := m.session.resultSetCount(); got != 2 {
		t.Fatalf("result sets = %d, want 2", got)
	}
	if m.session.resultIdx != 1 {
		t.Fatalf("active result set = %d, want the last row set", m.session.resultIdx)
	}
	if got := *m.session.result.res.Rows[0][0]; got != "second" {
		t.Fatalf("active value = %q, want second", got)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Result 2/2", "second"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view is missing %q:\n%s", want, view)
		}
	}

	// Brackets move between the results without re-running the script.
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.session.resultIdx != 0 {
		t.Fatalf("active result set = %d, want 0 after [", m.session.resultIdx)
	}
	if got := *m.session.result.res.Rows[0][0]; got != "first" {
		t.Fatalf("active value = %q, want first", got)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Result 1/2") {
		t.Fatalf("view is missing the first-set label:\n%s", view)
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.session.resultIdx != 1 {
		t.Fatalf("active result set = %d, want 1 after ]", m.session.resultIdx)
	}
}

func TestResultSetSwitchPreservesViewState(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users ORDER BY id;\nSELECT id FROM users ORDER BY id")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults

	// Filter the second set only.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(m, "3")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(m.session.result.viewRows()); got != 1 {
		t.Fatalf("filtered rows = %d, want 1", got)
	}

	// The first set has its own (unfiltered) view.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.session.result.filter != "" {
		t.Fatalf("first set inherited the filter %q", m.session.result.filter)
	}
	if got := len(m.session.result.viewRows()); got != 3 {
		t.Fatalf("first set rows = %d, want 3", got)
	}

	// Returning restores the second set's filter instead of resetting it.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if m.session.result.filter != "3" {
		t.Fatalf("second set filter = %q, want 3", m.session.result.filter)
	}
	if got := len(m.session.result.viewRows()); got != 1 {
		t.Fatalf("second set rows = %d, want 1", got)
	}
}

func TestEndToEndScriptKeepsAffectedResultSets(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "UPDATE users SET age = 99 WHERE id = 1;\nSELECT id FROM users WHERE id = 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if got := m.session.resultSetCount(); got != 2 {
		t.Fatalf("result sets = %d, want the UPDATE and the SELECT", got)
	}
	if got := len(m.session.result.res.Columns); got == 0 {
		t.Fatal("the last row set must be active")
	}

	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if got := m.session.result.res.Affected; got != 1 {
		t.Fatalf("affected rows = %d, want 1", got)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Result 1/2", "affected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view is missing %q:\n%s", want, view)
		}
	}
}

func TestEndToEndDMReportsAffectedRows(t *testing.T) {
	conn := e2eConn(t)
	// The UPDATE has no WHERE, so the confirmation dialog asks first.
	m := e2eModel(t, conn, "UPDATE users SET age = 99")
	m = confirmIfAsked(runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5}))

	if m.session.result.err != nil {
		t.Fatalf("update failed: %v", m.session.result.err)
	}
	if got := m.resultSummary(m.session.result); !strings.Contains(got, "3 rows affected") {
		t.Fatalf("summary = %q", got)
	}
}

func TestEndToEndErrorIsShownInTheGrid(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT * FROM missing_table")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err == nil {
		t.Fatal("a failing statement must be reported")
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "Query failed") || !strings.Contains(view, "missing_table") {
		t.Fatalf("the error must be visible in the results pane:\n%s", view)
	}
}

// decodeClipboard extracts the payload of the OSC 52 sequence that was written.
func decodeClipboard(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "\x1b]52;c;")
	if i < 0 {
		t.Fatalf("no OSC 52 sequence in %q", out)
	}
	payload := out[i+len("\x1b]52;c;"):]
	if j := strings.IndexAny(payload, "\x07\x1b"); j >= 0 {
		payload = payload[:j]
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode clipboard payload %q: %v", payload, err)
	}
	return string(raw)
}

func TestEndToEndCopyRow(t *testing.T) {
	buf := captureClipboard(t)
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDown}) // the second row
	if m.session.result.cursorRow != 1 {
		t.Fatalf("cursorRow = %d", m.session.result.cursorRow)
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if got := decodeClipboard(t, buf.String()); got != "2\tbob@example.com" {
		t.Fatalf("clipboard = %q, want the row as TSV", got)
	}
}

func TestEndToEndObjectBrowserKnowsTheDatabase(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1")
	// The catalog comes from the live connection, so the tree shows the real
	// table and its columns.
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	m.cat = cat
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 24})

	// Production folds schemas and resource groups; open them explicitly so the
	// table is reachable. Columns start folded too, so unfold the table before
	// looking for its fields.
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "main"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "main", group: catalog.KindTable}, true)
	m.layout.focus = paneObjects
	m.objects.sel = objectRowIndex(m, "users")
	if m.objects.sel < 0 {
		t.Fatalf("no users row in %+v", m.objectsRows())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})

	view := stripANSI(m.View())
	for _, want := range []string{"users", "id", "email"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the object browser is missing %q:\n%s", want, view)
		}
	}
}

// TestQueryRunsOnTheConnectedDatabase guards the most important rule of a
// connection manager: a statement runs against the database that is connected
// *now*, not against the one that happened to be connected at startup.
func TestQueryRunsOnTheConnectedDatabase(t *testing.T) {
	sqliteConn := e2eConn(t)
	demoConn, err := driver.Open(context.Background(), "demo", driver.Config{})
	if err != nil {
		t.Fatalf("open demo: %v", err)
	}
	defer demoConn.Close()

	// Start on the demo connection, exactly like the binary does by default.
	m := New(Config{
		ConnName: "demo", Dialect: demoConn.Dialect(), Provider: demoConn, Conn: demoConn,
		Text:       "SELECT email FROM users WHERE id = 1",
		Translator: i18n.New(i18n.En),
	})
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 24})

	// Now connect to SQLite through the object browser.
	m.applyConnection(settings.Connection{Name: "shop.db", Driver: "sqlite"}, sqliteConn, nil)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	if m.session.result.res == nil || len(m.session.result.res.Rows) != 1 {
		t.Fatalf("rows = %+v, want the one row of the SQLite database", m.session.result.res)
	}
	if got := *m.session.result.res.Rows[0][0]; got != "ada@example.com" {
		t.Fatalf("cell = %q, want the row from the connected database", got)
	}
}

func TestEndToEndExplainShowsPlan(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users WHERE id = 1")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF6})

	if m.session.result.err != nil {
		t.Fatalf("explain failed: %v", m.session.result.err)
	}
	if !strings.HasPrefix(m.session.result.stmt, "EXPLAIN ") {
		t.Fatalf("result statement = %q, want an EXPLAIN", m.session.result.stmt)
	}
	if len(m.session.result.res.Columns) == 0 {
		t.Fatal("EXPLAIN must produce a row set")
	}
}

func TestClearResultsKeepsColumnWidths(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	m.session.result.setColumnWidth(0, 17)

	m.clearResults()
	if m.session.result.res != nil || m.session.result.err != nil {
		t.Fatal("clearResults must empty the grid")
	}
	if got := m.session.result.colPrefs["id"]; got != 17 {
		t.Fatalf("column preference = %d, want 17", got)
	}
}
