package ui

// grid_edit_test.go covers the editing of a cell in the result grid: which
// results may be edited at all, how a typed value becomes a pending change,
// how the pending changes become parameterized UPDATE statements, and how the
// confirmation dialog and the read-only gate stand between the user and the
// database.

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/dialect"
	"github.com/p-v-q/tdb/driver"
)

// gridEditCatalog is a small catalog with the three shapes the editability
// rules care about: a table with a single-column primary key, a table without
// any, and a table with a composite primary key.
func gridEditCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Dialect: "postgres",
		Schemas: []*catalog.Schema{{
			Name: "main",
			Tables: []*catalog.Table{
				{
					Schema: "main", Name: "users", Type: catalog.TypeTable,
					Columns: []*catalog.Column{
						{Name: "id", Type: "INTEGER", PK: true, Ordinal: 1},
						{Name: "email", Type: "TEXT", Ordinal: 2},
					},
				},
				{
					Schema: "main", Name: "notes", Type: catalog.TypeTable,
					Columns: []*catalog.Column{
						{Name: "body", Type: "TEXT", Ordinal: 1},
					},
				},
				{
					Schema: "main", Name: "events", Type: catalog.TypeTable,
					Columns: []*catalog.Column{
						{Name: "room", Type: "TEXT", PK: true, Ordinal: 1},
						{Name: "seq", Type: "INTEGER", PK: true, Ordinal: 2},
						{Name: "note", Type: "TEXT", Ordinal: 3},
					},
				},
			},
		}},
	}
}

// gridEditModel builds a model on a real SQLite database with the catalog
// loaded: the grid can only be edited when it knows which table the rows came
// from.
func gridEditModel(t *testing.T, conn driver.Connection, text string) Model {
	t.Helper()
	m := e2eModel(t, conn, text)
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	m.cat = cat
	return m
}

// gridEditResult runs the statement of the model and moves the focus into the
// grid, so that the grid keys reach the result.
func gridEditResult(t *testing.T, m Model) Model {
	t.Helper()
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	if m.session.result.src == nil {
		t.Fatal("the result must be editable")
	}
	m.layout.focus = paneResults
	return m
}

// clearCellEdit removes the text the cell editor starts with, so that the
// test types a fresh value instead of appending to the old one.
func clearCellEdit(t *testing.T, m Model) Model {
	t.Helper()
	if m.session.result.editing == nil {
		t.Fatal("no cell editor is open")
	}
	n := len([]rune(m.session.result.editing.text))
	for i := 0; i < n; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	if got := m.session.result.editing.text; got != "" {
		t.Fatalf("the cell editor still holds %q", got)
	}
	return m
}

// editCell opens the cell editor on the cell under the caret, replaces its
// content and commits the change with Enter.
func editCell(t *testing.T, m Model, text string) Model {
	t.Helper()
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	m = clearCellEdit(t, m)
	m = typeText(m, text)
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.session.result.editing != nil {
		t.Fatal("Enter must close the cell editor")
	}
	return m
}

// userEmail reads the email of a user from the database: the tests check what
// the database holds, not what the grid believes.
func userEmail(t *testing.T, conn driver.Connection, id int) string {
	t.Helper()
	res, err := conn.Query(context.Background(), "SELECT email FROM users WHERE id = ?", id)
	if err != nil {
		t.Fatalf("read email: %v", err)
	}
	if res == nil || len(res.Rows) != 1 || len(res.Rows[0]) == 0 || res.Rows[0][0] == nil {
		t.Fatalf("unexpected result: %+v", res)
	}
	return *res.Rows[0][0]
}

// gridEditString is a helper for the statement generators, which take the
// value of a cell as a pointer (nil means SQL NULL).
func gridEditString(s string) *string { return &s }

// pendingText renders the value of a pending change for a failure message.
func pendingText(p pendingEdit) string {
	if p.value == nil {
		return "NULL"
	}
	return *p.value
}

// ---- Which results may be edited ----

// TestGridEditAllowsASingleTableSelect pins the positive case: a select over
// exactly one table whose primary key is part of the result can be edited.
func TestGridEditAllowsASingleTableSelect(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	src := m.session.result.src
	if src == nil {
		t.Fatal("a single-table select with the key in the result must be editable")
	}
	if src.table.Name != "users" {
		t.Fatalf("table = %q, want users", src.table.Name)
	}
	if len(src.pk) != 1 || src.pk[0].Name != "id" {
		t.Fatalf("primary key = %+v, want the id column", src.pk)
	}
	if len(src.keyIdx) != 1 || src.keyIdx[0] != 0 {
		t.Fatalf("key indexes = %v, want [0]", src.keyIdx)
	}
	if col := src.cols[1]; col == nil || col.Name != "email" {
		t.Fatalf("the email column must be mapped to the table column, got %+v", col)
	}
}

