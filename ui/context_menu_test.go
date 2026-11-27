package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/settings"
)

// rightClickObjectsRow right-clicks a row of the object tree.
func rightClickObjectsRow(m Model, idx int) Model {
	r := m.paneRects()
	x := r.objects.X + 2
	y := r.objects.Y + 1 + (idx - m.objects.scroll)
	return send(m, tea.MouseMsg{
		X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight,
	})
}

// savedConnRowIndex returns the tree row of a saved (not active) connection.
func savedConnRowIndex(m Model, name string) int {
	conns := m.connectionList()
	for i, row := range m.objectsRows() {
		if row.kind == rowConn && row.conn > 0 && conns[row.conn].Name == name {
			return i
		}
	}
	return -1
}

// contextActionIndex finds an action in the open context menu.
func contextActionIndex(m Model, want objectsMenuAction) int {
	for i, a := range m.ctx.actions {
		if a == want {
			return i
		}
	}
	return -1
}

// clickContextAction clicks an item of the open context menu.
func clickContextAction(m Model, action objectsMenuAction) (Model, tea.Cmd) {
	idx := contextActionIndex(m, action)
	if idx < 0 {
		return m, nil
	}
	r, ok := m.objectsMenuRect()
	if !ok {
		return m, nil
	}
	return sendCmd(m, mousePress(r.X+2, r.Y+1+idx))
}

// withSavedConnection adds a demo connection to the object browser and folds
// the active connection's metadata away, so both connection rows are on screen.
func withSavedConnection(m Model, name string) Model {
	m.settings.Connections = append(m.settings.Connections,
		settings.Connection{Name: name, Driver: "demo"})
	delete(m.objects.expanded, connKey(m.conn.Name))
	return m
}

func TestRightClickSavedConnectionOpensContextMenu(t *testing.T) {
	m := withSavedConnection(sized(""), "analytics")
	idx := savedConnRowIndex(m, "analytics")
	if idx < 0 {
		t.Fatal("the saved connection must be in the tree")
	}
	m = rightClickObjectsRow(m, idx)
	if !m.ctx.open {
		t.Fatal("right-clicking a connection must open its context menu")
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		"Connect", "Test connection", "Edit connection", "Delete connection",
		"New connection",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the context menu is missing %q:\n%s", want, view)
		}
	}
	if m.layout.focus != paneObjects {
		t.Fatal("right-clicking must focus the object browser")
	}
	if m.objects.sel != idx {
		t.Fatalf("the clicked row must be selected, sel = %d want %d", m.objects.sel, idx)
	}
}

func TestContextMenuTestConnection(t *testing.T) {
	m := withSavedConnection(sized(""), "analytics")
	idx := savedConnRowIndex(m, "analytics")
	m = rightClickObjectsRow(m, idx)

	next, cmd := clickContextAction(m, objActTest)
	if next.ctx.open {
		t.Fatal("choosing an action must close the context menu")
	}
	msgs := collectCmds(cmd)
	for _, msg := range msgs {
		if _, ok := msg.(connTestMsg); ok {
			next = send(next, msg)
		}
	}
	if !strings.Contains(next.status.message, "Connection OK: analytics") {
		t.Fatalf("status = %q, want a successful connection test", next.status.message)
	}
	// Testing must not switch the active session.
	if next.conn.Name != "demo" {
		t.Fatalf("active connection = %q, testing must not connect", next.conn.Name)
	}
}

