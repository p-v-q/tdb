package scenarios

// sqlite_big_test.go — end-to-end scenarios for the two prebuilt SQLite files:
// shop.db (600k rows across users/orders/order_items) and weird.db (hostile
// identifiers, weird types, empty tables). The big-result scenarios exercise the
// 1000-row driver cap, grid scrolling, sorting, filtering and the OSC 52 copy
// path; the weird.db scenarios exercise identifier quoting in the generated SQL.

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// sqStartShop opens the 600k-row shop.db.
func sqStartShop(t *testing.T) *scenario.Ctx {
	t.Helper()
	return dialStartSQLite(t, scenario.SharedEnv(t).SQLiteShop, "conn shop.db (sqlite)")
}

// sqStartWeird opens the edge-case weird.db.
func sqStartWeird(t *testing.T) *scenario.Ctx {
	t.Helper()
	return dialStartSQLite(t, scenario.SharedEnv(t).SQLiteWeird, "conn weird.db (sqlite)")
}

// TestSQLiteBigCatalogCount opens shop.db, reads the catalog out of the object
// browser (the whole tree, including the trigger and the indexes) and runs the
// row counts of the big tables in one round trip, checking every count against
// an independent database/sql connection and the summary's timing.
func TestSQLiteBigCatalogCount(t *testing.T) {
	c := sqStartShop(t)
	c.RequireContains("conn shop.db (sqlite)")
	c.RequireMatch(`1 schema / 9 tables`)
	c.Shot("connected")

	// The tree starts folded; unfold the session and its single schema. The
	// schema row sits directly below the live connection row.
	c.FocusObjects()
	c.Keys("expand-connection", harness.KeyRight)
	c.WaitFor("main", dialWait)
	c.Keys("objects-home", harness.KeyHome)
	dialObjectDown(c, 1)
	c.RequireMatch(`Object 2/6  main`)
	c.Keys("expand-schema", harness.KeyRight)
	c.WaitFor("Tables (6)", dialWait)
	c.RequireContains("Tables (6)", "Views (3)", "Triggers (1)", "Indexes (13)")
	c.Shot("catalog-groups")

	// The six base tables, including the generated-column and JSON ones.
	c.Keys("to-tables", harness.KeyDown)
	c.Keys("expand-tables", harness.KeyRight)
	c.WaitFor("order_items", dialWait)
	c.RequireContains("events", "order_audit", "order_items", "orders", "products", "users")
	c.Shot("tables")

	// One round trip counts every big table; the summary must carry a timing.
	dialRunWait(c,
		"SELECT (SELECT COUNT(*) FROM users) AS users, (SELECT COUNT(*) FROM products) AS products, "+
			"(SELECT COUNT(*) FROM orders) AS orders, (SELECT COUNT(*) FROM order_items) AS items, "+
			"(SELECT COUNT(*) FROM events) AS events;", "100000")
	c.RequireContains("100000", "5000", "200000", "300000", "20000")
	c.RequireMatch(`1 row in \d+ms`)
	c.Shot("counts")

	// Independent verification over database/sql.
	db := c.Env.OpenSQLite(t, c.Env.SQLiteShop)
	for table, want := range map[string]int64{
		"users": 100000, "products": 5000, "orders": 200000,
		"order_items": 300000, "events": 20000,
	} {
		if got := scenario.ScalarInt(t, db, "SELECT COUNT(*) FROM "+table); got != want {
			t.Fatalf("%s has %d rows, want %d", table, got, want)
		}
	}
}

