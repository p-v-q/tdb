package scenarios

// showcase_test.go produces the curated screenshots used in the integration
// report: a busy PostgreSQL session, the MySQL object browser with generated
// SQL, the Simplified Chinese UI, and the welcome page with saved connections.
// They are ordinary scenarios (they assert what they show) that double as
// evidence.

import (
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// TestShowcasePostgres: connect to the large PostgreSQL shop, unfold the tree
// and run a join with sorting, exactly like an analyst starting the day.
func TestShowcasePostgres(t *testing.T) {
	c := scenario.New(t)
	c.Start("-d", "postgres", "-dsn", c.Env.PGDSN)
	c.WaitFor("conn shop", 40*time.Second)
	c.RequireContains("conn shop", "7 schemas")
	c.Shot("pg-connected")

	// Open the object browser and walk into sales.order_items.
	c.FocusObjects()
	c.Keys("unfold-connection", harness.KeyRight)
	c.WaitFor("sales", 15*time.Second)
	c.Shot("pg-schemas")
	c.Keys("to-sales", harness.KeyDown)
	c.Keys("unfold-sales", harness.KeyRight)
	c.WaitFor("Tables", 15*time.Second)
	c.Shot("pg-sales-groups")

	// Run a realistic join and sort it by revenue. The statement is typed line
	// by line: a multi-line paste through a pseudo console can lose line feeds.
	c.Keys("select-all", harness.KeyCtrlA)
	c.TypeSQL("join", `SELECT c.id, c.email, count(o.id) AS orders, sum(o.total) AS spent
FROM public.customers c
JOIN sales.orders o ON o.customer_id = c.id
GROUP BY c.id, c.email
ORDER BY spent DESC
LIMIT 50;`)
	c.Keys("run-join", harness.KeyF5)
	c.WaitFor("spent", 90*time.Second)
	c.RequireNotContains("Query failed")
	c.FocusResults()
	c.Keys("sort-spent", harness.KeyRight)
	for i := 0; i < 6; i++ {
		c.Keys("right", harness.KeyRight)
	}
	c.Keys("sort", "s")
	c.RequireContains("50 rows", "orders")
	c.Shot("pg-join-sorted")

	// The query plan of the same kind of statement.
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("plan-sql", "SELECT count(*) FROM sales.order_items;")
	c.Keys("run-plan", harness.KeyF5)
	c.WaitFor("count", 90*time.Second)
	c.Keys("explain", harness.KeyF6)
	c.WaitFor("Aggregate", 30*time.Second)
	c.Shot("pg-plan")
}

// TestShowcaseMySQL: browse the MySQL shop, look at the generated SELECT of a
// table with a reserved-word name and at a JSON value in the grid.
func TestShowcaseMySQL(t *testing.T) {
	c := scenario.New(t)
	c.Start("-d", "mysql", "-dsn", c.Env.MySQLDSN)
	c.WaitFor("conn shop", 40*time.Second)
	c.Shot("mysql-connected")

	c.FocusObjects()
	c.Keys("unfold-connection", harness.KeyRight)
	c.WaitFor("legacy", 15*time.Second)
	// The databases of the server are the schemas: walk down to "shop" and
	// unfold it, then its Tables group.
	for i := 0; i < 7; i++ {
		c.Keys("to-shop", harness.KeyDown)
	}
	c.Keys("unfold-shop", harness.KeyRight)
	c.WaitFor("Tables", 15*time.Second)
	c.Keys("to-tables", harness.KeyDown)
	c.Keys("unfold-tables", harness.KeyRight)
	c.WaitFor("customers", 15*time.Second)
	c.Shot("mysql-groups")

	runSQL(c, "SELECT id, sku, name, price, meta->>'$.color' AS color FROM products ORDER BY id LIMIT 20;")
	c.WaitFor("color", 60*time.Second)
	c.Shot("mysql-json-grid")
}

// TestShowcaseChinese: the Simplified Chinese interface with real data, to
// check wide glyphs, alignment and the localized strings.
func TestShowcaseChinese(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-lang", "zh-Hans", "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("个模式", 25*time.Second)
	c.RequireContains("欢迎")
	c.Shot("chinese-welcome")

	runSQL(c, "SELECT id, name, city FROM users WHERE name IS NOT NULL ORDER BY id;")
	c.WaitFor("行，用时", 30*time.Second)
	c.Shot("chinese-grid")
}

// TestShowcaseWelcome: the welcome page with several saved connections.
func TestShowcaseWelcome(t *testing.T) {
	c := scenario.New(t)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "demo")
	c.WaitFor("Welcome to TDB", 25*time.Second)
	c.Shot("welcome-connections")
	c.RequireContains("pg-shop", "mysql-shop", "maria-shop", "sqlite-shop")
}