func TestContextMenuTestConnectionSQLite(t *testing.T) {
	path := t.TempDir() + "/shop.db"
	conn, err := driver.Open(context.Background(), "sqlite", driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := conn.Query(context.Background(), "CREATE TABLE users (id INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	conn.Close()

	m := sized("")
	m.settings.Connections = append(m.settings.Connections,
		settings.Connection{Name: "shop", Driver: "sqlite", File: path})
	delete(m.objects.expanded, connKey(m.conn.Name))
	idx := savedConnRowIndex(m, "shop")
	m = rightClickObjectsRow(m, idx)

	next, cmd := clickContextAction(m, objActTest)
	for _, msg := range collectCmds(cmd) {
		if _, ok := msg.(connTestMsg); ok {
			next = send(next, msg)
		}
	}
	if !strings.Contains(next.status.message, "Connection OK: shop") {
		t.Fatalf("status = %q, want the SQLite file to pass the test", next.status.message)
	}
}

func TestContextMenuTestConnectionReportsFailure(t *testing.T) {
	m := withSavedConnection(sized(""), "warehouse")
	m.settings.Connections[0].Driver = "mysql" // a registered placeholder
	idx := savedConnRowIndex(m, "warehouse")
	m = rightClickObjectsRow(m, idx)

	next, cmd := clickContextAction(m, objActTest)
	for _, msg := range collectCmds(cmd) {
		if _, ok := msg.(connTestMsg); ok {
			next = send(next, msg)
		}
	}
	if !strings.Contains(next.status.message, "not implemented") {
		t.Fatalf("status = %q, want the pending-driver hint", next.status.message)
	}
}

func TestContextMenuEditPrefillsTheForm(t *testing.T) {
	m := withSavedConnection(sized(""), "analytics")
	m.settings.Connections[0].Host = "db.example"
	idx := savedConnRowIndex(m, "analytics")
	m = rightClickObjectsRow(m, idx)

	next, _ := clickContextAction(m, objActEdit)
	if !next.connDlg.open || !next.connDlg.editing {
		t.Fatal("Edit connection must open the form in editing mode")
	}
	if next.connDlg.originalName != "analytics" {
		t.Fatalf("originalName = %q", next.connDlg.originalName)
	}
	if got := next.connDlg.fields[0].value; got != "analytics" {
		t.Fatalf("name field = %q, want analytics", got)
	}
	view := stripANSI(next.View())
	if !strings.Contains(view, "Edit connection") || !strings.Contains(view, "Save") {
		t.Fatalf("the edit form must say Edit connection / Save:\n%s", view)
	}
}

func TestEditConnectionRenamesAndSaves(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := withSavedConnection(sized(""), "analytics")
	m.settingsPath = path

	m.openConnDialogFor(m.settings.Connections[0])
	m.connDlg.fields[0].value = "analytics-2"
	next, cmd, _ := m.submitConnDialog()
	collectCmds(cmd)
	if next.connDlg.open {
		t.Fatal("saving must close the form")
	}
	if !strings.Contains(next.status.message, "Connection saved: analytics-2") {
		t.Fatalf("status = %q", next.status.message)
	}
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Name != "analytics-2" {
		t.Fatalf("saved connections = %+v, want the renamed one only", got.Connections)
	}
}

func TestContextMenuDeleteRemovesTheConnection(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	m := withSavedConnection(sized(""), "analytics")
	m.settingsPath = path
	idx := savedConnRowIndex(m, "analytics")
	m = rightClickObjectsRow(m, idx)

	next, cmd := clickContextAction(m, objActDelete)
	collectCmds(cmd) // run the settings write
	if !strings.Contains(next.status.message, "deleted") {
		t.Fatalf("status = %q", next.status.message)
	}
	if savedConnRowIndex(next, "analytics") >= 0 {
		t.Fatal("the deleted connection must leave the tree")
	}
	got, err := settings.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Connections) != 0 {
		t.Fatalf("saved connections = %+v, want none", got.Connections)
	}
}

func TestContextMenuOnTableInsertsItsName(t *testing.T) {
	m := sized("")
	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("no users table in the tree")
	}
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActInsertName) < 0 {
		t.Fatalf("a table menu must offer Insert name, got %v", m.ctx.actions)
	}
	next, _ := clickContextAction(m, objActInsertName)
	if got := next.buf.Text(); got != "users" {
		t.Fatalf("inserted text = %q, want users", got)
	}
	if next.layout.focus != paneEditor {
		t.Fatal("inserting must return the focus to the editor")
	}
}

