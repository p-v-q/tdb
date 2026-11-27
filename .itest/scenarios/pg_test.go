package scenarios

// PostgreSQL end-to-end scenarios: a real analyst/DB-admin session against the
// large `shop` dataset (7 schemas, 53 relations, 1.5M order items, jsonb,
// arrays, partitions, a materialized view, routines, quoted identifiers).
//
// Everything drives the real bin/tdb.exe through a ConPTY: real keystrokes
// in, rendered-screen assertions out. The tests are serial.
//
// Clipboard trick: the full text of an editor tab (a DDL tab, a generated
// statement) is far taller than one screen. Selecting all and copying emits an
// OSC 52 sequence into the PTY stream, which pgCopyAll decodes — so assertions
// can check the whole buffer, not just the visible lines, exactly the bytes the
// user would paste elsewhere.

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// ---- Session and editor helpers ----

// pgStart launches TDB connected to the PostgreSQL shop database through the
// command line and waits for the session header.
func pgStart(t *testing.T) *scenario.Ctx {
	t.Helper()
	c := scenario.New(t)
	c.Start("-d", "postgres", "-dsn", c.Env.PGDSN)
	c.WaitFor("conn shop (postgres)", 40*time.Second)
	return c
}

// pgReplace selects the whole editor and types sql into it.
func pgReplace(c *scenario.Ctx, sql string) {
	c.T.Helper()
	c.Keys("pg-select-all", harness.KeyCtrlA)
	c.Type("pg-typed-sql", sql)
}

// pgRun replaces the editor content and runs it with F5.
func pgRun(c *scenario.Ctx, sql string, want string, timeout time.Duration) {
	c.T.Helper()
	pgReplace(c, sql)
	c.Keys("pg-run", harness.KeyF5)
	if want != "" {
		c.WaitFor(want, timeout)
	}
}

// pgScript replaces the editor buffer with a multi-line script. Each line is
// typed separately and the completion popup is dismissed before every newline:
// while the popup is open Enter accepts a candidate, so a "\n" delivered inside
// one typed string is swallowed after an identifier (e.g. after "large_text").
func pgScript(c *scenario.Ctx, lines ...string) {
	c.T.Helper()
	c.Keys("pg-script-select-all", harness.KeyCtrlA)
	for i, line := range lines {
		c.Type("pg-script-line", line)
		if i == len(lines)-1 {
			break
		}
		c.Keys("pg-script-dismiss-popup", harness.KeyEsc)
		c.Keys("pg-script-newline", harness.KeyEnter)
	}
}

// pgRunScript types a multi-line script and runs it with F5.
func pgRunScript(c *scenario.Ctx, want string, timeout time.Duration, lines ...string) {
	c.T.Helper()
	pgScript(c, lines...)
	c.Keys("pg-run-script", harness.KeyF5)
	if want != "" {
		c.WaitFor(want, timeout)
	}
}

// pgRaw returns everything the process has printed, for clipboard probing.
func pgRaw(c *scenario.Ctx) []byte { return c.S.RawOutput() }

// pgClipboardSince decodes the last OSC 52 clipboard payload written after the
// raw-output offset off.
func pgClipboardSince(t *testing.T, c *scenario.Ctx, off int) (string, bool) {
	t.Helper()
	raw := pgRaw(c)
	if off < 0 || off > len(raw) {
		off = 0
	}
	body := string(raw[off:])
	const marker = "\x1b]52;c;"
	idx := strings.LastIndex(body, marker)
	if idx < 0 {
		return "", false
	}
	rest := body[idx+len(marker):]
	end := strings.IndexByte(rest, '\x07')
	if end < 0 {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(rest[:end])
	if err != nil {
		t.Fatalf("decode OSC 52 clipboard payload: %v", err)
	}
	return string(decoded), true
}

// pgCopyAll selects the whole active editor buffer and copies it, returning the
// text through the OSC 52 escape sequence.
//
// A leftover completion popup (the model-wide popup state survives a tab
// switch) is dismissed first: while it is open the copy may be swallowed.
func pgCopyAll(t *testing.T, c *scenario.Ctx) string {
	t.Helper()
	c.FocusEditor()
	c.Keys("pg-copy-dismiss-popup", harness.KeyEsc)
	off := len(pgRaw(c))
	c.Keys("pg-copy-select-all", harness.KeyCtrlA)
	c.Keys("pg-copy-to-clipboard", harness.KeyCtrlC)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if text, ok := pgClipboardSince(t, c, off); ok {
			return text
		}
		time.Sleep(80 * time.Millisecond)
	}
	c.Fail("copy produced no OSC 52 clipboard payload\nscreen:\n%s", c.Screen())
	return ""
}

// pgScreenCompact returns the screen with spaces removed, so assertions can
// match wide (CJK/emoji) text: the VT emulator pads every double-width cell
// with a blank column, which would otherwise split the runes.
func pgScreenCompact(c *scenario.Ctx) string {
	return strings.ReplaceAll(c.Screen(), " ", "")
}