// TestSQLiteBigTruncationScrollSortFilterCopy drives the big-result workflow on
// the 200k-row orders table: the driver caps a result at 1000 rows and says so,
// the grid scrolls, sorts and filters those rows, and a row goes to the
// clipboard over OSC 52. Sort order and filter counts are checked against
// database/sql, so the grid is proved to show what the database holds.
func TestSQLiteBigTruncationScrollSortFilterCopy(t *testing.T) {
	c := sqStartShop(t)
	db := c.Env.OpenSQLite(t, c.Env.SQLiteShop)

	// SELECT * on the 200k-row table comes back capped at 1000 rows, and the
	// summary says the result is truncated instead of silently lying.
	dialRunWait(c, "SELECT * FROM orders;", "showing the first 1000 rows")
	c.RequireContains("1000 rows in")
	c.RequireMatch(`showing the first 1000 rows`)
	c.Shot("truncated")

	// Grid scrolling: End jumps to the last loaded row, PgUp climbs back,
	// Home returns to the top.
	c.FocusResults()
	c.Keys("grid-end", harness.KeyEnd)
	c.WaitFor("ORD-0000001000", dialWait)
	c.Shot("grid-end")
	c.Keys("grid-pgup", harness.KeyPgUp)
	c.RequireContains("ORD-0000000987")
	c.Keys("grid-home", harness.KeyHome)
	c.RequireContains("ORD-0000000001")
	c.Shot("grid-home")

	// Sorting: the total column (5th) sorts numerically over the loaded rows.
	c.Keys("caret-total-column", strings.Repeat(harness.KeyRight, 4))
	c.Keys("sort-asc", "s")
	c.Keys("grid-home-2", harness.KeyHome)
	minRow, status := dialCopyGridRow(c)
	if !strings.Contains(status, "Copied") {
		c.Fail("copying the first sorted row did not report a copy; status: %q", status)
	}
	ascTotal := sqRowTotal(t, minRow)
	wantMin := sqScalarFloat(t, db, "SELECT CAST(MIN(total) AS TEXT) FROM (SELECT total FROM orders LIMIT 1000)")
	if ascTotal != wantMin {
		c.Fail("ascending sort: first row total = %v, database MIN = %v (row %q)", ascTotal, wantMin, minRow)
	}
	c.Shot("sorted-asc")

	// The same column sorts descending.
	c.Keys("sort-desc", "s")
	c.Keys("grid-home-3", harness.KeyHome)
	maxRow, _ := dialCopyGridRow(c)
	descTotal := sqRowTotal(t, maxRow)
	wantMax := sqScalarFloat(t, db, "SELECT CAST(MAX(total) AS TEXT) FROM (SELECT total FROM orders LIMIT 1000)")
	if descTotal != wantMax {
		c.Fail("descending sort: first row total = %v, database MAX = %v (row %q)", descTotal, wantMax, maxRow)
	}
	c.Shot("sorted-desc")

	// A third "s" restores the database order.
	c.Keys("sort-clear", "s")
	c.Keys("grid-home-4", harness.KeyHome)
	c.RequireContains("ORD-0000000001")

	// Filtering the big result: rows whose shipping address is in Shanghai.
	c.Keys("open-grid-filter", "/")
	c.Type("grid-filter", "上海")
	c.Keys("apply-grid-filter", harness.KeyEnter)
	c.WaitFor("filtered from 1000", dialWait)
	got := dialRowsShown(c)
	want := int(scenario.ScalarInt(t, db,
		"SELECT COUNT(*) FROM (SELECT shipping_address FROM orders LIMIT 1000) WHERE shipping_address LIKE '%上海%'"))
	if got != want {
		c.Fail("the grid filtered to %d rows, the database has %d", got, want)
	}
	c.RequireMatch(`\d+ rows in \d+ms · filtered from 1000`)
	c.Shot("filtered")

	// Copy a row of the filtered view: the OSC 52 payload is the TSV row, with
	// the unicode city intact.
	c.Keys("grid-home-5", harness.KeyHome)
	row, status := dialCopyGridRow(c)
	if !strings.Contains(status, "Copied") {
		c.Fail("copying a filtered row did not report a copy; status: %q", status)
	}
	if !strings.Contains(row, "ORD-") || !strings.Contains(row, "上海") || !strings.Contains(row, "\t") {
		c.Fail("the copied row is not the expected TSV row: %q", row)
	}
	c.Logf("copied filtered row: %q", row)
	c.Shot("copied-row")

	// "/" then Esc clears the filter; the full 1000-row view comes back.
	c.Keys("reopen-grid-filter", "/")
	c.Keys("clear-grid-filter", harness.KeyEsc)
	c.WaitGone("filtered from 1000", 10*time.Second)
	c.RequireContains("1000 rows in")
	c.Shot("filter-cleared")
}

// sqRowTotal parses the total column out of a copied orders row.
func sqRowTotal(t *testing.T, row string) float64 {
	t.Helper()
	cells := strings.Split(row, "\t")
	if len(cells) < 5 {
		t.Fatalf("copied row has %d cells, want at least 5: %q", len(cells), row)
	}
	v, err := strconv.ParseFloat(cells[4], 64)
	if err != nil {
		t.Fatalf("total column %q is not a number (row %q): %v", cells[4], row, err)
	}
	return v
}

// sqScalarFloat reads one numeric value through database/sql. It is
// deliberately read as text (CAST ... AS TEXT) so the comparison with the grid
// never depends on the driver's float formatting.
func sqScalarFloat(t *testing.T, db *sql.DB, query string) float64 {
	t.Helper()
	text := scenario.ScalarString(t, db, query)
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("scalar %q is not a number: %q (%v)", query, text, err)
	}
	return v
}

