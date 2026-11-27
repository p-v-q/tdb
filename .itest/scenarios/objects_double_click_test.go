package scenarios

// objects_double_click_test.go covers the pointer's default action in the object
// browser: double-clicking a table (or view) opens its data — the bounded
// SELECT, run right away — exactly like "View data" in the context menu.

import (
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// TestDoubleClickTableOpensViewData: browse to the tables of the SQLite shop and
// double-click users; the data tab opens and its rows are on screen.
func TestDoubleClickTableOpensViewData(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	// Open the tree down to the table names.
	c.FocusObjects()
	c.Keys("unfold-connection", harness.KeyRight)
	c.WaitFor("main", 10*time.Second)
	c.Keys("to-schema", harness.KeyDown)
	c.Keys("unfold-schema", harness.KeyRight)
	c.WaitFor("Tables", 10*time.Second)
	c.Keys("to-tables", harness.KeyDown)
	c.Keys("unfold-tables", harness.KeyRight)
	c.WaitFor("users", 10*time.Second)
	c.Shot("tree-open")

	// A double click on the table name opens its data right away.
	c.DoubleClickText("users")
	c.WaitFor("ada@example.com", 25*time.Second)
	c.RequireContains("[data]", "5 rows in", "Shanghai")
	c.RequireContains("SELECT * FROM") // the generated bounded SELECT in the new tab
	c.Shot("users-data")

	// A second double click works the same way: the gesture is repeatable.
	c.FocusObjects()
	c.DoubleClickText("orders")
	c.WaitFor("8 rows in", 25*time.Second)
	c.Shot("orders-data")
}

// TestDoubleClickViewOpensViewData: a view is a relation too, and "View data"
// is offered for it in the context menu, so the double click opens it as well.
func TestDoubleClickViewOpensViewData(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	c.FocusObjects()
	c.Keys("unfold-connection", harness.KeyRight)
	c.WaitFor("main", 10*time.Second)
	c.Keys("to-schema", harness.KeyDown)
	c.Keys("unfold-schema", harness.KeyRight)
	c.WaitFor("Views", 10*time.Second)
	c.Keys("to-tables", harness.KeyDown)
	c.Keys("to-views", harness.KeyDown)
	c.Keys("unfold-views", harness.KeyRight)
	c.WaitFor("v_order_totals", 10*time.Second)
	c.Shot("views-open")

	c.DoubleClickText("v_order_totals")
	c.WaitFor("spent", 25*time.Second)
	c.RequireContains("[data]", "SELECT * FROM")
	c.Shot("view-data")
}
