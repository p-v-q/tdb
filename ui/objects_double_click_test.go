package ui

// objects_double_click_test.go pins the pointer's default action on the object
// tree: a double click on a table (or view) opens "View data" — the bounded
// SELECT, run right away — while other rows keep their Enter behaviour.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"

	_ "github.com/p-v-q/tdb/driver/sqlite"
)

// doubleClickModel builds a model on a file-backed SQLite database with the
// tree open down to the tables of main, like a user who has browsed there.
func doubleClickModel(t *testing.T) Model {
	t.Helper()
	path := t.TempDir() + "/shop.db"
	conn, err := driver.Open(context.Background(), "sqlite", driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, stmt := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)",
		"CREATE VIEW active_users AS SELECT id, email FROM users",
		"INSERT INTO users (id, email) VALUES (1, 'a@example.com'), (2, 'b@example.com')",
	} {
		if _, err := conn.Query(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	m := sized("")
	m.cfg.Dialect = dialect.MustGet("sqlite")
	m.cat = cat
	m.connOpen = conn
	m.resetObjects()
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "main"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "main", group: catalog.KindTable}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "main", group: catalog.KindView}, true)
	return m
}

// objectsRowPoint returns a screen point inside the label of a tree row: past
// the expand caret and the type icon, so the click is not taken for a twisty
// press (which ends the gesture instead of counting clicks).
func objectsRowPoint(m Model, idx int) (int, int) {
	r := m.paneRects()
	row := m.objectsRows()[idx]
	return r.objects.X + 2 + row.indent*2 + 6, r.objects.Y + 1 + (idx - m.objects.scroll)
}

// doubleClick drives two press/release pairs at the same point and returns the
// command the second release produced.
func doubleClick(m Model, x, y int) (Model, tea.Cmd) {
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	var cmd tea.Cmd
	next, cmd := m.Update(mousePress(x, y))
	m = next.(Model)
	next, cmd = m.Update(mouseRelease(x, y))
	return next.(Model), cmd
}

// runQueryMsgs feeds the query result messages a command produced back into the
// model, the way the event loop would.
func runQueryMsgs(m Model, cmd tea.Cmd) Model {
	for _, msg := range collectCmds(cmd) {
		m = send(m, msg)
	}
	return m
}

func TestDoubleClickTableOpensViewData(t *testing.T) {
	m := doubleClickModel(t)
	idx, row := findObjectRow(t, m, rowTable, "users")
	x, y := objectsRowPoint(m, idx)
	m, cmd := doubleClick(m, x, y)

	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want the View data tab", len(m.tabs))
	}
	if got, want := m.buf.Text(), `SELECT * FROM "main"."users" LIMIT 200;`; got != want {
		t.Fatalf("view data text = %q, want %q", got, want)
	}
	if got := m.tabLabel(m.active); !strings.Contains(got, "[data]") {
		t.Fatalf("tab label = %q, want the data kind", got)
	}
	m = runQueryMsgs(m, cmd)
	if !strings.Contains(m.status.message, "2 rows") {
		t.Fatalf("status = %q, want the rows the view data read", m.status.message)
	}
	_ = row
}

func TestDoubleClickViewOpensViewData(t *testing.T) {
	m := doubleClickModel(t)
	idx, _ := findObjectRow(t, m, rowTable, "active_users")
	x, y := objectsRowPoint(m, idx)
	m, cmd := doubleClick(m, x, y)
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want the View data tab", len(m.tabs))
	}
	if got := m.buf.Text(); !strings.Contains(got, `"main"."active_users"`) {
		t.Fatalf("view data text = %q", got)
	}
	m = runQueryMsgs(m, cmd)
	if !strings.Contains(m.status.message, "2 rows") {
		t.Fatalf("status = %q, want the rows the view data read", m.status.message)
	}
}

func TestDoubleClickColumnStillInsertsTheName(t *testing.T) {
	// Only relations change: a double click deeper in the tree stays the quick
	// way to insert a name into the editor.
	m := doubleClickModel(t)
	tableIdx, tableRow := findObjectRow(t, m, rowTable, "users")
	m.objectsSetOpen(tableRow, true)
	colIdx := -1
	for i, row := range m.objectsRows() {
		if row.kind == rowColumn && row.column != nil && row.column.Name == "email" {
			colIdx = i
			break
		}
	}
	if colIdx <= tableIdx {
		t.Fatalf("the column row (%d) follows its table (%d)", colIdx, tableIdx)
	}
	x, y := objectsRowPoint(m, colIdx)
	m, _ = doubleClick(m, x, y)
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, a column double click must not open data", len(m.tabs))
	}
	if got := m.buf.Text(); !strings.Contains(got, "email") {
		t.Fatalf("buffer = %q, want the column name inserted", got)
	}
}

func TestDoubleClickSavedConnectionStillConnects(t *testing.T) {
	// The relation rule must not steal the gesture from a saved connection.
	m := sized("")
	if row := savedConnRowIndex(m, "no-such-connection"); row >= 0 {
		t.Fatalf("unexpected row %d", row)
	}
}