// TestSQLiteBigWeirdQuoting opens weird.db, the hostile-schema database: names
// that need quoting (reserved words, spaces, uppercase, non-ASCII), declared
// types, empty tables and generated columns. The generated SQL for those
// relations must quote every identifier, and executing it must return the rows.
func TestSQLiteBigWeirdQuoting(t *testing.T) {
	c := sqStartWeird(t)
	c.RequireContains("conn weird.db (sqlite)")
	c.RequireMatch(`1 schema / 19 tables`)
	c.Shot("connected")

	// Catalog: one schema, eighteen tables, one view, one trigger, four indexes.
	c.FocusObjects()
	c.Keys("expand-connection", harness.KeyRight)
	c.WaitFor("main", dialWait)
	c.Keys("objects-home", harness.KeyHome)
	dialObjectDown(c, 1)
	c.RequireMatch(`Object 2/6  main`)
	c.Keys("expand-schema", harness.KeyRight)
	c.WaitFor("Tables (18)", dialWait)
	c.RequireContains("Tables (18)", "Views (1)", "Triggers (1)", "Indexes (4)")
	c.Shot("catalog-groups")

	c.Keys("to-tables", harness.KeyDown)
	c.Keys("expand-tables", harness.KeyRight)
	c.WaitFor("wide_40", dialWait)
	c.RequireContains("select", "order", "weird table with spaces", "UPPERCASE",
		"empty table", "types test", "表")
	c.Shot("weird-tables")

	// --- Generated SQL for the reserved-word table: quoted, runnable. ---
	got := dialGenerate(c, "order", dialIconTable, "order", "Generate SELECT", `FROM "main"."order";`)
	want := "SELECT \"select\", \"from\", \"where\", \"rank\"\nFROM \"main\".\"order\";"
	if got != want {
		c.Fail("generated SELECT for \"order\"\n got: %q\nwant: %q", got, want)
	}
	c.Shot("generated-order-select")
	c.Keys("run-generated-order", harness.KeyF5)
	c.WaitFor("downstream", dialWait)
	c.RequireContains("1 row in")
	c.Shot("generated-order-ran")
	c.Keys("close-order-tab", harness.KeyCtrlW)

	// --- Generated SQL for a table name with spaces: each identifier quoted. ---
	got = dialGenerate(c, "weird table", dialIconTable, "weird table with spaces", "Generate SELECT", `"main"."weird table with spaces";`)
	want = "SELECT \"First Name\", \"full name\", \"col-with-dash\", \"  leading\", \"trailing  \"\n" +
		"FROM \"main\".\"weird table with spaces\";"
	if got != want {
		c.Fail("generated SELECT for \"weird table with spaces\"\n got: %q\nwant: %q", got, want)
	}
	c.Shot("generated-spaces-select")
	c.Keys("run-generated-spaces", harness.KeyF5)
	c.WaitFor("Alice Smith", dialWait)
	c.RequireContains("1 row in")
	c.Shot("generated-spaces-ran")
	c.Keys("close-spaces-tab", harness.KeyCtrlW)

	// --- Generated SQL for the non-ASCII table name. ---
	got = dialGenerate(c, "表", dialIconTable, "表", "Generate SELECT", `FROM "main"."表";`)
	want = "SELECT \"键\", \"值\"\nFROM \"main\".\"表\";"
	if got != want {
		c.Fail("generated SELECT for 表\n got: %q\nwant: %q", got, want)
	}
	c.Shot("generated-cjk-select")
	c.Keys("run-cjk-select", harness.KeyF5)
	c.WaitFor("中文", dialWait)
	c.RequireContains("1 row in")
	// The CJK value is clipped to "中文…" by the grid: column widths are
	// measured in runes while the renderer truncates by display cells (see the
	// scenario report), so only the prefix is on screen.
	c.Logf("CJK value on screen: %q", dialGridSummary(c))
	c.Shot("generated-cjk-ran")
	c.Keys("close-cjk-tab", harness.KeyCtrlW)

	// --- Declared types and NULLs: three rows, the NULL row spelled out. ---
	// The table name needs double quotes, which the editor takes verbatim.
	c.Keys("focus-editor", harness.Alt('2'))
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("types-sql", "SELECT id, int_col, real_col, text_col FROM \"types test\" ORDER BY id;")
	c.Keys("run-types", harness.KeyF5)
	c.WaitFor("text value", dialWait)
	c.RequireContains("42", "3.14159", "unicode", "NULL")
	c.RequireMatch(`3 rows in \d+ms`)
	c.Shot("weird-types")

	// --- The empty table: zero rows, no error. ---
	dialRunWait(c, "SELECT CONCAT('empty=', COUNT(*)) AS n FROM \"empty table\";", "empty=0")
	c.RequireContains("empty=0", "1 row in")
	c.Shot("empty-table")

	// --- Stored generated columns are materialised in the grid. ---
	dialRunWait(c, "SELECT id, a, b, sum_stored, product FROM \"gen_col\" ORDER BY id;", "12")
	c.RequireContains("12", "3", "6")
	c.Shot("generated-columns")
}
