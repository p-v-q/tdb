package ui

// grid_edit_safety_test.go covers the safety valves of the grid edit: the
// table a result is resolved to, the connection the changes belong to, the
// rows an UPDATE may touch, and the exit paths of the client.
//
// The rules are strict on purpose. A cell edit writes to the database without
// asking the server what it means, so everything the client cannot prove —
// which table, which connection, which row — has to be refused instead of
// guessed.

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/settings"
)

// ambiguousCatalog is a catalog with two schemas that hold a table of the
// same name — the shape of a multi-tenant database, where every tenant has
// its own schema and the same table names.
func ambiguousCatalog() *catalog.Catalog {
	users := func(schema string) *catalog.Table {
		return &catalog.Table{
			Schema: schema, Name: "users", Type: catalog.TypeTable,
			Columns: []*catalog.Column{
				{Name: "id", Type: "INTEGER", PK: true, Ordinal: 1},
				{Name: "email", Type: "TEXT", Ordinal: 2},
			},
		}
	}
	return &catalog.Catalog{
		Dialect: "postgres",
		Schemas: []*catalog.Schema{
			{Name: "app", Tables: []*catalog.Table{users("app")}},
			{Name: "public", Tables: []*catalog.Table{users("public")}},
		},
	}
}

// ---- B1: which table does the result belong to ----

// TestGridEditRefusesAnAmbiguousTableName pins the rule that keeps a cell
// edit from writing to another table: an unqualified name that several
// schemas hold is refused. The first table of that name in the catalog is
// not the table the server read — it is only the one that comes first in the
// snapshot — and an UPDATE built from it would silently write elsewhere.
func TestGridEditRefusesAnAmbiguousTableName(t *testing.T) {
	cat := ambiguousCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	_, reason := resolveSource("SELECT id, email FROM users", cat, cols, resultOrigin{})
	if reason != editBlockedAmbiguousTable {
		t.Fatalf("reason = %v, want editBlockedAmbiguousTable", reason)
	}
	// The reason has a message of its own, so the user is told what to do.
	if got := editBlockedAmbiguousTable.key(); got != i18n.KeyResultNotEditableReasonAmbiguousTable {
		t.Fatalf("key = %q, want the ambiguity key", got)
	}
	msg := i18n.New(i18n.En).T(editBlockedAmbiguousTable.key())
	if !strings.Contains(msg, "ambiguous") {
		t.Fatalf("message = %q, want it to name the ambiguity", msg)
	}
}

// TestGridEditResolvesAQualifiedTableName: a qualified name says which
// schema it means, so it is resolved exactly — and the UPDATE names that
// schema, so the confirmation dialog shows the table that is written to.
func TestGridEditResolvesAQualifiedTableName(t *testing.T) {
	cat := ambiguousCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	src, reason := resolveSource("SELECT id, email FROM app.users", cat, cols, resultOrigin{})
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	if src.table.Schema != "app" {
		t.Fatalf("schema = %q, want app", src.table.Schema)
	}
	text, _ := generateCellUpdate(dialect.MustGet("postgres"), src.table,
		src.cols[1], src.pk, gridEditString("new@example.com"), []string{"1"})
	if !strings.Contains(text, `"app"."users"`) {
		t.Fatalf("statement = %q, want the app schema", text)
	}
}

// TestGridEditResolvesTheDefaultSchema: the connection works in a schema of
// its own, and the server resolves unqualified names there first. The
// catalog must be read the same way, or the edit would address another
// schema's table.
func TestGridEditResolvesTheDefaultSchema(t *testing.T) {
	cat := ambiguousCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	origin := resultOrigin{schema: "app"}
	src, reason := resolveSource("SELECT id, email FROM users", cat, cols, origin)
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	if src.table.Schema != "app" {
		t.Fatalf("schema = %q, want the default schema app", src.table.Schema)
	}
	text, _ := generateCellUpdate(dialect.MustGet("postgres"), src.table,
		src.cols[1], src.pk, gridEditString("new@example.com"), []string{"1"})
	if !strings.Contains(text, `"app"."users"`) {
		t.Fatalf("statement = %q, want the default schema", text)
	}
}

// TestGridEditResolvesAUniqueUnqualifiedName: a name that only one schema
// holds is unambiguous, so it stays editable.
func TestGridEditResolvesAUniqueUnqualifiedName(t *testing.T) {
	cat := gridEditCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	src, reason := resolveSource("SELECT id, email FROM users", cat, cols, resultOrigin{})
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	if src.table.Schema != "main" {
		t.Fatalf("schema = %q, want main", src.table.Schema)
	}
}