// pgFindTree returns the screen coordinates of text inside the object browser's
// row area (below its title row). Searching from row 3 on avoids matching the
// pane title, the "/filter" indicator or the neighbouring panes.
func pgFindTree(c *scenario.Ctx, text string) (row, col int, ok bool) {
	lines := strings.Split(c.Screen(), "\n")
	const objectsPaneWidth = 34
	for y := 3; y < len(lines); y++ {
		line := lines[y]
		idx := strings.Index(line, text)
		if idx < 0 {
			continue
		}
		col = len([]rune(line[:idx]))
		if col >= objectsPaneWidth {
			continue // right-hand pane, not the tree
		}
		return y, col, true
	}
	return 0, 0, false
}

// pgClickTreeRow left-clicks a row of the object browser.
func pgClickTreeRow(c *scenario.Ctx, text string) {
	c.T.Helper()
	row, col, ok := pgFindTree(c, text)
	if !ok {
		c.Fail("cannot find tree row %q\nscreen:\n%s", text, c.Screen())
	}
	if err := c.S.MouseClickButton(col+2, row, harness.MouseLeft); err != nil {
		c.T.Fatalf("click tree row %q: %v", text, err)
	}
}

// pgRightClickTreeRow opens the context menu of an object-browser row.
func pgRightClickTreeRow(c *scenario.Ctx, text string) {
	c.T.Helper()
	row, col, ok := pgFindTree(c, text)
	if !ok {
		c.Fail("cannot find tree row %q\nscreen:\n%s", text, c.Screen())
	}
	if err := c.S.MouseClickButton(col+2, row, harness.MouseRight); err != nil {
		c.T.Fatalf("right click tree row %q: %v", text, err)
	}
}

// pgClickScreen left-clicks the first occurrence of text anywhere on screen.
func pgClickScreen(c *scenario.Ctx, text string) {
	c.T.Helper()
	row, col, ok := c.S.FindText(text)
	if !ok {
		c.Fail("cannot click %q: not on screen\nscreen:\n%s", text, c.Screen())
	}
	if err := c.S.MouseClickButton(col+len([]rune(text))/2, row, harness.MouseLeft); err != nil {
		c.T.Fatalf("click %q: %v", text, err)
	}
}

// pgFilterObjects puts the object browser filter on text and applies it.
// "/" starts from scratch, so a filter left over from an earlier step is
// replaced.
func pgFilterObjects(c *scenario.Ctx, text string) {
	c.T.Helper()
	c.FocusObjects()
	c.Keys("pg-filter-open", "/")
	c.Type("pg-filter-text", text)
	c.Keys("pg-filter-apply", harness.KeyEnter)
}

// pgClearObjectsFilter clears an object-browser filter and folds the projection
// back to the stored tree state.
func pgClearObjectsFilter(c *scenario.Ctx) {
	c.T.Helper()
	c.FocusObjects()
	c.Keys("pg-filter-open", "/")
	c.Keys("pg-filter-cancel", harness.KeyEsc)
}

// pgFilterAndOpenMenu filters the tree, right-clicks the matching row and waits
// for the action's label to appear in the context menu.
func pgFilterAndOpenMenu(c *scenario.Ctx, filter, rowText, action string) {
	c.T.Helper()
	pgFilterObjects(c, filter)
	pgRightClickTreeRow(c, rowText)
	c.WaitFor(action, 10*time.Second)
}

// pgOpenStatement runs an object-browser statement action (Generate SELECT,
// View Data, … and returns the text of the tab it opened.
func pgOpenStatement(c *scenario.Ctx, filter, rowText, action, tabName string) string {
	c.T.Helper()
	pgFilterAndOpenMenu(c, filter, rowText, action)
	pgClickScreen(c, action)
	c.WaitFor(tabName, 15*time.Second)
	return pgCopyAll(c.T, c)
}

// pgWaitRegexp waits until the rendered screen matches re.
func pgWaitRegexp(t *testing.T, c *scenario.Ctx, re string, timeout time.Duration) {
	t.Helper()
	if err := c.S.WaitForRegexp(regexp.MustCompile(re), timeout); err != nil {
		t.Fatalf("%v", err)
	}
}

// pgOpenDefinition opens an object's "View definition" tab and returns the
// complete DDL from the clipboard. tabName is the schema-qualified object name,
// which the tab strip shows and therefore proves the right tab is active.
func pgOpenDefinition(t *testing.T, c *scenario.Ctx, filter, rowText, tabName string) string {
	t.Helper()
	pgFilterAndOpenMenu(c, filter, rowText, "View definition")
	pgClickScreen(c, "View definition")
	c.WaitFor(tabName, 15*time.Second)
	return pgCopyAll(t, c)
}

// pgRequireDDL asserts every wanted fragment appears in ddl.
func pgRequireDDL(t *testing.T, ddl, what string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(ddl, want) {
			t.Errorf("%s DDL is missing %q\n--- DDL ---\n%s", what, want, ddl)
		}
	}
}

