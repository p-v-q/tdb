package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// open is the factory entry point under test (named so it can be used inside if
// statements, where a composite literal would confuse the parser).
var open = factory{}.Open

// openTest opens a database file in a temp directory with a small schema.
func openTest(t *testing.T) driver.Connection {
	t.Helper()
	cfg := driver.Config{Name: "sqlite", DSN: filepath.Join(t.TempDir(), "test.db")}
	conn, err := open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	schema := `
		CREATE TABLE users (
			id    INTEGER PRIMARY KEY,
			email TEXT    NOT NULL UNIQUE,
			name  TEXT,
			age   INTEGER
		);
		CREATE TABLE orders (
			id      INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			total   REAL
		);
		CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18;
		CREATE UNIQUE INDEX idx_orders_user_id ON orders(user_id);
		INSERT INTO users (id, email, name, age) VALUES
			(1, 'a@example.com', 'Ada', 36),
			(2, 'b@example.com', NULL, 7),
			(3, 'c@example.com', 'Lin', NULL);
	`
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := conn.Query(context.Background(), stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	// A trigger body contains semicolons, so execute it as one statement rather
	// than splitting it with the rest of the fixture.
	if _, err := conn.Query(context.Background(), `
		CREATE TRIGGER users_audit AFTER UPDATE ON users
		BEGIN
			UPDATE users SET age = age WHERE id = NEW.id;
		END`); err != nil {
		t.Fatalf("setup trigger: %v", err)
	}
	return conn
}

func TestOpenRequiresAFile(t *testing.T) {
	cfg := driver.Config{} // no DSN and no file option
	if _, err := open(context.Background(), cfg); err == nil {
		t.Fatal("opening without a database file must fail with a clear error")
	}
}

func TestOpenCreatesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.db")
	conn, err := open(context.Background(), driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := conn.Name(); got != "new.db" {
		t.Fatalf("Name = %q, want the file name", got)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The file exists on disk and can be opened again.
	conn2, err := open(context.Background(), driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	conn2.Close()
}

func TestCatalogIntrospection(t *testing.T) {
	conn := openTest(t)
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if cat.Dialect != "sqlite" {
		t.Fatalf("Dialect = %q", cat.Dialect)
	}
	if len(cat.Schemas) != 1 || cat.Schemas[0].Name != "main" {
		t.Fatalf("schemas = %+v", cat.Schemas)
	}

	byName := map[string]*catalog.Table{}
	for _, tb := range cat.Schemas[0].Tables {
		byName[tb.Name] = tb
	}
	for _, name := range []string{"users", "orders", "adults"} {
		if byName[name] == nil {
			t.Fatalf("table %q is missing from %+v", name, byName)
		}
	}
	if byName["adults"].Type != catalog.TypeView {
		t.Fatalf("adults must be a view, got %q", byName["adults"].Type)
	}

	if cat.Capabilities.Supports(catalog.KindFunction) {
		t.Fatal("SQLite cannot introspect stored functions")
	}
	if !cat.Capabilities.Supports(catalog.KindTrigger) || !cat.Capabilities.Supports(catalog.KindIndex) {
		t.Fatalf("SQLite capabilities = %v, want trigger and index", cat.Capabilities)
	}
	resources := map[string]*catalog.Resource{}
	for _, r := range cat.Schemas[0].Resources {
		resources[r.Name] = r
	}
	trigger := resources["users_audit"]
	if trigger == nil || trigger.Kind != catalog.KindTrigger || trigger.Owner != "users" {
		t.Fatalf("trigger = %+v, want users_audit on users", trigger)
	}
	if !strings.Contains(trigger.Detail, "AFTER") || !strings.Contains(trigger.Detail, "UPDATE") {
		t.Fatalf("trigger detail = %q, want timing and event", trigger.Detail)
	}
	index := resources["idx_orders_user_id"]
	if index == nil || index.Kind != catalog.KindIndex || index.Owner != "orders" {
		t.Fatalf("index = %+v, want idx_orders_user_id on orders", index)
	}
	if !strings.Contains(index.Detail, "unique") {
		t.Fatalf("index detail = %q, want unique", index.Detail)
	}
	if resources["sqlite_autoindex_users_1"] != nil {
		t.Fatal("SQLite internal autoindexes must not be shown as user objects")
	}

	users := byName["users"]
	want := []struct {
		name, typ string
		nullable  bool
		pk        bool
	}{
		{"id", "INTEGER", false, true},
		{"email", "TEXT", false, false},
		{"name", "TEXT", true, false},
		{"age", "INTEGER", true, false},
	}
	if len(users.Columns) != len(want) {
		t.Fatalf("users columns = %+v", users.Columns)
	}
	for i, w := range want {
		c := users.Columns[i]
		if c.Name != w.name || c.Type != w.typ || c.Nullable != w.nullable || c.PK != w.pk {
			t.Errorf("column %d = %+v, want %+v", i, c, w)
		}
		if c.Ordinal != i {
			t.Errorf("column %s ordinal = %d, want %d", c.Name, c.Ordinal, i)
		}
	}
}

func TestQueryRowsAndNulls(t *testing.T) {
	conn := openTest(t)
	res, err := conn.Query(context.Background(), "SELECT id, email, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Columns) != 3 || res.Columns[0].Name != "id" {
		t.Fatalf("columns = %+v", res.Columns)
	}
	if res.Columns[0].Type == "" {
		t.Fatal("column types must be reported")
	}
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
	// NULL is not the empty string: the second user has no name.
	if got := *res.Rows[0][2]; got != "Ada" {
		t.Fatalf("name = %q", got)
	}
	if res.Rows[1][2] != nil {
		t.Fatalf("name = %v, want NULL", res.Rows[1][2])
	}
	// A duration below the monotonic clock's granularity can come out as
	// exactly zero (Windows under a virtualized clock does this), so the
	// measurement is pinned by the slower query below instead of by this one.
	if res.Duration < 0 {
		t.Fatalf("Duration = %v, a measured duration cannot be negative", res.Duration)
	}
}

// TestDurationIsMeasured checks that a statement slow enough to be visible to
// the monotonic clock reports a positive duration.
func TestDurationIsMeasured(t *testing.T) {
	conn := openTest(t)
	res, err := conn.Query(context.Background(),
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 300000) SELECT count(*) FROM n`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Duration <= 0 {
		t.Fatalf("Duration = %v, want a positive measurement", res.Duration)
	}
}

func TestQueryDMLReportsAffectedRows(t *testing.T) {
	conn := openTest(t)
	res, err := conn.Query(context.Background(), "UPDATE users SET age = 99 WHERE age IS NULL")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Affected != 1 {
		t.Fatalf("Affected = %d, want 1", res.Affected)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("a DML statement must not return rows, got %d", len(res.Rows))
	}
	// A row set is still available afterwards.
	res, err = conn.Query(context.Background(), "SELECT COUNT(*) FROM users WHERE age = 99")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != 1 || *res.Rows[0][0] != "1" {
		t.Fatalf("rows = %+v", res.Rows)
	}
}

func TestQueryErrorsAreReported(t *testing.T) {
	conn := openTest(t)
	_, err := conn.Query(context.Background(), "SELECT * FROM missing_table")
	if err == nil {
		t.Fatal("querying a missing table must fail")
	}
	if !strings.Contains(err.Error(), "missing_table") {
		t.Fatalf("the error must name the problem: %v", err)
	}
	if _, err := conn.Query(context.Background(), "   "); err == nil {
		t.Fatal("an empty statement must fail with a clear message")
	}
}

func TestQueryIsCancellable(t *testing.T) {
	conn := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := conn.Query(ctx, "SELECT * FROM users")
	if err == nil {
		t.Fatal("a cancelled context must abort the statement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestQueryIsCapped(t *testing.T) {
	conn := openTest(t)
	_, err := conn.Query(context.Background(), `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 5000)
		INSERT INTO users (id, email) SELECT i + 100, 'x' || i FROM n`)
	if err != nil {
		t.Fatalf("bulk insert: %v", err)
	}
	res, err := conn.Query(context.Background(), "SELECT id FROM users")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != maxRows {
		t.Fatalf("rows = %d, want the cap %d", len(res.Rows), maxRows)
	}
	if !res.Truncated {
		t.Fatal("a capped result must say that it was truncated")
	}
}

func TestClosedConnectionRefusesWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	conn, err := open(context.Background(), driver.Config{DSN: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := conn.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("a closed connection must refuse to run statements")
	}
	if _, err := conn.Catalog(context.Background()); err == nil {
		t.Fatal("a closed connection must refuse to introspect")
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"file:my.db":             "my.db",
		"/tmp/x/test.db":         "test.db",
		"file:my.db?mode=memory": "my.db",
		":memory:":               ":memory:",
	}
	for in, want := range cases {
		if got := displayName(in); got != want {
			t.Errorf("displayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTimeoutDoesNotHang(t *testing.T) {
	conn := openTest(t)

	// A cancelled context must never execute the statement, whatever the
	// clock resolution.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := conn.Query(ctx, "SELECT * FROM users"); err == nil {
		t.Fatal("a cancelled context must abort the statement")
	}

	// A slow statement must return once its deadline expires. A nanosecond
	// deadline is not a reliable trigger: with a coarse monotonic clock it
	// can still look unexpired for a moment, so the deadline here is long
	// enough to be real and short enough to keep the test fast.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	start := time.Now()
	_, err := conn.Query(ctx2,
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 100000000) SELECT count(*) FROM n`)
	if err == nil {
		t.Fatal("an expired deadline must abort the statement")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the deadline took %v to abort the statement", elapsed)
	}
}

// TestDefinitionReadsStoredDDL checks the DefinitionProvider implementation:
// SQLite stores CREATE statements verbatim, so the fetched DDL must reproduce
// the original statement — for tables, views, triggers and indexes alike.
func TestDefinitionReadsStoredDDL(t *testing.T) {
	conn := openTest(t)
	prov, ok := conn.(driver.DefinitionProvider)
	if !ok {
		t.Fatal("the sqlite connection must implement driver.DefinitionProvider")
	}
	ctx := context.Background()

	cases := []struct {
		ref  driver.DefinitionRef
		want string // a fragment the DDL must contain
	}{
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "main", Name: "users"}, "CREATE TABLE users"},
		{driver.DefinitionRef{Kind: catalog.KindTable, Schema: "main", Name: "orders"}, "user_id INTEGER NOT NULL"},
		{driver.DefinitionRef{Kind: catalog.KindView, Schema: "main", Name: "adults"}, "CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18"},
		{driver.DefinitionRef{Kind: catalog.KindTrigger, Schema: "main", Name: "users_audit", Owner: "users"}, "CREATE TRIGGER users_audit AFTER UPDATE ON users"},
		{driver.DefinitionRef{Kind: catalog.KindIndex, Schema: "main", Name: "idx_orders_user_id", Owner: "orders"}, "CREATE UNIQUE INDEX idx_orders_user_id ON orders(user_id)"},
		// No schema means "main", the default database.
		{driver.DefinitionRef{Kind: catalog.KindTable, Name: "users"}, "CREATE TABLE users"},
	}
	for _, tc := range cases {
		ddl, err := prov.Definition(ctx, tc.ref)
		if err != nil {
			t.Fatalf("Definition(%s %s.%s): %v", tc.ref.Kind, tc.ref.Schema, tc.ref.Name, err)
		}
		if !strings.Contains(ddl, tc.want) {
			t.Errorf("Definition(%s %s.%s) = %q, want it to contain %q",
				tc.ref.Kind, tc.ref.Schema, tc.ref.Name, ddl, tc.want)
		}
	}

	// A trigger body contains semicolons; the stored definition must keep it.
	ddl, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTrigger, Schema: "main", Name: "users_audit"})
	if err != nil {
		t.Fatalf("Definition(trigger): %v", err)
	}
	if !strings.Contains(ddl, "UPDATE users SET age = age WHERE id = NEW.id;") {
		t.Fatalf("the trigger body must survive intact, got %q", ddl)
	}
}