// demoUsersSQLText is the exact starter SQL for the demo users table. The
// expectations are spelled out rather than recomputed, so a regression in the
// generator cannot make the menu test agree with it by construction.
var demoUsersSQLText = map[objectsMenuAction]string{
	objActGenSelect: `SELECT "id", "email", "full_name", "status", "country_code", "created_at", "last_login_at"
FROM "public"."users";`,
	objActGenInsert: `INSERT INTO "public"."users" ("id", "email", "full_name", "status", "country_code", "created_at", "last_login_at")
VALUES ($1, $2, $3, $4, $5, $6, $7);`,
	objActGenUpdate: `UPDATE "public"."users"
SET "email" = $1, "full_name" = $2, "status" = $3, "country_code" = $4, "created_at" = $5, "last_login_at" = $6
WHERE "id" = $7;`,
	objActGenDelete: `DELETE FROM "public"."users"
WHERE "id" = $1;`,
	objActViewData: `SELECT * FROM "public"."users" LIMIT 200;`,
}

func TestContextMenuOnTableOffersSQLGenerationActions(t *testing.T) {
	m := sized("")
	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatal("no users table in the tree")
	}
	m = rightClickObjectsRow(m, idx)
	want := []objectsMenuAction{
		objActInsertName, objActCopyName,
		objActGenSelect, objActGenInsert, objActGenUpdate, objActGenDelete, objActViewData,
	}
	if !slices.Equal(m.ctx.actions, want) {
		t.Fatalf("table actions = %v, want %v", m.ctx.actions, want)
	}
	view := stripANSI(m.View())
	for _, label := range []string{"Generate SELECT", "Generate INSERT", "Generate UPDATE", "Generate DELETE", "View Data"} {
		if !strings.Contains(view, label) {
			t.Fatalf("the table menu is missing %q:\n%s", label, view)
		}
	}
}

func TestContextMenuOnViewsOffersOnlyReadActions(t *testing.T) {
	for _, name := range []string{"v_daily_sales", "mv_sales_daily"} {
		t.Run(name, func(t *testing.T) {
			m := sized("")
			idx, _ := findObjectRow(t, m, rowTable, name)
			m.objects.sel = idx
			m.scrollObjectsToSelection(m.objectsBodyHeight())
			m = rightClickObjectsRow(m, idx)
			want := []objectsMenuAction{
				objActInsertName, objActCopyName, objActGenSelect, objActViewData,
			}
			if !slices.Equal(m.ctx.actions, want) {
				t.Fatalf("%s actions = %v, want %v", name, m.ctx.actions, want)
			}
		})
	}
}

func TestContextMenuGenerateActionsOpenTheStatementInANewTab(t *testing.T) {
	cases := []struct {
		name   string
		action objectsMenuAction
	}{
		{"select", objActGenSelect},
		{"insert", objActGenInsert},
		{"update", objActGenUpdate},
		{"delete", objActGenDelete},
		{"view data", objActViewData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := sized("")
			idx := objectRowIndex(m, "users")
			m = rightClickObjectsRow(m, idx)
			next, cmd := clickContextAction(m, tc.action)
			if next.ctx.open {
				t.Fatal("choosing an action must close the context menu")
			}
			collectCmds(cmd)
			if len(next.tabs) != 2 {
				t.Fatalf("tabs = %d, want the generated SQL in a second tab", len(next.tabs))
			}
			if got, want := next.buf.Text(), demoUsersSQLText[tc.action]; got != want {
				t.Fatalf("generated text = %q, want %q", got, want)
			}
			if next.layout.focus != paneEditor {
				t.Fatal("a generated statement must focus the editor")
			}
		})
	}
}

func TestContextMenuViewDataExecutesTheBoundedSelect(t *testing.T) {
	path := t.TempDir() + "/shop.db"
	conn, err := driver.Open(context.Background(), "sqlite", driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, stmt := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)",
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

	idx, _ := findObjectRow(t, m, rowTable, "users")
	m = rightClickObjectsRow(m, idx)
	next, cmd := clickContextAction(m, objActViewData)
	if got, want := next.buf.Text(), `SELECT * FROM "main"."users" LIMIT 200;`; got != want {
		t.Fatalf("view data text = %q, want %q", got, want)
	}
	if len(next.tabs) != 2 {
		t.Fatalf("tabs = %d, want the query in a second tab", len(next.tabs))
	}

	found := false
	for _, msg := range collectCmds(cmd) {
		if qm, ok := msg.(queryMsg); ok {
			found = true
			next = send(next, qm)
		}
	}
	if !found {
		t.Fatal("View Data must execute the generated SELECT")
	}
	if !strings.Contains(next.status.message, "2 rows") {
		t.Fatalf("status = %q, want the two rows read from sqlite", next.status.message)
	}
}