// TestGridEditAllowsClausesAfterTheTable: the clauses that follow the table
// reference — a filter, an order, a limit — do not hide the relation, so the
// result stays editable.
func TestGridEditAllowsClausesAfterTheTable(t *testing.T) {
	cat := gridEditCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	for _, stmt := range []string{
		"SELECT id, email FROM users",
		"SELECT id, email FROM users ORDER BY id",
		"SELECT id, email FROM users WHERE id = 1",
		"SELECT id, email FROM users LIMIT 10",
		"SELECT id, email FROM users u WHERE u.id = 1",
		"SELECT u.id, u.email FROM users u ORDER BY u.id",
		"SELECT id, email FROM users;",
		"SELECT id, email FROM main.users",
	} {
		if _, reason := resolveSource(stmt, cat, cols, resultOrigin{}); reason != editAllowed {
			t.Errorf("%q: reason = %v, want editAllowed", stmt, reason)
		}
	}
}

// TestGridEditBlocksQueriesThatAreNotASingleTableSelect: every statement that
// hides the relation behind another one keeps the grid read-only, because the
// generated UPDATE could not name exactly one table.
func TestGridEditBlocksQueriesThatAreNotASingleTableSelect(t *testing.T) {
	cat := gridEditCatalog()
	cols := []driver.ResultColumn{{Name: "id"}, {Name: "email"}}
	for _, stmt := range []string{
		"SELECT u.id FROM users u JOIN orders o ON o.user_id = u.id",
		"SELECT u.id FROM users u, orders o",
		"SELECT id FROM (SELECT id FROM users)",
		"SELECT id FROM users WHERE id IN (SELECT id FROM users)",
		"WITH recent AS (SELECT id FROM users) SELECT id FROM recent",
		"SELECT id FROM users UNION SELECT id FROM users",
		"SELECT id FROM users INTERSECT SELECT id FROM users",
		"SELECT id FROM users EXCEPT SELECT id FROM users",
		"SELECT id FROM users; SELECT id FROM users",
		"UPDATE users SET email = 'x@example.com'",
	} {
		if _, reason := resolveSource(stmt, cat, cols, resultOrigin{}); reason != editBlockedQuery {
			t.Errorf("%q: reason = %v, want editBlockedQuery", stmt, reason)
		}
	}
}

// TestGridEditBlocksAResultWithoutThePrimaryKey: the key is what addresses a
// row, so a result that does not contain it cannot be edited.
func TestGridEditBlocksAResultWithoutThePrimaryKey(t *testing.T) {
	cat := gridEditCatalog()
	_, reason := resolveSource("SELECT email FROM users", cat,
		[]driver.ResultColumn{{Name: "email"}}, resultOrigin{})
	if reason != editBlockedNoKeyColumn {
		t.Fatalf("reason = %v, want editBlockedNoKeyColumn", reason)
	}
}

// TestGridEditBlocksATableWithoutAPrimaryKey: without a key the UPDATE would
// hit more than one row.
func TestGridEditBlocksATableWithoutAPrimaryKey(t *testing.T) {
	cat := gridEditCatalog()
	_, reason := resolveSource("SELECT body FROM notes", cat,
		[]driver.ResultColumn{{Name: "body"}}, resultOrigin{})
	if reason != editBlockedNoKey {
		t.Fatalf("reason = %v, want editBlockedNoKey", reason)
	}
}