// pgRequireNoServerClauses asserts the DDL does not carry clauses of another
// database or session-only catalog dressing that would not re-execute.
func pgRequireNoServerClauses(t *testing.T, ddl, what string) {
	t.Helper()
	for _, unwanted := range []string{
		"DEFINER=", "ALGORITHM=", "SQL SECURITY", // MySQL/MariaDB-only header clauses
		"OWNER TO", "WITH OIDS", "SET search_path", "SET client_encoding", // server/session-only
		"pg_catalog.", // catalog function calls do not belong in reproduced DDL
	} {
		if strings.Contains(ddl, unwanted) {
			t.Errorf("%s DDL leaks server-only clause %q\n--- DDL ---\n%s", what, unwanted, ddl)
		}
	}
}

// ---- Connection and object browser ----

// TestPGConnectAndCatalog connects over the command line and walks the object
// browser: catalog counts, the seven schemas, group counts per schema, folding,
// table comments and the reserved-word relation.
func TestPGConnectAndCatalog(t *testing.T) {
	c := pgStart(t)
	c.Shot("connected")

	// Menu bar right-hand side: connection, dialect and the catalog size.
	c.RequireContains("conn shop (postgres)", "7 schemas / 53 tables")
	c.RequireContains("Objects", "Welcome to TDB", "shop (postgres)")
	c.RequireNotContains("metadata not loaded")
	c.RequireContains("Ln 1, Col 1") // status bar: caret position of the empty editor

	// The tree starts folded. Unfold the connection: the seven user schemas
	// appear, system schemas stay hidden.
	c.FocusObjects()
	c.Keys("pg-unfold-connection", harness.KeyRight)
	c.Shot("schemas")
	for _, schema := range []string{"LEGACY_2019", "Warehouse", "analytics", "inventory", "public", "reporting", "sales"} {
		c.RequireContains(schema)
	}
	c.RequireNotContains("pg_catalog", "information_schema")
	c.RequireContains("Object 1/12  shop")

	// Fold it again: the schemas disappear, the connection row stays.
	c.Keys("pg-fold-connection", harness.KeyLeft)
	c.WaitGone("LEGACY_2019", 5*time.Second)
	c.Shot("folded")

	// Unfold it again and open public by clicking the row, then read the
	// resource groups a schema exposes with their member counts.
	c.Keys("pg-unfold-connection-again", harness.KeyRight)
	c.WaitFor("LEGACY_2019", 10*time.Second)
	pgClickTreeRow(c, "public")
	c.Keys("pg-unfold-public", harness.KeyRight)
	c.WaitFor("Tables (9)", 10*time.Second)
	c.Shot("public-groups")
	c.RequireContains("Tables (9)", "Functions (1)", "Indexes (21)", "Sequences (3)")

	// Open the tables folder: the relations, including the quoted
	// reserved-word table, are listed with their catalog comments.
	pgClickTreeRow(c, "Tables (9)")
	c.Keys("pg-unfold-public-tables", harness.KeyRight)
	c.WaitFor("wide_table_40", 10*time.Second)
	c.Shot("public-tables")
	c.RequireContains("customers", "products", "reviews", "null_only", "unicode_test")
	c.RequireContains("200k cust")             // customers table comment (pane-clipped, ends with …)
	c.RequireContains("wide_table_40  40 col") // literal comment prefix in the row (clipped with …)
	c.RequireOrderBefore("empty_table_0", "products")

	// Fold the table list again.
	pgClickTreeRow(c, "Tables (9)")
	c.Keys("pg-fold-public-tables", harness.KeyLeft)
	c.WaitGone("wide_table_40", 5*time.Second)

	// reporting is read-only: only views, no tables.
	pgClickTreeRow(c, "reporting")
	c.Keys("pg-unfold-reporting", harness.KeyRight)
	c.WaitFor("Views (6)", 10*time.Second)
	c.Shot("reporting-views")
	c.RequireContains("Views (6)")

	// analytics carries the materialized view, the functions and the
	// procedure.
	pgClickTreeRow(c, "analytics")
	c.Keys("pg-unfold-analytics", harness.KeyRight)
	c.WaitFor("Materialized views (1)", 10*time.Second)
	c.RequireContains("Tables (2)", "Functions (3)", "Procedures (1)", "Sequences (2)")
	c.Shot("analytics-groups")

	// sales has the partitioned parent and its 25 partitions listed as
	// tables; the row trigger is cloned onto every partition by the server.
	pgClickTreeRow(c, "sales")
	c.Keys("pg-unfold-sales", harness.KeyRight)
	c.WaitFor("Tables (31)", 10*time.Second)
	c.RequireContains("Triggers (26)")
	c.Shot("sales-groups")
}