func TestContextMenuGenerateFailureDoesNotOpenATab(t *testing.T) {
	m := sized("")
	// An UPDATE template cannot be produced without columns, even though a
	// hand-built row might still offer the action.
	m.ctx.row = objectsRow{
		kind:  rowTable,
		table: &catalog.Table{Schema: "public", Name: "empty", Type: catalog.TypeTable},
	}
	cmd := m.runObjectsMenuAction(objActGenUpdate)
	collectCmds(cmd)
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, a failed generation must not open a tab", len(m.tabs))
	}
	if !strings.Contains(m.status.message, "Cannot generate SQL") {
		t.Fatalf("status = %q, want the generation failure message", m.status.message)
	}
}

func TestObjectsMenuFitsOnFortyRowTerminal(t *testing.T) {
	m := newTestModel("")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m.connOpen = demoDefinitionConn(t) // adds View definition and Refresh
	idx := objectRowIndex(m, "users")
	m = rightClickObjectsRow(m, idx)
	if len(m.ctx.actions) != 9 {
		t.Fatalf("actions = %d, want 9 (insert, copy, 5 generate/view actions, definition, refresh)", len(m.ctx.actions))
	}
	r, ok := m.objectsMenuRect()
	if !ok {
		t.Fatal("the context menu must have a rectangle")
	}
	if r.Y < 0 || r.Y+r.H > m.height {
		t.Fatalf("menu rect %+v does not fit in a %d-row terminal", r, m.height)
	}
}

func TestShiftF10OpensTheContextMenu(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = objectRowIndex(m, "users")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+f10")})
	if !m.ctx.open {
		t.Fatal("Shift+F10 must open the selected row's context menu")
	}
	m = keyByName(m, "esc")
	if m.ctx.open {
		t.Fatal("Esc must close the context menu")
	}
}

// TestF13OpensTheObjectsContextMenu pins the terminal reality: a terminal
// sends Shift+F10 as the F13 sequence ("\x1b[25~", "\x1b[1;2P"), which Bubble
// Tea decodes as KeyF13. The application therefore has to treat F13 as the
// documented Shift+F10.
func TestF13OpensTheObjectsContextMenu(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = objectRowIndex(m, "users")
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if !m.ctx.open {
		t.Fatal("F13 must open the selected row's context menu")
	}
	// The same key closes the menu again.
	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if m.ctx.open {
		t.Fatal("F13 must close the context menu")
	}
}

func TestDoubleClickSavedConnectionConnects(t *testing.T) {
	m := withSavedConnection(sized(""), "analytics")
	idx := savedConnRowIndex(m, "analytics")
	if idx < 0 {
		t.Fatal("the saved connection must be in the tree")
	}
	r := m.paneRects()
	x := r.objects.X + 12 // the label, not the twisty
	y := r.objects.Y + 1 + (idx - m.objects.scroll)

	// A double click is two presses at the same spot; the command is returned
	// on the second release.
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	next, cmd := sendCmd(m, mouseRelease(x, y))
	if cmd == nil {
		t.Fatal("double-clicking a saved connection must start opening it")
	}
	for _, msg := range collectCmds(cmd) {
		next = send(next, msg)
	}
	if next.conn.Name != "analytics" {
		t.Fatalf("active connection = %q, want analytics", next.conn.Name)
	}
}