// TestGridEditIsBlockedInReadOnlyMode: read-only mode is a hard gate, and it
// says so instead of failing later.
func TestGridEditIsBlockedInReadOnlyMode(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m.settings.ReadOnly = true

	if got := m.editBlockReason(); got != editBlockedReadOnly {
		t.Fatalf("reason = %v, want editBlockedReadOnly", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing != nil {
		t.Fatal("read-only mode must not open the cell editor")
	}
	if !strings.Contains(m.status.message, "read-only mode") {
		t.Fatalf("status = %q, want the read-only reason", m.status.message)
	}
}

// TestGridEditNamesTheReasonItCannotEdit: a result that cannot be edited says
// why, instead of failing when the user presses the key.
func TestGridEditNamesTheReasonItCannotEdit(t *testing.T) {
	conn := e2eConn(t)
	// A table without a primary key cannot address a single row.
	if _, err := conn.Query(context.Background(), "CREATE TABLE notes (body TEXT)"); err != nil {
		t.Fatalf("create notes: %v", err)
	}
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"join", "SELECT u.id FROM users u JOIN users v ON v.id = u.id", "single-table select"},
		{"no primary key", "SELECT body FROM notes", "no primary key"},
		{"key not in the result", "SELECT email FROM users", "primary key is not part of the result"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := gridEditModel(t, conn, tc.query)
			m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
			if m.session.result.err != nil {
				t.Fatalf("query failed: %v", m.session.result.err)
			}
			if m.session.result.src != nil {
				t.Fatal("the result must not be editable")
			}
			m.layout.focus = paneResults
			m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
			if m.session.result.editing != nil {
				t.Fatal("the cell editor must not open")
			}
			if !strings.Contains(m.status.message, tc.want) {
				t.Fatalf("status = %q, want %q", m.status.message, tc.want)
			}
		})
	}
}

// ---- Editing a cell ----

// TestGridEditRecordsAPendingChange: Enter stores the typed value as a
// pending change; nothing has reached the database yet.
func TestGridEditRecordsAPendingChange(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column
	m = editCell(t, m, "new@example.com")

	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1", got)
	}
	p, ok := m.session.result.pendingAt(0, 1)
	if !ok {
		t.Fatal("the change must address the edited cell")
	}
	if p.value == nil || *p.value != "new@example.com" {
		t.Fatalf("pending value = %q, want new@example.com", pendingText(p))
	}
	// The summary counts the pending changes…
	if summary := stripANSI(m.resultSummary(m.session.result)); !strings.Contains(summary, "1 pending change") {
		t.Fatalf("summary = %q, want the pending count", summary)
	}
	// …and the row is marked in the gutter.
	row := stripANSI(m.resultRow(0, 0, 60, m.resultsGutterWidth()))
	if !strings.Contains(row, "*") {
		t.Fatalf("the pending row must be marked: %q", row)
	}
	// The database still holds the old value.
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// TestGridEditEscapeCancelsTheEdit: Esc leaves the cell as it was.
func TestGridEditEscapeCancelsTheEdit(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	m = typeText(m, "zzz")
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.session.result.editing != nil {
		t.Fatal("Esc must close the cell editor")
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0", got)
	}
}

// TestGridEditNullMarkerMeansNull: the literal \N means SQL NULL, the way
// COPY spells it.
func TestGridEditNullMarkerMeansNull(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, `\N`)

	p, ok := m.session.result.pendingAt(0, 1)
	if !ok {
		t.Fatal("the change must be recorded")
	}
	if p.value != nil {
		t.Fatalf("the \\N marker must mean NULL, got %q", *p.value)
	}
}

// TestGridEditEmptyTextIsAnEmptyString: NULL and the empty string are not the
// same thing, so an emptied cell stays an empty string.
func TestGridEditEmptyTextIsAnEmptyString(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = clearCellEdit(t, m)
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	p, ok := m.session.result.pendingAt(0, 1)
	if !ok {
		t.Fatal("the change must be recorded")
	}
	if p.value == nil {
		t.Fatal("an empty cell must stay the empty string, not NULL")
	}
	if *p.value != "" {
		t.Fatalf("pending value = %q, want the empty string", *p.value)
	}
}

// TestGridEditContinuesFromThePendingValue: editing the same cell again must
// not throw away what the user typed.
func TestGridEditContinuesFromThePendingValue(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "first@example.com")

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	if got := m.session.result.editing.text; got != "first@example.com" {
		t.Fatalf("the editor must continue from the pending value, got %q", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: the cell was edited twice", got)
	}
	p, _ := m.session.result.pendingAt(0, 1)
	if p.value == nil || *p.value != "first@example.com" {
		t.Fatalf("pending value = %q, want first@example.com", pendingText(p))
	}
}

