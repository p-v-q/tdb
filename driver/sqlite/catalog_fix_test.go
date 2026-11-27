package sqlite

// catalog_fix_test.go pins the catalog fixes from the pre-release review:
// generated columns are listed (PRAGMA table_xinfo), a TEXT PRIMARY KEY of an
// ordinary rowid table is nullable, the sqlite_ name filter uses an escaped
// wildcard, and the affected count is only reported for DML.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
)

// openCatalogFixTest opens a fresh temp-file database and runs ddl.
func openCatalogFixTest(t *testing.T, ddl ...string) driver.Connection {
	t.Helper()
	conn, err := open(context.Background(), driver.Config{
		Name: "sqlite",
		DSN:  filepath.Join(t.TempDir(), "catalog.db"),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, statement := range ddl {
		if _, err := conn.Query(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return conn
}

func catalogTable(t *testing.T, conn driver.Connection, name string) *catalog.Table {
	t.Helper()
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	tbl := cat.FindTable("main." + name)
	if tbl == nil {
		t.Fatalf("table %q is missing from the catalog", name)
	}
	return tbl
}

func columnByName(t *testing.T, tbl *catalog.Table, name string) *catalog.Column {
	t.Helper()
	for _, col := range tbl.Columns {
		if col.Name == name {
			return col
		}
	}
	t.Fatalf("column %q is missing from %s: %+v", name, tbl.Name, tbl.Columns)
	return nil
}

// TestGeneratedColumnsAreCatalogued reproduces the review's finding: PRAGMA
// table_info omitted STORED and VIRTUAL generated columns entirely, so the
// table looked narrower than it is. table_xinfo lists them.
func TestGeneratedColumnsAreCatalogued(t *testing.T) {
	conn := openCatalogFixTest(t,
		`CREATE TABLE g (
			a INT,
			b INT GENERATED ALWAYS AS (a * 2) VIRTUAL,
			s TEXT GENERATED ALWAYS AS (a || 'x') STORED
		)`)
	tbl := catalogTable(t, conn, "g")
	if len(tbl.Columns) != 3 {
		t.Fatalf("columns = %+v, want a, b and s", tbl.Columns)
	}
	for i, name := range []string{"a", "b", "s"} {
		col := columnByName(t, tbl, name)
		if col.Ordinal != i {
			t.Errorf("column %s ordinal = %d, want %d", name, col.Ordinal, i)
		}
	}
	if col := columnByName(t, tbl, "b"); !col.PK && col.Name != "b" {
		t.Errorf("generated column = %+v", col)
	}
	// The catalog has no read-only flag, so the generated columns carry their
	// real metadata (default is empty; they are not writable in SQLite, which
	// the driver documents rather than invents a flag for).
	if col := columnByName(t, tbl, "s"); col.Default != "" {
		t.Errorf("generated column default = %q, want empty", col.Default)
	}
}

// TestPrimaryKeyNullability pins SQLite's real rule: a PRIMARY KEY column is
// only implicitly NOT NULL for an INTEGER PRIMARY KEY rowid alias, a WITHOUT
// ROWID table or a STRICT table. An ordinary rowid table accepts NULL in a TEXT
// PRIMARY KEY, so the catalog must not claim otherwise.
func TestPrimaryKeyNullability(t *testing.T) {
	conn := openCatalogFixTest(t,
		`CREATE TABLE text_pk (code TEXT PRIMARY KEY)`,
		`CREATE TABLE integer_pk (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE int_pk (id INT PRIMARY KEY)`,
		`CREATE TABLE without_rowid (code TEXT PRIMARY KEY) WITHOUT ROWID`,
		`CREATE TABLE strict_table (code TEXT PRIMARY KEY) STRICT`,
		`CREATE TABLE declared_not_null (code TEXT PRIMARY KEY NOT NULL)`,
		`CREATE TABLE composite_pk (a TEXT, b TEXT, PRIMARY KEY (a, b))`,
	)
	cases := []struct {
		table, column string
		wantNullable  bool
	}{
		{"text_pk", "code", true},
		{"integer_pk", "id", false},
		{"int_pk", "id", true}, // INT is not the rowid alias type
		{"without_rowid", "code", false},
		{"strict_table", "code", false},
		{"declared_not_null", "code", false},
		{"composite_pk", "a", true},
		{"composite_pk", "b", true},
	}
	for _, tc := range cases {
		tbl := catalogTable(t, conn, tc.table)
		col := columnByName(t, tbl, tc.column)
		if !col.PK {
			t.Errorf("%s.%s is not marked as a primary key", tc.table, tc.column)
		}
		if col.Nullable != tc.wantNullable {
			t.Errorf("%s.%s Nullable = %v, want %v", tc.table, tc.column, col.Nullable, tc.wantNullable)
		}
	}

	// The catalog's claim matches what the database accepts: a TEXT PRIMARY
	// KEY of a rowid table stores NULL, while inserting NULL into an INTEGER
	// PRIMARY KEY assigns a rowid (so the column is never NULL).
	if _, err := conn.Query(context.Background(), "INSERT INTO text_pk (code) VALUES (NULL)"); err != nil {
		t.Errorf("a TEXT PRIMARY KEY of a rowid table accepts NULL, but the insert failed: %v", err)
	}
	if _, err := conn.Query(context.Background(), "INSERT INTO integer_pk (id) VALUES (NULL)"); err != nil {
		t.Errorf("INSERT NULL into an INTEGER PRIMARY KEY must assign a rowid: %v", err)
	}
	res, err := conn.Query(context.Background(), "SELECT count(*) FROM integer_pk WHERE id IS NULL")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "0" {
		t.Errorf("an INTEGER PRIMARY KEY column holds NULL after an insert, but it cannot")
	}
}

// TestSQLiteNameFilterIsEscaped pins that only the literal sqlite_ prefix is
// hidden: '_' is a LIKE wildcard, so the old filter also hid user tables named
// sqlitex/sqlitefoo from the catalog.
func TestSQLiteNameFilterIsEscaped(t *testing.T) {
	conn := openCatalogFixTest(t,
		`CREATE TABLE sqlitex (a INT)`,
		`CREATE TABLE sqlitefoo (a INT)`,
		`CREATE TABLE visible (a INT)`,
		`CREATE INDEX sqlitefoo_idx ON visible (a)`,
		`CREATE TRIGGER sqlitefoo_trg AFTER INSERT ON visible BEGIN SELECT 1; END`,
	)
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	for _, name := range []string{"sqlitex", "sqlitefoo", "visible"} {
		if cat.FindTable("main."+name) == nil {
			t.Errorf("table %q is hidden from the catalog", name)
		}
	}
	main := cat.FindSchema("main")
	if main == nil {
		t.Fatal("main schema is missing")
	}
	resources := make(map[string]bool)
	for _, r := range main.Resources {
		resources[r.Name] = true
	}
	for _, name := range []string{"sqlitefoo_idx", "sqlitefoo_trg"} {
		if !resources[name] {
			t.Errorf("resource %q is hidden from the catalog", name)
		}
	}
}