// TestDefinitionErrorsAreReported checks the failure paths: a kind SQLite
// cannot store, an object that does not exist and a closed connection.
func TestDefinitionErrorsAreReported(t *testing.T) {
	conn := openTest(t)
	prov := conn.(driver.DefinitionProvider)
	ctx := context.Background()

	if _, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindFunction, Schema: "main", Name: "f"}); err == nil {
		t.Error("asking for a stored function must fail: SQLite has none")
	}
	_, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "main", Name: "missing"})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("a missing table must name the object, got %v", err)
	}
	_, err = prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "no_such_db", Name: "t"})
	if err == nil {
		t.Error("a missing schema must fail")
	}

	conn.Close()
	if _, err := prov.Definition(ctx, driver.DefinitionRef{Kind: catalog.KindTable, Schema: "main", Name: "users"}); err == nil {
		t.Error("a closed connection must refuse to fetch definitions")
	}
}

func TestSQLiteTriggerDetailUsesOnlyTheHeader(t *testing.T) {
	definition := `CREATE TRIGGER audit AFTER INSERT ON users
		BEGIN
			UPDATE users SET age = age WHERE id = NEW.id;
		END`
	if got := sqliteTriggerDetail(definition); got != "AFTER INSERT" {
		t.Fatalf("trigger detail = %q, want AFTER INSERT", got)
	}
	definition = `CREATE TRIGGER touch BEFORE UPDATE OF email ON users BEGIN SELECT 1; END`
	if got := sqliteTriggerDetail(definition); got != "BEFORE UPDATE" {
		t.Fatalf("trigger detail = %q, want BEFORE UPDATE", got)
	}
}