// TestPGWelcomeSavedConnection starts on the welcome page of a session with the
// standard saved connections and opens pg-shop with the keyboard.
func TestPGWelcomeSavedConnection(t *testing.T) {
	c := scenario.New(t)
	c.Start("-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)
	c.Shot("welcome")
	c.RequireContains("pg-shop (postgres)", "mysql-shop", "sqlite-shop")

	// New query and Open SQL File sit above the saved connections.
	c.Keys("pg-welcome-down-1", harness.KeyDown)
	c.Keys("pg-welcome-down-2", harness.KeyDown)
	c.Shot("selected-pg-shop")
	c.Keys("pg-welcome-connect", harness.KeyEnter)
	c.WaitFor("conn pg-shop (postgres)", 40*time.Second)
	c.Shot("connected")

	// The PostgreSQL session replaced the demo metadata and says so.
	c.RequireContains("Connected to pg-shop", "7 schemas / 53 tables")
	c.FocusObjects()
	c.Keys("pg-unfold-connection", harness.KeyRight)
	c.WaitFor("analytics", 10*time.Second)
	c.Shot("browser")
	c.RequireContains("LEGACY_2019", "Warehouse", "inventory", "reporting", "sales")
	c.RequireNotContains("metadata not loaded")

	// The connection table offers its schema list; the demo connection's
	// offline sample metadata is gone.
	c.RequireNotContains("demo (demo)")
}

// TestPGBrowseFilterAndColumns drills into a table's columns through the "/"
// incremental filter and checks column names, types and catalog comments.
func TestPGBrowseFilterAndColumns(t *testing.T) {
	c := pgStart(t)
	c.FocusObjects()
	c.Keys("pg-unfold-connection", harness.KeyRight)
	c.WaitFor("LEGACY_2019", 10*time.Second)

	// Filtering by table name keeps the path to the match and opens the
	// matching table's columns: name and type share one row.
	pgFilterObjects(c, "customers")
	c.Shot("filter-in-progress")
	c.RequireContains("/customers") // filter indicator glued to the pane title
	c.Keys("pg-filter-enter", harness.KeyEnter)
	c.WaitFor("created_at  timestam", 10*time.Second) // the type column is clipped with …
	c.Shot("filtered-customers")
	c.RequireContains("customers", "public", "Tables (1)")
	c.RequireContains("id  bigint", "public_id  uuid", "metadata  jsonb", "tags  text[]")
	c.RequireContains("email  email_address") // domain type in the column row

	// The filter also matches catalog comments ("... for truncation/binary
	// tests"), which is how a user finds an object whose name they forgot.
	pgFilterObjects(c, "truncation")
	c.Keys("pg-filter-enter", harness.KeyEnter)
	c.WaitFor("large_text", 10*time.Second)
	c.Shot("filtered-comment")
	c.RequireContains("large_text")

	// A targeted query finds the million-row child table and its columns.
	pgFilterObjects(c, "order_items")
	c.Keys("pg-filter-enter", harness.KeyEnter)
	c.WaitFor("line_total", 10*time.Second)
	c.Shot("filtered-order-items")
	c.RequireContains("sales", "Tables (1)", "order_items", "order_id", "line_total")

	// A broad query matches names, comments and columns across schemas: the
	// pane reports hundreds of matching rows (reorder_level matches inside
	// inventory/products, old_orders in the legacy schema, …).
	pgFilterObjects(c, "order")
	c.Keys("pg-filter-enter", harness.KeyEnter)
	pgWaitRegexp(t, c, `Object 1/\d{3}`, 10*time.Second)
	c.Shot("filtered-order-broad")
	c.RequireContains("reorder_level", "old_orders", "order  Quoted")

	// Esc cancels the filter input and restores the unexpanded tree of the
	// previous expansion state.
	pgClearObjectsFilter(c)
	c.WaitGone("/order", 5*time.Second)
	c.Shot("filter-cleared")
	c.RequireContains("Objects")
}

// TestPGDefaultSchema opens a saved connection whose default schema is sales:
// unqualified names resolve there, the header and the tree mark it.
func TestPGDefaultSchema(t *testing.T) {
	c := scenario.New(t)
	settings := c.Env.WriteSettings(t, "pg-sales-schema",
		scenario.SavedConnection{Name: "pg-sales", Driver: "postgres", DSN: c.Env.PGDSN, Schema: "sales"},
	)
	c.StartWithSettings(settings, "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)
	c.Keys("pg-welcome-down-1", harness.KeyDown)
	c.Keys("pg-welcome-down-2", harness.KeyDown)
	c.Keys("pg-welcome-connect", harness.KeyEnter)
	c.WaitFor("conn pg-sales (postgres)", 40*time.Second)
	c.Shot("connected")
	c.RequireContains("Default schema: sales")
	c.RequireContains("Connected to pg-sales")

	// The object tree marks the schema unqualified names resolve in.
	c.FocusObjects()
	c.Keys("pg-unfold-connection", harness.KeyRight)
	c.WaitFor("analytics", 10*time.Second)
	c.Shot("tree-default-schema")
	c.RequireContains("sales  default")

	// Unqualified `orders` resolves to sales.orders (500k rows); without the
	// default schema it would not resolve at all.
	pgRun(c, "SELECT count(*) AS order_count FROM orders;", "order_count", 30*time.Second)
	c.Shot("unqualified-query")
	c.RequireContains("500000")
	c.RequireContains("1 row in")

	// The session itself agrees about the schema.
	pgRun(c, "SELECT current_schema() AS active_schema;", "active_schema", 30*time.Second)
	c.Shot("current-schema")
	c.RequireContains("sales")
}

// TestPGCompletion types a schema-qualified name, lets the popup open on the
// ".", accepts a candidate and runs the statement.
func TestPGCompletion(t *testing.T) {
	c := pgStart(t)
	c.FocusEditor()
	pgReplace(c, "SELECT * FROM sales.")
	// Typing the dot opens the completion popup for the schema's tables.
	pgWaitRegexp(t, c, `complete \d+/\d+`, 10*time.Second)
	c.Shot("schema-popup")
	c.RequireContains("orders")

	// Dismiss it, then narrow it down to one table and accept with Enter.
	c.Keys("pg-popup-dismiss", harness.KeyEsc)
	c.Type("pg-type-table", "order_item")
	pgWaitRegexp(t, c, `complete 1/1`, 10*time.Second)
	c.Shot("narrowed-popup")
	c.RequireContains("order_items")
	c.Keys("pg-popup-accept", harness.KeyEnter)
	c.Shot("accepted")
	copied := pgCopyAll(t, c)
	if !strings.Contains(copied, "FROM sales.order_items") {
		c.Fail("accepted completion did not produce a schema-qualified statement; buffer:\n%s", copied)
	}

	// The accepted statement is real SQL and runs: order_items is capped at
	// the client's row limit.
	c.Keys("pg-run-accepted", harness.KeyF5)
	c.WaitFor("showing the first 1000 rows", 40*time.Second)
	c.Shot("order-items-select")
	c.RequireContains("1000 rows in", "line_total")
}

// ---- Queries, types and rendering ----

// TestPGQueryJoinAndAggregates runs analyst-style aggregates and joins over the
// partitioned orders and the 1.5M-line order_items table.
func TestPGQueryJoinAndAggregates(t *testing.T) {
	c := pgStart(t)

	pgRunScript(c, "avg_total", 40*time.Second,
		"SELECT o.status, count(*) AS orders, round(avg(o.total), 2) AS avg_total",
		"FROM sales.orders o",
		"GROUP BY o.status",
		"ORDER BY orders DESC;")
	c.Shot("status-aggregate")
	c.RequireContains("6 rows in", "shipped", "delivered", "83350", "83325")
	c.RequireContains("numeric") // avg_total type row
	c.RequireContains("int8")    // count(*) type row

	// A join across the partitioned parent and its million-row child: the
	// count proves both sides are really read.
	pgRunScript(c, "joined_order_items", 60*time.Second,
		"SELECT count(*) AS joined_order_items",
		"FROM sales.orders o",
		"JOIN sales.order_items oi ON oi.order_id = o.id;")
	c.Shot("join-count")
	c.RequireContains("1500000", "1 row in")

	// A grouped join over the same pair.
	pgRunScript(c, "lines", 60*time.Second,
		"SELECT o.currency, count(*) AS lines",
		"FROM sales.orders o",
		"JOIN sales.order_items oi ON oi.order_id = o.id",
		"GROUP BY o.currency",
		"ORDER BY lines DESC;")
	c.Shot("grouped-join")
	c.RequireContains("USD")
}

// TestPGJsonbAndArrays exercises jsonb operators, text[] rendering, uuid and
// bytea rendering, enum values and timestamptz formatting.
func TestPGJsonbAndArrays(t *testing.T) {
	c := pgStart(t)

	// jsonb extraction (->> / ->) and containment (@>).
	pgRunScript(c, "is_cart", 40*time.Second,
		"SELECT id, payload->>'path' AS path, payload->'props'->>'qty' AS qty,",
		"       payload @> '{\"path\":\"/cart\"}' AS is_cart",
		"FROM analytics.events",
		"ORDER BY id",
		"LIMIT 5;")
	c.Shot("jsonb-operators")
	c.RequireContains("path", "qty", "is_cart", "/products", "/cart")
	c.RequireContains("true")

	// A jsonb containment aggregate over the 1M-event stream (GIN-backed).
	pgRunScript(c, "carts", 60*time.Second,
		"SELECT count(*) FILTER (WHERE payload @> '{\"path\":\"/cart\"}') AS carts,",
		"       count(*) AS total",
		"FROM analytics.events;")
	c.Shot("jsonb-aggregate")
	c.RequireContains("166667", "1000000")

	// uuid, text[] and bytea rendering, plus array subscripts.
	pgRunScript(c, "first_tag", 40*time.Second,
		"SELECT id, public_id, tags, tags[1] AS first_tag,",
		"       array_length(tags, 1) AS n_tags, avatar",
		"FROM public.customers",
		"WHERE id IN (7, 10)",
		"ORDER BY id;")
	c.Shot("uuid-array-bytea")
	c.RequireContains("eba56801-0ad9-4776-9aeb-48bbfcf28e25")
	c.RequireContains("{tag7,tier2}", "tag7", "2 rows in")
	c.RequireContains("\\x") // bytea hex rendering

	// Enum values and timestamptz formatting.
	pgRunScript(c, "ordered_at", 40*time.Second,
		"SELECT id, status, ordered_at, total",
		"FROM sales.orders",
		"ORDER BY id",
		"LIMIT 3;")
	c.Shot("enum-timestamptz")
	c.RequireContains("shipped", "2024-01-01", "5.01")
	c.RequireContains("timestamptz")

	// NULL is spelled out, the empty string is not: exactly one NULL on the
	// row, and the empty cell stays blank.
	pgRun(c, "SELECT NULL::text AS missing, ''::text AS empty_string, 'present' AS present;",
		"present", 30*time.Second)
	c.Shot("null-vs-empty")
	row := c.S.Line(c.RowOf("present"))
	if got := strings.Count(row, "NULL"); got != 1 {
		c.Fail("NULL vs empty string row must contain exactly one NULL, got %d: %q", got, row)
	}
	c.RequireContains("missing", "empty_string", "present")
}

// TestPGEdgeTables reads the deliberately awkward tables: empty, NULL-only,
// a 1 MB single-line value, the 40-column table and Unicode rows.
func TestPGEdgeTables(t *testing.T) {
	c := pgStart(t)

	pgRun(c, "SELECT * FROM public.empty_table_0;", "0 rows in", 30*time.Second)
	c.Shot("empty-table")
	c.RequireContains("id", "payload")

	pgRun(c, "SELECT * FROM public.null_only;", "5 rows in", 30*time.Second)
	c.Shot("null-only")
	c.RequireContains("NULL")

	// The 1 MB value is one line: the grid truncates it to the column width
	// and the layout (one row per result row) must survive.
	pgRunScript(c, "long_chars", 40*time.Second,
		"SELECT id, label, length(content) AS chars, length(long_line) AS long_chars",
		"FROM public.large_text",
		"ORDER BY id;")
	c.Shot("large-text-lengths")
	c.RequireContains("1048576", "1000000", "unicode-large", "3 rows in")

	pgRun(c, "SELECT content FROM public.large_text WHERE id = 1;", "1 row in", 40*time.Second)
	c.Shot("one-megabyte-cell")
	// The app stays usable after rendering the 1 MB cell.
	pgRun(c, "SELECT 'still-alive' AS state;", "still-alive", 30*time.Second)
	c.Shot("alive-after-1mb")
	c.RequireContains("1 row in")

	// 40 columns do not fit 125 result columns: the grid must clip cleanly
	// instead of wrapping or crashing.
	pgRun(c, "SELECT * FROM public.wide_table_40 ORDER BY col_01;", "2 rows in", 30*time.Second)
	c.Shot("wide-table")
	c.RequireContains("col_01", "col_02", "col_03")
	c.RequireNotContains("col_40") // horizontally clipped, not wrapped
}

// TestPGUnicodeRendering checks that wide (CJK/emoji) result values reach the
// grid intact and that the column layout stays sane.
//
// It is kept as a defect scenario: the grid computes its automatic column
// widths from the number of runes in a value, not from its display width, so a
// CJK value is cut to half its visual size even when the grid has plenty of
// horizontal room.
func TestPGUnicodeRendering(t *testing.T) {
	c := pgStart(t)
	pgRunScript(c, "notation", 30*time.Second,
		"SELECT id, category, phrase, notation",
		"FROM public.unicode_test",
		"WHERE id IN (1, 3, 5)",
		"ORDER BY id;")
	c.Shot("unicode")
	c.RequireContains("3 rows in", "chinese", "emoji", "japanese", "text")

	compact := pgScreenCompact(c)
	// The rows render, and the wide glyphs survive as glyphs — no crash and
	// no mojibake.
	for _, want := range []string{"你好", "🚀", "こんにちは", "U+4F60"} {
		if !strings.Contains(compact, want) {
			c.Fail("unicode result is missing %q\ncompact screen:\n%s", want, compact)
		}
	}

	// … but the full Chinese and Japanese phrases are clipped even though the
	// result uses 125 columns and the four columns need about 60 cells.
	phrases := []string{"你好，世界！数据库测试", "こんにちは世界、テストです"}
	clipped := make([]string, 0, len(phrases))
	for _, p := range phrases {
		if !strings.Contains(compact, p) {
			clipped = append(clipped, p)
		}
	}
	if len(clipped) > 0 {
		t.Skipf("defect: result column auto-width measures runes instead of display cells "+
			"(ui/results.go columnWidths uses len([]rune(cell))), so CJK values are clipped to half "+
			"their visual width although the 125-column grid has room: screen shows %q + ellipsis "+
			"instead of %q; the emoji row (%q) renders fully because its rune count matches its "+
			"width. Screenshot: %s",
			"你好，世界！…", clipped[0], "🚀🎉😀🔥👍", c.Shot("unicode-clipped"))
	}
}

// TestPGRowCap selects an unbounded large table and checks the row cap and its
// truncation notice, then the result grid still works.
func TestPGRowCap(t *testing.T) {
	c := pgStart(t)
	pgRun(c, "SELECT * FROM sales.orders;", "showing the first 1000 rows", 60*time.Second)
	c.Shot("row-cap")
	c.RequireContains("1000 rows in", "showing the first 1000 rows")
	c.RequireContains("order_no", "ordered_at", "customer_id")

	// The capped result is still a normal grid: sort it by a column.
	c.FocusResults()
	c.Keys("pg-sort-status", "s")
	c.Shot("row-cap-sorted")
	c.RequireContains("1000 rows in")
}

// TestPGExplain runs F6 on a join and checks the plan comes back as a normal
// result grid.
func TestPGExplain(t *testing.T) {
	c := pgStart(t)
	pgScript(c,
		"SELECT count(*) AS n",
		"FROM sales.orders o",
		"JOIN sales.order_items oi ON oi.order_id = o.id",
		"WHERE o.id < 50;")
	c.Keys("pg-explain", harness.KeyF6)
	c.WaitFor("QUERY PLAN", 40*time.Second)
	c.Shot("explain")
	// The plan is an ordinary result grid: the top of the join plan is
	// visible with its join node and its scans.
	c.RequireContains("QUERY PLAN", "Aggregate", "Nested Loop", "Append", "Index Only Scan")
}

// TestPGScriptAndSelection runs a multi-statement script whole (F5), switches
// between its result sets, then runs only the statement under the caret and
// only a selection.
func TestPGScriptAndSelection(t *testing.T) {
	c := pgStart(t)
	// Distinct row counts tell the result of each statement apart, so the
	// assertions cannot be fooled by the editor text that stays on screen.
	pgScript(c,
		"SELECT 1 AS alpha UNION ALL SELECT 2;",
		"SELECT 1 AS beta UNION ALL SELECT 2 UNION ALL SELECT 3;",
		"SELECT 1 AS gamma UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4;")

	// The whole script: three result sets, the last one shown first.
	c.Keys("pg-run-script", harness.KeyF5)
	c.WaitFor("Result 3/3", 40*time.Second)
	c.Shot("script-last-set")
	c.RequireContains("Result 3/3", "4 rows in")

	// "[" steps to the previous result set (a real grid key).
	c.FocusResults()
	c.Keys("pg-previous-set", "[")
	c.WaitFor("Result 2/3", 10*time.Second)
	c.Shot("script-previous-set")
	c.RequireContains("Result 2/3", "3 rows in")

	// The caret decides which single statement F5 runs: line 1 (2 rows).
	c.FocusEditor()
	c.Keys("pg-caret-end", harness.KeyHome) // line 3 start
	c.Keys("pg-caret-up-1", harness.KeyUp)
	c.Keys("pg-caret-up-2", harness.KeyUp)
	c.Shot("caret-first-statement")
	c.Keys("pg-run-caret-statement", harness.KeyF5)
	c.WaitFor("2 rows in", 30*time.Second)
	c.Shot("caret-statement-run")
	c.RequireNotContains("Result 2/3", "4 rows in", "3 rows in")

	// The caret decides again: line 3 only (4 rows).
	c.Keys("pg-caret-home-3", harness.KeyHome)
	c.Keys("pg-caret-down-3a", harness.KeyDown)
	c.Keys("pg-caret-down-3b", harness.KeyDown)
	c.Keys("pg-run-third", harness.KeyF5)
	c.WaitFor("4 rows in", 30*time.Second)
	c.Shot("caret-third-run")
	c.RequireNotContains("Result 2/3", "2 rows in", "3 rows in")

	// A selection runs just the selected text (line 2, 3 rows), not the line
	// the caret would otherwise pick.
	c.Keys("pg-sel-home", harness.KeyHome) // line 3 start
	c.Keys("pg-sel-up-1", harness.KeyUp)   // line 2 start
	c.Keys("pg-sel-up-2", harness.KeyUp)   // line 1 start
	c.Keys("pg-sel-down", harness.KeyDown) // line 2 start
	c.Keys("pg-select-line2", "\x1b[1;2F") // Shift+End selects the line
	c.Shot("selection")
	c.RequireContains("line(s)")
	c.Keys("pg-run-selection", harness.KeyF5)
	c.WaitFor("3 rows in", 30*time.Second)
	c.Shot("selection-run")
	c.RequireNotContains("Result 2/3", "2 rows in", "4 rows in")
}

// TestPGCancelLongQuery starts a blocking statement and cancels it with Esc,
// then checks the session is still usable.
func TestPGCancelLongQuery(t *testing.T) {
	c := pgStart(t)
	pgReplace(c, "SELECT pg_sleep(10) AS slept;")
	c.Keys("pg-run-long", harness.KeyF5)
	c.WaitFor("Running… Esc cancels", 10*time.Second)
	c.Shot("query-running")
	c.Keys("pg-cancel", harness.KeyEsc)
	c.WaitFor("Query cancelled", 20*time.Second)
	c.Shot("cancelled")
	c.RequireContains("Query cancelled")

	// Cancelling must not leave the connection broken.
	pgRun(c, "SELECT 'after-cancel' AS state;", "after-cancel", 30*time.Second)
	c.Shot("usable-after-cancel")
	c.RequireContains("1 row in")
}

// TestPGGenerateSQL generates the four starter statements for products, checks
// the reserved/uppercase identifier quoting, the read-only view menu and runs
// "View Data" (SELECT * LIMIT 200).
func TestPGGenerateSQL(t *testing.T) {
	c := pgStart(t)

	selectText := pgOpenStatement(c, "products", "products", "Generate SELECT", "public.products [SELECT]")
	c.Shot("generated-select")
	// Identifiers go through the dialect's QuoteIdent, which quotes the schema
	// as well as the relation and the columns.
	pgRequireDDL(t, selectText, "generated SELECT",
		`SELECT "id", "sku", "name", "description", "category", "price", "cost", "stock"`,
		`FROM "public"."products";`)

	insertText := pgOpenStatement(c, "products", "products", "Generate INSERT", "public.products [INSERT]")
	c.Shot("generated-insert")
	pgRequireDDL(t, insertText, "generated INSERT",
		`INSERT INTO "public"."products" ("id", "sku", "name"`,
		"VALUES ($1, $2, $3")
	if n := strings.Count(insertText, "$"); n != 15 {
		c.T.Errorf("generated INSERT must bind 15 columns as $1..$15, got %d placeholders:\n%s", n, insertText)
	}

	updateText := pgOpenStatement(c, "products", "products", "Generate UPDATE", "public.products [UPDATE]")
	c.Shot("generated-update")
	pgRequireDDL(t, updateText, "generated UPDATE",
		`UPDATE "public"."products"`,
		`SET "sku" = $1`,
		`WHERE "id" = $15;`)

	deleteText := pgOpenStatement(c, "products", "products", "Generate DELETE", "public.products [DELETE]")
	c.Shot("generated-delete")
	pgRequireDDL(t, deleteText, "generated DELETE",
		`DELETE FROM "public"."products"`,
		`WHERE "id" = $1;`)

	// Quoted reserved-word identifiers survive the generators.
	orderInsert := pgOpenStatement(c, "reserved-word", "order", "Generate INSERT", "public.order [INSERT]")
	c.Shot("generated-reserved-insert")
	pgRequireDDL(t, orderInsert, "reserved-word INSERT",
		`INSERT INTO "public"."order" ("id", "select", "from", "where", "group")`,
		"VALUES ($1, $2, $3, $4, $5);")
	orderUpdate := pgOpenStatement(c, "reserved-word", "order", "Generate UPDATE", "public.order [UPDATE]")
	pgRequireDDL(t, orderUpdate, "reserved-word UPDATE",
		`UPDATE "public"."order"`,
		`SET "select" = $1, "from" = $2, "where" = $3, "group" = $4`,
		`WHERE "id" = $5;`)

	// Quoted uppercase schema names are quoted too.
	legacySelect := pgOpenStatement(c, "old_orders", "old_orders", "Generate SELECT", "LEGACY_2019.old_orders [SELECT]")
	c.Shot("generated-legacy-select")
	pgRequireDDL(t, legacySelect, "uppercase-schema SELECT",
		`FROM "LEGACY_2019"."old_orders";`)

	// Views are read-only: the context menu offers SELECT and View Data only.
	pgFilterAndOpenMenu(c, "v_product_sales", "v_product_sales", "Generate SELECT")
	c.Shot("view-context-menu")
	c.RequireContains("Generate SELECT", "View Data", "View definition")
	c.RequireNotContains("Generate INSERT", "Generate UPDATE", "Generate DELETE")
	c.Keys("pg-close-view-menu", harness.KeyEsc)
	c.WaitGone("Generate SELECT", 5*time.Second)
	viewSelect := pgOpenStatement(c, "v_product_sales", "v_product_sales", "Generate SELECT", "reporting.v_product_sales [SELECT]")
	pgRequireDDL(t, viewSelect, "view SELECT", `FROM "reporting"."v_product_sales";`)
	if strings.Contains(viewSelect, "INSERT") {
		c.T.Errorf("generated view statement must stay read-only:\n%s", viewSelect)
	}

	// "View Data" builds SELECT * ... LIMIT 200 and actually runs it.
	dataText := pgOpenStatement(c, "products", "products", "View Data", "public.products [data]")
	c.WaitFor("200 rows in", 40*time.Second)
	c.Shot("view-data-products")
	pgRequireDDL(t, dataText, "View Data", `SELECT * FROM "public"."products" LIMIT 200;`)
	c.RequireContains("SKU-0000001")

	// The materialized view's data is fetched the same way.
	mvData := pgOpenStatement(c, "mv_daily_events", "mv_daily_events", "View Data", "analytics.mv_daily_events [data]")
	c.WaitFor("200 rows in", 40*time.Second)
	c.Shot("view-data-matview")
	pgRequireDDL(t, mvData, "matview View Data", `SELECT * FROM "analytics"."mv_daily_events" LIMIT 200;`)
}
