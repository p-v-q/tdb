package scenarios

import (
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// startShopSQLite launches TDB on the standard scenario database.
func startShopSQLite(t *testing.T) *scenario.Ctx {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)
	return c
}

// runSQL replaces the editor content and runs it, the way a user does:
// select all, type the statement, F5.
func runSQL(c *scenario.Ctx, sql string) {
	c.T.Helper()
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", sql)
	c.Keys("run", harness.KeyF5)
}

// TestInteractionMenuRun drives Query -> Run through the menu bar with the
// keyboard (Alt+Q, then R) instead of the F5 shortcut.
func TestInteractionMenuRun(t *testing.T) {
	c := startShopSQLite(t)
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", "SELECT id, email, city FROM users ORDER BY id;")
	c.Menu('q', 'r')
	c.WaitFor("ada@example.com", 25*time.Second)
	c.RequireContains("5 rows in", "grace@example.com", "Shanghai", "NULL")
	c.Shot("grid")
}

// TestInteractionGridSortAndFilter sorts with the keyboard, flips the
// direction and filters the grid down to one row.
func TestInteractionGridSortAndFilter(t *testing.T) {
	c := startShopSQLite(t)
	runSQL(c, "SELECT id, email, city FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)

	c.FocusResults()
	c.Keys("caret-to-email", harness.KeyRight) // id -> email
	c.Keys("sort-asc", "s")
	c.Shot("sorted-asc")
	c.RequireOrderBefore("ada@example.com", "yuan@example.com")

	c.Keys("sort-desc", "s")
	c.Shot("sorted-desc")
	c.RequireOrderBefore("yuan@example.com", "ada@example.com")

	c.Keys("caret-to-city", harness.KeyRight) // email -> city
	c.Keys("open-filter", "/")
	c.Type("filter-text", "Shanghai")
	c.Keys("apply-filter", harness.KeyEnter)
	c.WaitFor("filtered from 5", 10*time.Second)
	c.RequireContains("lin@example.com")
	c.RequireNotContains("ada@example.com")
	c.Shot("filtered")

	// "/" then Esc clears the filter again.
	c.Keys("reopen-filter", "/")
	c.Keys("clear-filter", harness.KeyEsc)
	c.WaitFor("ada@example.com", 10*time.Second)
}

// TestInteractionTabs runs two independent query tabs and switches between
// them, then closes the first.
func TestInteractionTabs(t *testing.T) {
	c := startShopSQLite(t)
	runSQL(c, "SELECT email FROM users ORDER BY email;")
	c.WaitFor("ada@example.com", 20*time.Second)

	c.Keys("new-tab", harness.KeyCtrlT)
	c.Type("second-sql", "SELECT COUNT(*) AS order_count FROM orders;")
	c.Keys("run-second", harness.KeyF5)
	c.WaitFor("order_count", 20*time.Second)
	c.RequireContains("8")
	c.Shot("second-tab")

	// Ctrl+PgUp cannot carry its modifier through a pseudo console, so switch
	// tabs through the View menu, which is a real user path as well.
	c.Menu('v', 'p')
	c.RequireContains("SELECT email FROM users")
	c.Shot("first-tab")

	c.Menu('v', 'n')
	c.RequireContains("COUNT(*) AS order_count")

	c.Keys("close-tab", harness.KeyCtrlW)
	c.WaitGone("COUNT(*) AS order_count", 10*time.Second)
	c.Shot("after-close")
}

// TestInteractionCompletion opens the completion popup with Ctrl+Space and
// accepts an entry. The direct shortcut works on Windows again through the
// key-name alias in the editor.
func TestInteractionCompletion(t *testing.T) {
	c := startShopSQLite(t)
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-prefix", "SELECT * FROM u")
	c.Keys("open-popup", harness.KeyCtrlSpace)
	c.WaitFor("users", 10*time.Second) // the popup is the only place "users" shows
	c.Shot("popup")

	c.Keys("accept", harness.KeyEnter)
	c.Shot("accepted")
	c.Type("finish", ";")
	c.Keys("run", harness.KeyF5)
	c.WaitFor("ada@example.com", 20*time.Second)
	c.RequireContains("5 rows in")
	c.Shot("results")
}

// TestInteractionFormatAndComment toggles block comments with the documented
// Ctrl+/ shortcut and reformats the statement through the Edit menu.
func TestInteractionFormatAndComment(t *testing.T) {
	c := startShopSQLite(t)
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", "select id,email,city from users where age>30 order by id;")

	c.Keys("select-all", harness.KeyCtrlA)
	c.Keys("comment", harness.KeyCtrlSlash)
	c.RequireContains("-- select")
	c.Shot("commented")

	c.Keys("select-all", harness.KeyCtrlA)
	c.Keys("uncomment", harness.KeyCtrlSlash)
	c.RequireNotContains("-- select")

	c.Keys("select-all", harness.KeyCtrlA)
	c.Menu('e', 'f') // Edit -> Format
	c.Shot("formatted")
	screen := c.Screen()
	for _, want := range []string{"SELECT", "FROM users", "WHERE", "ORDER BY"} {
		if !strings.Contains(screen, want) {
			c.Fail("formatted SQL is missing %q\n%s", want, screen)
		}
	}
	// Format puts each clause on its own line.
	if !strings.Contains(screen, "FROM users\n") && c.RowOf("WHERE") <= c.RowOf("FROM users") {
		c.Fail("clauses must be laid out one per line\n%s", screen)
	}
}

// TestInteractionHistory opens the query history and reopens the last
// statement in a fresh tab.
func TestInteractionHistory(t *testing.T) {
	c := startShopSQLite(t)
	runSQL(c, "SELECT 1 AS one;")
	c.WaitFor("one", 20*time.Second)
	runSQL(c, "SELECT 2 AS two;")
	c.WaitFor("two", 20*time.Second)

	c.Keys("history", "\x1bq")  // open the Query menu the real way...
	c.Keys("history-menu", "h") // ...then its History item
	c.WaitFor("Query History", 10*time.Second)
	c.RequireContains("SELECT 2 AS two", "SELECT 1 AS one")
	c.Shot("history-dialog")

	c.Keys("reopen", harness.KeyEnter)
	c.WaitGone("Query History", 10*time.Second)
	c.RequireContains("SELECT 2 AS two")
	c.Shot("reopened")
}

// TestInteractionWelcomeConnections clicks a saved connection on the welcome
// page and connects to it.
func TestInteractionWelcomeConnections(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "welcome",
		scenario.SavedConnection{Name: "shop-local", Driver: "sqlite", File: dbPath},
	)
	c.StartWithSettings(settings, "-d", "demo")
	c.WaitFor("Welcome to TDB", 20*time.Second)
	c.RequireContains("shop-local")
	c.Shot("welcome")

	// The connection row sits below New query and Open SQL File.
	c.Keys("move-down-1", harness.KeyDown)
	c.Keys("move-down-2", harness.KeyDown)
	c.Shot("selected")
	c.Keys("connect", harness.KeyEnter)
	c.WaitFor("conn shop-local", 25*time.Second)
	c.RequireContains("Connected to shop-local")
	c.Shot("connected")

	// Walk the object browser the way a user does: focus it, unfold the
	// connection, its schema, the Tables group, and read the table names.
	c.Keys("focus-objects", "\x1b1")
	c.Keys("unfold-connection", harness.KeyRight)
	c.WaitFor("main", 10*time.Second)
	c.Keys("down-to-schema", harness.KeyDown)
	c.Keys("unfold-schema", harness.KeyRight)
	c.WaitFor("Tables", 10*time.Second)
	c.Keys("down-to-tables", harness.KeyDown)
	c.Keys("unfold-tables", harness.KeyRight)
	c.WaitFor("users", 10*time.Second)
	c.RequireContains("orders", "products")
	c.Shot("browser")
}

// TestInteractionMouse sorts by clicking the column header and opens the Query
// menu by clicking its title.
func TestInteractionMouse(t *testing.T) {
	c := startShopSQLite(t)
	runSQL(c, "SELECT id, email, city FROM users ORDER BY id;")
	c.WaitFor("ada@example.com", 25*time.Second)

	// Clicking a column header sorts by that column.
	c.ClickText("email")
	c.RequireOrderBefore("ada@example.com", "yuan@example.com")
	c.Shot("clicked-header")

	// Clicking a menu title opens its dropdown.
	c.ClickText("Query")
	c.RequireContains("Run", "Explain")
	c.Shot("menu-open")
	c.Keys("close-menu", harness.KeyEsc)
	c.RequireNotContains("Explain")
}

// TestInteractionExplain runs the query plan for a statement with F6. SQLite
// shows its bytecode program (addr/opcode/... columns).
func TestInteractionExplain(t *testing.T) {
	c := startShopSQLite(t)
	c.Keys("select-all", harness.KeyCtrlA)
	c.Type("typed-sql", "SELECT * FROM orders;")
	c.Keys("explain", harness.KeyF6)
	c.WaitFor("opcode", 20*time.Second)
	c.Shot("explain")
}