// TestGridEditReportsTheAmbiguityInTheStatusBar: the user learns why the
// grid is read-only instead of pressing a key that does nothing.
func TestGridEditReportsTheAmbiguityInTheStatusBar(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	// A catalog in which two schemas hold the table.
	cat := ambiguousCatalog()
	m.cat = cat
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.src != nil {
		t.Fatal("the result must not be editable")
	}
	m.layout.focus = paneResults
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing != nil {
		t.Fatal("the cell editor must not open")
	}
	if !strings.Contains(m.status.message, "ambiguous") {
		t.Fatalf("status = %q, want the ambiguity reason", m.status.message)
	}
}

// ---- B2: which connection do the changes belong to ----

// TestGridEditDropsThePendingChangesWhenTheConnectionChanges: a pending
// change addresses a table of the database it was made in. After a switch it
// would be written to the table of the same name in the *new* database, so
// the changes are dropped.
func TestGridEditDropsThePendingChangesWhenTheConnectionChanges(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1", got)
	}

	// Another database is opened.
	other := e2eConn(t)
	otherCat, err := other.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	m.applyConnection(settings.Connection{Name: "other.db", Driver: "sqlite"}, other, otherCat)

	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0 after the switch", got)
	}
	if m.session.result.src != nil {
		t.Fatal("the source must be dropped with the connection")
	}
	if m.session.result.editing != nil {
		t.Fatal("the cell editor must be closed with the connection")
	}
	// The grid cannot be edited any more.
	m.layout.focus = paneResults
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing != nil {
		t.Fatal("the grid must not be editable after the switch")
	}
}

// TestGridEditDropsThePendingChangesOnDisconnect: the same rule for the
// connection that is closed.
func TestGridEditDropsThePendingChangesOnDisconnect(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	m.disconnect()

	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0 after the disconnect", got)
	}
	if m.session.result.src != nil {
		t.Fatal("the source must be dropped with the connection")
	}
}