func TestEnterSavedConnectionConnects(t *testing.T) {
	m := withSavedConnection(sized(""), "analytics")
	idx := savedConnRowIndex(m, "analytics")
	if idx < 0 {
		t.Fatal("the saved connection must be in the tree")
	}
	m.layout.focus = paneObjects
	m.objects.sel = idx

	next, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on a saved connection must start opening it")
	}
	for _, msg := range collectCmds(cmd) {
		next = send(next, msg)
	}
	if next.conn.Name != "analytics" {
		t.Fatalf("active connection = %q, want analytics", next.conn.Name)
	}
}

func TestContextMenuOnResourceInsertsItsName(t *testing.T) {
	m := sized("")
	idx, _ := findObjectRow(t, m, rowResource, "order_total")
	m = rightClickObjectsRow(m, idx)
	if !m.ctx.open {
		t.Fatal("right-clicking a function must open its context menu")
	}
	if contextActionIndex(m, objActInsertName) < 0 {
		t.Fatalf("a function menu must offer Insert name, got %v", m.ctx.actions)
	}
	next, _ := clickContextAction(m, objActInsertName)
	if got := next.buf.Text(); got != "order_total" {
		t.Fatalf("inserted text = %q, want the function name", got)
	}
}

// demoDefinitionConn opens a live demo connection: it implements
// driver.DefinitionProvider, which is exactly what the menu visibility of
// "View definition" hinges on.
func demoDefinitionConn(t *testing.T) driver.Connection {
	t.Helper()
	conn, err := driver.Open(context.Background(), "demo", driver.Config{})
	if err != nil {
		t.Fatalf("open demo: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// failingDefConn is a live connection whose DDL fetch always fails.
type failingDefConn struct {
	driver.Connection
	err error
}

func (c failingDefConn) Definition(context.Context, driver.DefinitionRef) (string, error) {
	return "", c.err
}

func TestViewDefinitionMenuNeedsAProvider(t *testing.T) {
	m := sized("")
	idx, _ := findObjectRow(t, m, rowTable, "users")

	// Without a live connection the action is absent, not failing on click.
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActViewDef) >= 0 {
		t.Fatalf("a menu without a connection must not offer View definition: %v", m.ctx.actions)
	}

	// A connection that implements driver.DefinitionProvider gets the item.
	m = sized("")
	m.connOpen = demoDefinitionConn(t)
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActViewDef) < 0 {
		t.Fatalf("a DefinitionProvider connection must offer View definition: %v", m.ctx.actions)
	}
	if !strings.Contains(stripANSI(m.View()), "View definition") {
		t.Fatalf("the menu must render the localized View definition label:\n%s", stripANSI(m.View()))
	}

	// A schema is not an object with a definition, provider or not.
	for i, row := range m.objectsRows() {
		if row.kind != rowSchema {
			continue
		}
		// The previous right click left its menu open; a right click inside it
		// dismisses the menu rather than reaching the row underneath.
		m.closeObjectsMenu()
		m = rightClickObjectsRow(m, i)
		if contextActionIndex(m, objActViewDef) >= 0 {
			t.Fatalf("a schema menu must not offer View definition: %v", m.ctx.actions)
		}
		break
	}
}

func TestViewDefinitionOpensTheDDLInANewTab(t *testing.T) {
	path := t.TempDir() + "/shop.db"
	conn, err := driver.Open(context.Background(), "sqlite", driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Query(context.Background(),
		"CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}

	m := sized("")
	m.cat = cat
	m.connOpen = conn
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "main"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "main", group: catalog.KindTable}, true)

	idx, _ := findObjectRow(t, m, rowTable, "users")
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActViewDef) < 0 {
		t.Fatalf("the sqlite menu must offer View definition: %v", m.ctx.actions)
	}
	next, cmd := clickContextAction(m, objActViewDef)
	if !strings.Contains(next.status.message, "Loading definition of users") {
		t.Fatalf("status = %q, want the loading message", next.status.message)
	}
	found := false
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			found = true
			next = send(next, dm)
		}
	}
	if !found {
		t.Fatal("the click must fetch the definition in the background")
	}
	if len(next.tabs) != 2 {
		t.Fatalf("tabs = %d, want the definition in a second tab", len(next.tabs))
	}
	if got := next.buf.Text(); !strings.Contains(got, "CREATE TABLE users") {
		t.Fatalf("the new tab contains %q, want the table's DDL", got)
	}
	if next.layout.focus != paneEditor {
		t.Fatal("the definition tab must be focused for copying and editing")
	}
	if !strings.Contains(next.status.message, "Definition of users opened") {
		t.Fatalf("status = %q, want the opened message", next.status.message)
	}
}