// TestGridEditCleansTheEditorAndKeepsTheStoredBytes: opening a cell whose
// value carries escape sequences and a break must not put them on the
// terminal — the editor seeds with the same cleaned copy the grid draws —
// while a commit that did not type over the text writes the stored bytes
// back, so merely looking at a cell can never rewrite the data it holds.
func TestGridEditCleansTheEditorAndKeepsTheStoredBytes(t *testing.T) {
	conn := e2eConn(t)
	raw := "abc" + escapeCellOSC + escapeCellClear + "\nnext"
	if _, err := conn.Query(context.Background(),
		"UPDATE users SET email = ? WHERE id = 1", raw); err != nil {
		t.Fatalf("store the escape sequences: %v", err)
	}
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m.layout.objectsVisible = false
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // the email column

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	e := m.session.result.editing
	if e == nil {
		t.Fatalf("the cell editor did not open: %q", m.status.message)
	}
	if want := foldBreaks(raw); e.text != want {
		t.Fatalf("the editor seeds %q, want the cleaned copy %q", e.text, want)
	}
	if e.orig != raw {
		t.Fatalf("the editor must keep the stored bytes as the value to write back, got %q", e.orig)
	}
	// While the edit is open the pane draws the cleaned value on one line
	// and stays inside the narrow screen.
	assertGridFits(t, m, 40)
	if plain := stripANSI(m.View()); !strings.Contains(plain, "abc]52;c;AAAA[2J next") {
		t.Fatalf("the cleaned value must stay on screen:\n%s", plain)
	}

	// Enter without typing: the pending change carries the stored bytes.
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.session.result.editing != nil {
		t.Fatal("Enter must close the cell editor")
	}
	p, ok := m.session.result.pendingAt(0, 1)
	if !ok {
		t.Fatal("the commit must store a pending change")
	}
	if p.value == nil || *p.value != raw {
		t.Fatalf("the pending value = %q, want the stored bytes %q", pendingText(p), raw)
	}

	// Apply the change: the database still holds the original bytes.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.session.result.err != nil {
		t.Fatalf("the apply failed: %v", m.session.result.err)
	}
	if got := userEmail(t, conn, 1); got != raw {
		t.Fatalf("the database holds %q, want the original bytes %q", got, raw)
	}
	if plain := stripANSI(m.View()); strings.ContainsRune(plain, '\x1b') {
		t.Fatalf("the refreshed grid must stay clean:\n%q", plain)
	}
}

// TestGridEditWriteBackKeepsWhatTheUserTyped: the other half of the rule —
// an edit that changed the text writes what the user sees, not the bytes the
// cell used to hold.
func TestGridEditWriteBackKeepsWhatTheUserTyped(t *testing.T) {
	conn := e2eConn(t)
	raw := "old" + escapeCellOSC
	if _, err := conn.Query(context.Background(),
		"UPDATE users SET email = ? WHERE id = 1", raw); err != nil {
		t.Fatalf("store the escape sequences: %v", err)
	}
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	p, ok := m.session.result.pendingAt(0, 1)
	if !ok {
		t.Fatal("the edit must store a pending change")
	}
	if p.value == nil || *p.value != "new@example.com" {
		t.Fatalf("the pending value = %q, want the typed text", pendingText(p))
	}
}

// ---- The generated UPDATE ----