// TestGridEditRefusesChangesFromAnotherConnection: even when a pending
// change survives a switch, the apply is refused — the rows were read
// through another connection, so the table they address lives in another
// database.
func TestGridEditRefusesChangesFromAnotherConnection(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	// The connection is replaced without the result being touched.
	other := e2eConn(t)
	m.connOpen = other
	m.conn = settings.Connection{Name: "other.db", Driver: "sqlite"}

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.confirm.open {
		t.Fatal("the apply must be refused, not confirmed")
	}
	if !strings.Contains(m.status.message, "another database connection") {
		t.Fatalf("status = %q, want the connection message", m.status.message)
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: the change is kept for the user", got)
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// ---- S1: a NULL cell stays a NULL ----

// TestGridEditNullCellStartsWithTheNullMarker: a NULL cell shows the marker,
// so the user sees that the cell is empty in the database sense. Pressing
// Enter without typing keeps the NULL instead of writing the empty string.
func TestGridEditNullCellStartsWithTheNullMarker(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, name FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDown})  // the row with a NULL name
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the name column

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	if got := m.session.result.editing.text; got != nullMarker {
		t.Fatalf("the editor holds %q, want the NULL marker", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	p, ok := m.session.result.pendingAt(1, 1)
	if !ok {
		t.Fatal("the change must be recorded")
	}
	if p.value != nil {
		t.Fatalf("the value must stay NULL, got %q", pendingText(p))
	}
}

// ---- S2: the primary key is not editable ----

// TestGridEditRefusesToEditThePrimaryKey: the key is what addresses the
// row. Rewriting it would move the change to another row — and a database
// that coerces the value silently could hit a row the user never saw.
func TestGridEditRefusesToEditThePrimaryKey(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	// The caret sits on the id column.

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing != nil {
		t.Fatal("the primary key must not be editable")
	}
	if !strings.Contains(m.status.message, "primary key") {
		t.Fatalf("status = %q, want the primary key message", m.status.message)
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0", got)
	}
}

// TestGridEditUpdateNeverSetsThePrimaryKey: even if a change for a key
// column reaches the generator, it produces no statement: the key is not
// part of the SET list.
func TestGridEditUpdateNeverSetsThePrimaryKey(t *testing.T) {
	cat := gridEditCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	src, reason := resolveSource("SELECT id, email FROM users", cat, cols, resultOrigin{})
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	text, args := generateCellUpdate(dialect.MustGet("postgres"), src.table,
		src.cols[0], src.pk, gridEditString("7"), []string{"1"})
	if text != "" || args != nil {
		t.Fatalf("the primary key must not be written, got %q", text)
	}
}

// ---- S3: how many rows did the UPDATE change ----

// TestGridEditReportsWhenNoRowWasAffected: an UPDATE that addresses a row by
// its key has to change exactly one row. When the row is gone, nothing
// changed — and the change is not reported as applied.
func TestGridEditReportsWhenNoRowWasAffected(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	// The row disappears before the change is applied.
	if _, err := conn.Query(context.Background(), "DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	m = confirmAndApply(m)
	if !strings.Contains(m.status.message, "did not affect any row") {
		t.Fatalf("status = %q, want the row-count message", m.status.message)
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: the change must be kept", got)
	}
}

// TestGridEditNamesTheRowsThatWereAffected: an UPDATE that changed more
// than one row did not address a single row, and the message says so.
func TestGridEditNamesTheRowsThatWereAffected(t *testing.T) {
	m := New(Config{Title: "TDB", Translator: i18n.New(i18n.En)})
	msg := queryMsg{err: &rowCountError{index: 0, affected: 3}}
	text := m.applyFailureText(msg)
	if !strings.Contains(text, "more than one row") {
		t.Fatalf("message = %q, want the row-count message", text)
	}
	if !strings.Contains(text, "primary key") {
		t.Fatalf("message = %q, want it to point at the primary key", text)
	}
}

// TestGridEditJoinsTheManualTransaction: in manual commit mode the batch
// joins the transaction of the user instead of opening one of its own, and
// it does not commit it — the user decides when the work becomes visible.
func TestGridEditJoinsTheManualTransaction(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m.txManual = true
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	m = confirmAndApply(m)

	if !m.txOpen {
		t.Fatal("the manual transaction must still be open after the apply")
	}
	if m.session.result.err != nil {
		t.Fatalf("the apply failed: %v", m.session.result.err)
	}
}

// ---- S4: the batch is atomic ----
// TestGridEditAppliesTheBatchAtomically: the second UPDATE of the batch
// fails, so the first one must not reach the database either. The batch
// runs in a transaction of its own, and a failure rolls it back.
func TestGridEditAppliesTheBatchAtomically(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	// The first change: the email of the first row.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "first@example.com")
	// The second change: the email of the second row, which the NOT NULL
	// constraint refuses.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDown})
	m = editCell(t, m, `\N`)

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	m = confirmAndApply(m)

	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value: the batch must be atomic", got)
	}
	if !strings.Contains(m.status.message, "rolled back") {
		t.Fatalf("status = %q, want the rollback message", m.status.message)
	}
	if got := m.session.result.pendingCount(); got != 2 {
		t.Fatalf("pending changes = %d, want 2: the changes must be kept", got)
	}
}

// confirmAndApply confirms the open dialog and hands the outcome of the
// batch to the model, the way the Bubble Tea loop does — without draining
// the commands, so the status message survives.
func confirmAndApply(m Model) Model {
	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		return m
	}
	msg := cmd()
	q, isQuery := msg.(queryMsg)
	if !isQuery {
		return m
	}
	return send(next, q)
}

// ---- S5: a connection that could not be loaded is closed ----

// TestConnMsgClosesTheConnectionWhenTheCatalogFails: the handshake
// succeeded and the metadata load did not. The connection is open, and
// leaving it open would leak a connection pool on every failed attempt.
func TestConnMsgClosesTheConnectionWhenTheCatalogFails(t *testing.T) {
	failed := newFakeConn()
	failed.catErr = errors.New("the catalog is broken")
	live := newFakeConn()
	m := New(Config{
		Title:      "TDB",
		Dialect:    live.Dialect(),
		Provider:   live,
		Conn:       live,
		Translator: i18n.New(i18n.En),
	})

	_, closeCmd := m.Update(connMsg{
		cfg:  settings.Connection{Name: "broken", Driver: fakeDriverName},
		conn: failed,
		err:  failed.catErr,
	})
	collectCmds(closeCmd)

	if got := failed.closeCount(); got != 1 {
		t.Fatalf("the failed connection was closed %d times, want 1: it must not leak", got)
	}
	if got := live.closeCount(); got != 0 {
		t.Fatalf("the live connection was closed %d times, want 0", got)
	}
}

// ---- S6: the exit paths are the same ----

// TestCtrlCQuitClosesTheConnection: Ctrl+C leaves the program the same way
// the File menu does: the transaction is rolled back and the connection is
// closed, so the server sees a clean disconnect.
func TestCtrlCQuitClosesTheConnection(t *testing.T) {
	fake := newFakeConn()
	m := New(Config{
		Title:      "TDB",
		Dialect:    fake.Dialect(),
		Provider:   fake,
		Conn:       fake,
		Translator: i18n.New(i18n.En),
	})
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 24})

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})

	if !m.quitting {
		t.Fatal("Ctrl+C must quit the program")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C must return the background shutdown command")
	}
	_ = cmd()
	if got := fake.closeCount(); got != 1 {
		t.Fatalf("the connection was closed %d times, want 1", got)
	}
}