func TestViewDefinitionOnAResource(t *testing.T) {
	m := sized("")
	m.connOpen = demoDefinitionConn(t)
	idx, _ := findObjectRow(t, m, rowResource, "order_total")
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActViewDef) < 0 {
		t.Fatalf("a function menu must offer View definition: %v", m.ctx.actions)
	}
	next, cmd := clickContextAction(m, objActViewDef)
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			next = send(next, dm)
		}
	}
	if got := next.buf.Text(); !strings.Contains(got, "CREATE FUNCTION public.order_total") {
		t.Fatalf("the definition tab contains %q, want the function DDL", got)
	}
}

func TestViewDefinitionReportsFailure(t *testing.T) {
	m := sized("")
	m.connOpen = failingDefConn{err: errors.New("boom")}
	idx, _ := findObjectRow(t, m, rowTable, "users")
	m = rightClickObjectsRow(m, idx)
	next, cmd := clickContextAction(m, objActViewDef)
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			next = send(next, dm)
		}
	}
	if !strings.Contains(next.status.message, "Failed to load definition: boom") {
		t.Fatalf("status = %q, want the localized failure", next.status.message)
	}
	if len(next.tabs) != 1 {
		t.Fatalf("tabs = %d, a failed fetch must not open a tab", len(next.tabs))
	}
}

// TestViewDefinitionDropsAResultFromAStaleConnection pins the guard on
// definitionMsg: the fetch runs in the background, and by the time it reports
// back the user may have switched databases. The stale outcome — success and
// failure alike — must be dropped instead of opening a tab with the old
// connection's DDL (stealing the focus, and inviting the user to run it
// against the wrong database) or reporting an error from the old session.
func TestViewDefinitionDropsAResultFromAStaleConnection(t *testing.T) {
	m := sized("")
	m.connOpen = demoDefinitionConn(t)
	idx, _ := findObjectRow(t, m, rowTable, "users")
	m = rightClickObjectsRow(m, idx)
	next, cmd := clickContextAction(m, objActViewDef)

	var stale definitionMsg
	found := false
	for _, msg := range collectCmds(cmd) {
		if dm, ok := msg.(definitionMsg); ok {
			found = true
			stale = dm
		}
	}
	if !found {
		t.Fatal("the click must fetch the definition in the background")
	}
	if stale.conn != m.connOpen {
		t.Fatal("the message must carry the connection it was fetched from")
	}
	loading := next.status.message

	// The user switches databases while the fetch is in flight.
	next.connOpen = failingDefConn{err: errors.New("other connection")}

	after := send(next, stale) // a successful result from the old connection
	if len(after.tabs) != 1 {
		t.Fatalf("tabs = %d, a stale definition must not open a tab", len(after.tabs))
	}
	if after.status.message != loading {
		t.Fatalf("status = %q, a stale definition must not touch the status bar", after.status.message)
	}

	stale.ddl, stale.err = "", errors.New("boom") // a stale failure
	after = send(next, stale)
	if len(after.tabs) != 1 {
		t.Fatalf("tabs = %d, a stale failure must not open a tab", len(after.tabs))
	}
	if strings.Contains(after.status.message, "boom") {
		t.Fatalf("status = %q, a stale failure must not be reported", after.status.message)
	}
}