// TestGridEditGeneratesAParameterizedUpdate: the value and the key are
// arguments, never text in the statement.
func TestGridEditGeneratesAParameterizedUpdate(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	stmts, err := m.pendingStatements()
	if err != nil {
		t.Fatalf("pending statements: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("statements = %d, want 1", len(stmts))
	}
	text := stmts[0].Text
	if !strings.Contains(text, "UPDATE") || !strings.Contains(text, "SET") || !strings.Contains(text, "WHERE") {
		t.Fatalf("statement = %q, want an UPDATE with a SET and a WHERE clause", text)
	}
	if !strings.Contains(text, `SET "email" = ?`) || !strings.Contains(text, `WHERE "id" = ?`) {
		t.Fatalf("statement = %q, want placeholders for the value and the key", text)
	}
	if strings.Contains(text, "new@example.com") {
		t.Fatalf("the value must not be part of the statement: %q", text)
	}
	if len(stmts[0].Args) != 2 {
		t.Fatalf("arguments = %v, want the value and the key", stmts[0].Args)
	}
	if stmts[0].Args[0] != "new@example.com" || stmts[0].Args[1] != "1" {
		t.Fatalf("arguments = %v, want the value and the key", stmts[0].Args)
	}
}

// TestGridEditGeneratesNumberedPlaceholdersForPostgres: PostgreSQL numbers
// its placeholders, and the numbering must run through the statement.
func TestGridEditGeneratesNumberedPlaceholdersForPostgres(t *testing.T) {
	cat := gridEditCatalog()
	src, reason := resolveSource("SELECT id, email FROM users", cat,
		[]driver.ResultColumn{{Name: "id"}, {Name: "email"}}, resultOrigin{})
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	text, args := generateCellUpdate(dialect.MustGet("postgres"), src.table,
		src.cols[1], src.pk, gridEditString("new@example.com"), []string{"1"})
	want := `UPDATE "main"."users" SET "email" = $1 WHERE "id" = $2`
	if text != want {
		t.Fatalf("statement = %q, want %q", text, want)
	}
	if len(args) != 2 || args[0] != "new@example.com" || args[1] != "1" {
		t.Fatalf("arguments = %v, want the value and the key", args)
	}
}

// TestGridEditGeneratesAnAndChainForACompositePrimaryKey: the key addresses
// one row, so every key column is part of the WHERE clause and the
// placeholders keep counting.
func TestGridEditGeneratesAnAndChainForACompositePrimaryKey(t *testing.T) {
	cat := gridEditCatalog()
	src, reason := resolveSource("SELECT room, seq, note FROM events", cat,
		[]driver.ResultColumn{{Name: "room"}, {Name: "seq"}, {Name: "note"}}, resultOrigin{})
	if reason != editAllowed {
		t.Fatalf("reason = %v, want editAllowed", reason)
	}
	text, args := generateCellUpdate(dialect.MustGet("postgres"), src.table,
		src.cols[2], src.pk, gridEditString("hello"), []string{"kitchen", "7"})
	want := `UPDATE "main"."events" SET "note" = $1 WHERE "room" = $2 AND "seq" = $3`
	if text != want {
		t.Fatalf("statement = %q, want %q", text, want)
	}
	if len(args) != 3 || args[0] != "hello" || args[1] != "kitchen" || args[2] != "7" {
		t.Fatalf("arguments = %v, want the value and both key columns", args)
	}
}

// ---- Applying the changes ----

// TestGridEditApplyAsksForConfirmationFirst: nothing reaches the database
// before the user has seen the statement.
func TestGridEditApplyAsksForConfirmationFirst(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	if len(m.confirm.stmts) != 1 {
		t.Fatalf("the dialog carries %d statements, want 1", len(m.confirm.stmts))
	}
	if !strings.Contains(m.confirm.stmts[0].Text, "UPDATE") {
		t.Fatalf("the dialog must carry the UPDATE, got %q", m.confirm.stmts[0].Text)
	}
	if view := stripANSI(m.View()); !strings.Contains(view, m.confirm.stmts[0].Text) {
		t.Fatalf("the dialog must show the statement %q:\n%s", m.confirm.stmts[0].Text, view)
	}
	// The database has not been touched yet.
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}

	// Esc cancels: the changes stay pending, the database stays as it is.
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.confirm.open {
		t.Fatal("Esc must close the dialog")
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: cancelling must keep them", got)
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// TestGridEditEndToEndWritesTheNewValue: the whole path — select, edit,
// apply, confirm — changes the row in the database and refreshes the grid.
func TestGridEditEndToEndWritesTheNewValue(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.confirm.open {
		t.Fatal("the dialog must close when the changes are applied")
	}
	if m.session.result.err != nil {
		t.Fatalf("the apply failed: %v", m.session.result.err)
	}
	if got := userEmail(t, conn, 1); got != "new@example.com" {
		t.Fatalf("the database holds %q, want new@example.com", got)
	}
	// The grid was refreshed: it shows what the database holds now.
	if m.session.result.res == nil || len(m.session.result.res.Rows) == 0 {
		t.Fatal("the grid must show the refreshed rows")
	}
	cell := m.session.result.res.Rows[0][1]
	if cell == nil {
		t.Fatal("the grid must show the new value, not NULL")
	}
	if *cell != "new@example.com" {
		t.Fatalf("the grid shows %q, want new@example.com", *cell)
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0 after the apply", got)
	}
}

// ---- The safety valves ----

// TestGridEditCtrlEnterAppliesThePendingChanges: Ctrl+Enter is the fallback
// for the terminals whose tty swallows Ctrl+S — with the software flow
// control of IXON enabled the byte 0x13 never reaches the application. It
// must apply the pending changes just like Ctrl+S does.
func TestGridEditCtrlEnterAppliesThePendingChanges(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	// The terminals send Ctrl+Enter as Ctrl+J (LF).
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if !m.confirm.open {
		t.Fatal("Ctrl+Enter must ask for confirmation, just like Ctrl+S")
	}
	if len(m.confirm.stmts) != 1 {
		t.Fatalf("the dialog carries %d statements, want 1", len(m.confirm.stmts))
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := userEmail(t, conn, 1); got != "new@example.com" {
		t.Fatalf("the database holds %q, want new@example.com", got)
	}
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0 after the apply", got)
	}
}

// TestGridEditCtrlEnterWithoutPendingChangesRunsTheQuery pins that the
// fallback key does not steal the global meaning of Ctrl+Enter: with nothing
// to apply it still runs the statement.
func TestGridEditCtrlEnterWithoutPendingChangesRunsTheQuery(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.confirm.open {
		t.Fatal("there is nothing to apply, so no confirmation must appear")
	}
	if m.session.result.err != nil {
		t.Fatalf("the query failed: %v", m.session.result.err)
	}
	if m.session.result.res == nil || len(m.session.result.res.Rows) != 3 {
		t.Fatalf("the statement must have run again: %+v", m.session.result.res)
	}
}

// TestGridEditReadOnlyBlocksTheApply: the read-only gate stops the batch even
// when the changes were made before the mode was switched on.
func TestGridEditReadOnlyBlocksTheApply(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	m.settings.ReadOnly = true
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.confirm.open {
		t.Fatal("read-only mode must block the apply, not ask for confirmation")
	}
	if !strings.Contains(m.status.message, "Read-only mode") {
		t.Fatalf("status = %q, want the read-only message", m.status.message)
	}
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: a blocked apply must keep them", got)
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// TestGridEditFailedApplyKeepsThePendingChanges: a failed batch must not
// throw away what the user typed.
func TestGridEditFailedApplyKeepsThePendingChanges(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	// The email column is NOT NULL, so a NULL value makes the UPDATE fail.
	m = editCell(t, m, `\N`)

	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.confirm.open {
		t.Fatal("applying the changes must ask for confirmation")
	}
	next, cmd, ok := m.acceptConfirm()
	if !ok || cmd == nil {
		t.Fatal("confirming must run the statements")
	}
	msg := cmd()
	q, isQuery := msg.(queryMsg)
	if !isQuery {
		t.Fatalf("the command returned %T, want a query outcome", msg)
	}
	if q.err == nil {
		t.Fatal("the UPDATE must fail: the email column does not accept NULL")
	}
	// The outcome is handled without draining the command queue, so the
	// status message survives.
	m = send(next, q)
	if got := m.session.result.pendingCount(); got != 1 {
		t.Fatalf("pending changes = %d, want 1: a failed apply must keep them", got)
	}
	if !strings.Contains(m.status.message, "could not be applied") {
		t.Fatalf("status = %q, want the failure message", m.status.message)
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// ---- Discarding the changes ----

// TestGridEditDiscardChangesDropsThePendingChanges: the Query menu drops the
// pending changes without touching the database.
func TestGridEditDiscardChangesDropsThePendingChanges(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	// Alt+Q opens the Query menu, "d" activates Discard Changes.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true})
	if m.menu.open != 4 {
		t.Fatalf("menu.open = %d, want the Query menu (4)", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if got := m.session.result.pendingCount(); got != 0 {
		t.Fatalf("pending changes = %d, want 0", got)
	}
	if !strings.Contains(m.status.message, "discarded") {
		t.Fatalf("status = %q, want the discard message", m.status.message)
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}

// TestGridEditMenuApplyChangesOpensTheConfirmation: the Query menu offers the
// same action as Ctrl+S in the grid.
func TestGridEditMenuApplyChangesOpensTheConfirmation(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	m = editCell(t, m, "new@example.com")

	// Alt+Q opens the Query menu, "a" activates Apply Changes.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q"), Alt: true})
	if m.menu.open != 4 {
		t.Fatalf("menu.open = %d, want the Query menu (4)", m.menu.open)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if !m.confirm.open {
		t.Fatal("the menu item must ask for confirmation")
	}
	if got := userEmail(t, conn, 1); got != "ada@example.com" {
		t.Fatalf("the database holds %q, want the old value", got)
	}
}
