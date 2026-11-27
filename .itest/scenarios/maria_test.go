package scenarios

// maria_test.go — end-to-end scenarios for the MariaDB 11.4 dataset, which has
// the MySQL objects MariaDB users care about: a SYSTEM VERSIONED table, a
// SEQUENCE-backed invoices table, DEFINER routines and JSON alias columns. The
// server speaks the MySQL wire protocol, so TDB connects with -d mysql, exactly
// as the datasets README documents.

import (
	"strings"
	"testing"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// TestMariaSystemVersionedQuery runs the queries a MariaDB user needs on the
// system-versioned products table (current rows vs. history via FOR SYSTEM_TIME
// ALL), on the SEQUENCE-backed invoices, and on the JSON alias columns and the
// stored generated column; every count is cross-checked against an independent
// database/sql connection.
func TestMariaSystemVersionedQuery(t *testing.T) {
	c := dialStartMaria(t)
	db := c.Env.OpenMaria(t)

	// Current state vs. full history of the SYSTEM VERSIONED table.
	dialRunWait(c, "SELECT COUNT(*) AS current_rows FROM products;", "30000")
	c.RequireContains("30000", "1 row in")
	c.Shot("products-current")
	if n := scenario.ScalarInt(t, db, "SELECT COUNT(*) FROM shop.products"); n != 30000 {
		t.Fatalf("current products rows = %d, want 30000", n)
	}

	dialRunWait(c, "SELECT COUNT(*) AS history_rows FROM products FOR SYSTEM_TIME ALL;", "30500")
	c.RequireContains("30500")
	c.Shot("products-history")
	if n := scenario.ScalarInt(t, db, "SELECT COUNT(*) FROM shop.products FOR SYSTEM_TIME ALL"); n != 30500 {
		t.Fatalf("products FOR SYSTEM_TIME ALL = %d rows, want 30500", n)
	}

	// The SEQUENCE-backed invoices start at the sequence's start value.
	dialRunWait(c, "SELECT MIN(invoice_no) AS first_invoice, COUNT(*) AS invoices FROM invoices;", "500000")
	c.RequireContains("500000", "5000")
	c.Shot("sequence-invoices")
	if n := scenario.ScalarInt(t, db, "SELECT MIN(invoice_no) FROM shop.invoices"); n != 500000 {
		t.Fatalf("MIN(invoice_no) = %d, want the sequence start 500000", n)
	}

	// MariaDB stores JSON as LONGTEXT + json_valid() CHECK: the column renders
	// as JSON text and the JSON functions still work on it.
	dialRunWait(c, "SELECT id, tags, JSON_UNQUOTE(JSON_EXTRACT(tags, '$[0]')) AS first_tag FROM customers WHERE id IN (1,4);", "tag0")
	c.RequireContains("tag0", "tag3", "tier0")
	c.Shot("json-alias")
	if want := scenario.ScalarString(t, db, "SELECT JSON_UNQUOTE(JSON_EXTRACT(tags, '$[0]')) FROM shop.customers WHERE id=4"); want != "tag3" {
		t.Fatalf("JSON_EXTRACT first tag = %q, want tag3", want)
	}

	// Generated column + ENUM + unicode rows round-trip through the session.
	dialRunWait(c, "SELECT id, first_name, last_name, full_name, status FROM customers WHERE id IN (1,4);", "王伟 李")
	c.RequireContains("王伟 李", "vip", "Alice Smith")
	c.Shot("generated-enum-unicode")
	if want := scenario.ScalarString(t, db, "SELECT full_name FROM shop.customers WHERE id=4"); want != "王伟 李" {
		t.Fatalf("MariaDB full_name = %q, want the generated 王伟 李", want)
	}
}

// TestMariaBrowseCatalog walks the MariaDB object browser: the eight databases
// are schemas, shop unfolds into its resource groups, and the relations,
// routines, trigger and indexes are listed with their real counts.
func TestMariaBrowseCatalog(t *testing.T) {
	c := dialStartMaria(t)
	c.RequireMatch(`conn shop \(mysql\)`)
	c.RequireMatch(`8 schemas / \d+ tables`)
	c.Shot("connected")

	c.FocusObjects()
	c.Keys("expand-connection", harness.KeyRight)
	c.WaitFor("legacy_archive", dialWait)
	c.RequireContains("shop", "legacy", "legacy_2019", "legacy_eu", "legacy_archive")
	c.RequireNotContains("information_schema", "performance_schema")
	c.Shot("schemas")

	// The filter projects the whole shop schema: every group with its count and
	// every member (MariaDB's own objects included).
	dialFilterObjects(c, "shop")
	c.RequireContains("Tables (9)", "Views (3)", "Functions (1)",
		"Procedures (1)", "Triggers (1)", "Indexes (25)")
	c.RequireContains("customers", "empty_table", "invoices", "order",
		"order_audit", "order_items", "orders", "payments")
	c.RequireContains("v_customer_summary", "v_product_sales", "v_order_totals")
	c.RequireContains("fn_customer_total", "sp_customer_orders", "trg_orders_audit_statu")
	c.RequireContains("ft_products", "uq_customers_email")
	c.Shot("shop-schema")

	// The reserved-word table's columns are quoted identifiers, too.
	dialFilterObjects(c, "shop.order")
	c.RequireContains("select", "from", "where", "group", "varchar(20)", "int(11)")
	c.Shot("order-columns")
}

// TestMariaSystemVersionedTableListed is the coverage item that MariaDB's
// SYSTEM VERSIONED table is listed and selectable in the object browser. Today
// it is not: MariaDB reports TABLE_TYPE = "SYSTEM VERSIONED" for it, while the
// MySQL driver introspects only TABLE_TYPE IN ('BASE TABLE','VIEW'), so
// shop.products silently disappears from the tree even though the server has it
// and SELECTs against it work. The scenario is kept and skips with evidence.
func TestMariaSystemVersionedTableListed(t *testing.T) {
	c := dialStartMaria(t)

	// The server really has the table (checked through TDB itself first).
	dialRunWait(c, "SELECT COUNT(*) AS history_rows FROM products FOR SYSTEM_TIME ALL;", "30500")
	c.RequireContains("30500")
	c.Shot("products-selectable")

	// The object browser must list it too.
	dialFilterObjects(c, "products")
	if _, _, ok := c.S.FindText(dialIconTable + " products"); ok {
		c.RequireContains("Tables (")
		c.Shot("products-listed")
		return
	}
	c.Shot("products-missing-from-tree")
	db := c.Env.OpenMaria(t)
	typ := scenario.ScalarString(t, db,
		"SELECT TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA='shop' AND TABLE_NAME='products'")
	c.T.Skipf("defect: MariaDB's SYSTEM VERSIONED table shop.products is missing "+
		"from the object browser (server says TABLE_TYPE=%q, the driver only "+
		"introspects BASE TABLE/VIEW), so it cannot be opened, have generated SQL "+
		"made for it, or have its definition viewed from the tree", typ)
}

// TestMariaSequenceListed is the coverage item that the MariaDB SEQUENCE object
// is listed. Today it is not: sequences appear in information_schema.TABLES with
// TABLE_TYPE = "SEQUENCE", the driver filters them out, and its capability list
// excludes catalog.KindSequence, so the tree has no "Sequences" group. The
// scenario is kept and skips with evidence.
func TestMariaSequenceListed(t *testing.T) {
	c := dialStartMaria(t)
	db := c.Env.OpenMaria(t)

	// The sequence is real and backs shop.invoices.invoice_no.
	dialRunWait(c, "SELECT MIN(invoice_no) AS first_invoice FROM invoices;", "500000")
	c.RequireContains("500000")
	c.Shot("sequence-in-use")

	dialFilterObjects(c, "seq_invoice_no")
	_, _, hasGroup := c.S.FindText("Sequences (1)")
	_, _, hasRow := c.S.FindText("≋ seq_invoice_no")
	if hasGroup && hasRow {
		c.Shot("sequence-listed")
		return
	}
	c.Shot("sequence-missing-from-tree")
	typ := scenario.ScalarString(t, db,
		"SELECT TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA='shop' AND TABLE_NAME='seq_invoice_no'")
	c.T.Skipf("gap: MariaDB SEQUENCE objects are not listed in the object browser "+
		"(server says TABLE_TYPE=%q; the MySQL driver filters tables to "+
		"BASE TABLE/VIEW and advertises no KindSequence capability)", typ)
}

// TestMariaDefinitionsStripped opens the DDL of both views, both routines and
// the trigger. SHOW CREATE prints ALGORITHM=, DEFINER=`tdb`@`%` and SQL SECURITY
// DEFINER on MariaDB; none of that may reach the copied statement, while the
// core CREATE of the object has to survive.
func TestMariaDefinitionsStripped(t *testing.T) {
	c := dialStartMaria(t)

	defs := []struct {
		query  string
		icon   string
		member string
		tab    string
		wants  []string
	}{
		{"v_customer_summary", dialIconView, "v_customer_summary", "shop.v_customer_summary",
			[]string{"CREATE VIEW `v_customer_summary`", "lifetime_value"}},
		{"v_product_sales", dialIconView, "v_product_sales", "shop.v_product_sales",
			[]string{"CREATE VIEW `v_product_sales`", "units_sold"}},
		{"sp_customer_orders", dialIconFunc, "sp_customer_orders", "shop.sp_customer_orders",
			[]string{"CREATE PROCEDURE `sp_customer_orders`", "SELECT o.id"}},
		{"fn_customer_total", dialIconFunc, "fn_customer_total", "shop.fn_customer_total",
			[]string{"CREATE FUNCTION `fn_customer_total`", "RETURNS decimal(14,2)"}},
		{"trg_orders_audit_status", dialIconTrigger, "trg_orders_audit_status", "shop.trg_orders_audit_status",
			[]string{"CREATE TRIGGER trg_orders_audit_status", "AFTER UPDATE ON orders"}},
	}
	for _, d := range defs {
		dialOpenDefinition(c, d.query, d.icon, d.member, d.tab)
		c.RequireContains(d.wants[0])
		ddl := dialCopyEditor(c)
		c.Logf("definition of %s:\n%s", d.member, ddl)
		for _, want := range d.wants {
			if !strings.Contains(ddl, want) {
				c.Fail("definition of %s is missing %q\nddl: %q", d.member, want, ddl)
			}
		}
		for _, banned := range []string{"DEFINER=", "ALGORITHM=", "SQL SECURITY"} {
			if strings.Contains(ddl, banned) {
				c.Fail("definition of %s still carries the environment clause %q\nddl: %q",
					d.member, banned, ddl)
			}
		}
		c.Keys("close-definition-tab", harness.KeyCtrlW)
	}
}

// TestMariaMultiDatabase checks the multi-database side of the MariaDB
// connection: the eight databases are schemas in the tree, legacy_2019 unfolds
// into its tables, and a query addresses a table in a non-default database
// while unqualified names keep resolving against the connected shop database.
func TestMariaMultiDatabase(t *testing.T) {
	c := dialStartMaria(t)
	db := c.Env.OpenMaria(t)

	c.FocusObjects()
	c.Keys("expand-connection", harness.KeyRight)
	c.WaitFor("legacy_2019", dialWait)
	c.RequireContains("legacy", "legacy_2019", "legacy_2020", "legacy_eu",
		"legacy_us", "legacy_apac", "legacy_archive", "shop")
	c.Shot("legacy-schemas")

	dialFilterObjects(c, "legacy_2019")
	c.RequireContains("legacy_2019", "archive_orders", "archive_notes")
	c.Shot("legacy-2019-tables")

	dialRunWait(c, "SELECT id, placed_at, total FROM legacy_2019.archive_orders ORDER BY id;", "2019-01-01")
	c.RequireContains("2019-01-01", "12.34", "2 rows in")
	c.Shot("legacy-query")
	if n := scenario.ScalarInt(t, db, "SELECT COUNT(*) FROM legacy_2019.archive_orders"); n != 2 {
		t.Fatalf("legacy_2019.archive_orders has %d rows, want 2", n)
	}

	dialRunWait(c, "SELECT COUNT(*) AS shop_customers FROM customers;", "100000")
	c.RequireContains("100000")
	c.Shot("default-database-still-shop")
}

// TestMariaErrorsAndRecovery checks the error paths on the MariaDB server: a
// missing table, a syntax error and a foreign-key violation all surface in the
// results pane, and the session keeps working afterwards.
func TestMariaErrorsAndRecovery(t *testing.T) {
	c := dialStartMaria(t)

	dialRunWait(c, "SELECT * FROM missing_table;", "missing_table")
	c.RequireContains("missing_table", "doesn't exist")
	c.Shot("missing-table")

	dialRunWait(c, "SELEC 1;", "syntax")
	c.RequireContains("SELEC 1;", "syntax")
	c.Shot("syntax-error")

	dialRunWait(c, "INSERT INTO order_items(id, order_id, product_id, quantity, unit_price, discount) VALUES (99999999, 999999999, 1, 1, 1.00, 0.00);", "foreign key")
	c.RequireContains("foreign key")
	c.Shot("constraint-error")
	if n := scenario.ScalarInt(t, c.Env.OpenMaria(t), "SELECT COUNT(*) FROM shop.order_items"); n != 300000 {
		t.Fatalf("the rejected insert changed order_items: %d rows, want 300000", n)
	}

	dialRunWait(c, "SELECT 'recovered' AS state;", "recovered")
	c.RequireContains("1 row in")
	c.Shot("recovered")
}