// TestContextMenuTestConnectionReportsMissingPasswordEnv pins that the
// message about a missing password reaches the status bar. The check used to
// run on a copy of the model, so the status bar kept saying "Testing
// connection…" while the real message was lost.
func TestContextMenuTestConnectionReportsMissingPasswordEnv(t *testing.T) {
	t.Setenv("TDB_TEST_MISSING_PASSWORD", "")
	m := withSavedConnection(sized(""), "analytics")
	m.settings.Connections[0].PasswordEnv = "TDB_TEST_MISSING_PASSWORD"
	idx := savedConnRowIndex(m, "analytics")
	m = rightClickObjectsRow(m, idx)

	next, cmd := clickContextAction(m, objActTest)
	if !strings.Contains(next.status.message, "TDB_TEST_MISSING_PASSWORD") {
		t.Fatalf("status = %q, want the missing-variable message", next.status.message)
	}
	// The message must survive the command the action returned: it is the
	// only report the user gets.
	for _, msg := range collectCmds(cmd) {
		if _, ok := msg.(connTestMsg); ok {
			next = send(next, msg)
		}
	}
	if !strings.Contains(next.status.message, "TDB_TEST_MISSING_PASSWORD") {
		t.Fatalf("status = %q, want the missing-variable message", next.status.message)
	}
}

// TestContextMenuOffersTheLifecycleOfTheLiveConnection checks which rows of
// the object browser offer Reconnect and Disconnect: only the live
// connection has a lifecycle to manage.
func TestContextMenuOffersTheLifecycleOfTheLiveConnection(t *testing.T) {
	// The active connection with a live session: both actions are offered.
	m := sized("")
	m.connOpen = demoDefinitionConn(t)
	m = rightClickObjectsRow(m, 0)
	if contextActionIndex(m, objActReconnect) < 0 {
		t.Fatalf("the live connection must offer Reconnect: %v", m.ctx.actions)
	}
	if contextActionIndex(m, objActDisconnect) < 0 {
		t.Fatalf("the live connection must offer Disconnect: %v", m.ctx.actions)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Reconnect", "Disconnect"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the context menu is missing %q:\n%s", want, view)
		}
	}

	// A saved connection is not connected, so it has no lifecycle to manage.
	m = withSavedConnection(sized(""), "analytics")
	idx := savedConnRowIndex(m, "analytics")
	m = rightClickObjectsRow(m, idx)
	if contextActionIndex(m, objActReconnect) >= 0 || contextActionIndex(m, objActDisconnect) >= 0 {
		t.Fatalf("a saved connection must not offer Reconnect or Disconnect: %v", m.ctx.actions)
	}

	// A disconnected session can be opened again, but has nothing to close.
	m = sized("")
	m.connOpen = nil
	m = rightClickObjectsRow(m, 0)
	if contextActionIndex(m, objActReconnect) >= 0 || contextActionIndex(m, objActDisconnect) >= 0 {
		t.Fatalf("a closed connection must not offer Reconnect or Disconnect: %v", m.ctx.actions)
	}
	if contextActionIndex(m, objActConnect) < 0 {
		t.Fatalf("a closed connection must offer Connect: %v", m.ctx.actions)
	}
}

// TestDisconnectCanBeFollowedByAConnect walks the round trip: the session is
// closed, and the connection can be opened again from the object browser.
// Without the Connect action on a closed session there would be no way back
// from the browser.
func TestDisconnectCanBeFollowedByAConnect(t *testing.T) {
	fresh := newFakeConn()
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		return fresh, nil
	})

	conn := newFakeConn()
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "shop", Driver: fakeDriverName}

	m = rightClickObjectsRow(m, 0)
	next, cmd := clickContextAction(m, objActDisconnect)
	collectCmds(cmd)
	if next.connOpen != nil {
		t.Fatal("the connection must be closed")
	}

	// The closed session offers to be opened again.
	m = rightClickObjectsRow(next, 0)
	if contextActionIndex(m, objActConnect) < 0 {
		t.Fatalf("a closed session must offer Connect: %v", m.ctx.actions)
	}
	next, cmd = clickContextAction(m, objActConnect)
	for _, msg := range collectCmds(cmd) {
		if cm, ok := msg.(connMsg); ok {
			next = send(next, cm)
		}
	}
	if next.connOpen != fresh {
		t.Fatal("the connection must be open again")
	}
	if !strings.Contains(next.status.message, "Connected to shop") {
		t.Fatalf("status = %q, want the connected message", next.status.message)
	}
}
