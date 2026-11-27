package scenarios

// tabs_connections_test.go covers two browser-level conveniences: the bulk
// entries of the tab context menu (close others / left / right / all) and the
// stable position of a connection row when it is opened by double click.

import (
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// newTabWithSQL opens a tab and types a statement into it, so the active editor
// has a recognizable text.
func newTabWithSQL(c *scenario.Ctx, sql string) {
	c.Keys("new-tab", harness.KeyCtrlT)
	c.Type("tab-sql", sql)
	c.WaitFor(sql, 10*time.Second)
}

// TestTabContextMenuBulkClose drives the bulk closes through the real mouse: a
// right click on a tab title, then the menu entry.
func TestTabContextMenuBulkClose(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	// Tabs: 0 = Welcome, 1 = alpha, 2 = beta, 3 = gamma.
	newTabWithSQL(c, "SELECT alpha;")
	newTabWithSQL(c, "SELECT beta;")
	newTabWithSQL(c, "SELECT gamma;")
	if got := c.TabCount(); got != 4 {
		t.Fatalf("tabs = %d, want four", got)
	}
	c.Shot("three-tabs")

	// Close to the right of beta: gamma goes, beta stays.
	c.RightClickTabAt(2)
	c.WaitFor("Close others", 10*time.Second)
	c.RequireContains("Close to the left", "Close to the right", "Close all")
	c.Shot("tab-menu")
	c.ClickText("Close to the right")
	c.WaitFor("Tabs to the right closed", 10*time.Second)
	c.WaitFor("SELECT beta;", 10*time.Second)
	c.RequireNotContains("SELECT gamma;")
	if got := c.TabCount(); got != 3 {
		t.Fatalf("tabs = %d after Close to the right, want three", got)
	}
	c.Shot("right-closed")

	// Close others from beta: the welcome tab and alpha go, beta stays.
	c.RightClickTabAt(2)
	c.WaitFor("Close others", 10*time.Second)
	c.ClickText("Close others")
	c.WaitFor("Other tabs closed", 10*time.Second)
	c.WaitGone("SELECT alpha;", 10*time.Second)
	c.WaitGone("Welcome to TDB", 10*time.Second)
	c.RequireContains("SELECT beta;")
	if got := c.TabCount(); got != 1 {
		t.Fatalf("tabs = %d after Close others, want one", got)
	}
	c.Shot("others-closed")

	// One tab left: the bulk entries are not offered any more.
	c.RightClickTabAt(0)
	c.WaitFor("Duplicate", 10*time.Second)
	c.RequireNotContains("Close others", "Close to the left", "Close to the right", "Close all")
	c.Keys("dismiss", harness.KeyEsc)
	c.WaitGone("Duplicate", 5*time.Second)

	// Close all: back to the welcome page.
	newTabWithSQL(c, "SELECT delta;")
	newTabWithSQL(c, "SELECT epsilon;")
	c.RightClickTabAt(c.TabCount() - 1)
	c.WaitFor("Close all", 10*time.Second)
	c.ClickText("Close all")
	c.WaitFor("All tabs closed", 10*time.Second)
	c.WaitFor("Welcome to TDB", 15*time.Second)
	if got := c.TabCount(); got != 1 {
		t.Fatalf("tabs = %d after Close all, want the welcome tab", got)
	}
	c.Shot("all-closed")
}

// TestDoubleClickSavedConnectionKeepsPosition: opening a saved connection by
// double click must not make its row jump to the top of the tree, and the caret
// must stay on the connection that was opened.
func TestDoubleClickSavedConnectionKeepsPosition(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	settings := c.Env.WriteSettings(t, "main", c.Env.StandardConnections()...)
	c.StartWithSettings(settings, "-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)
	c.RequireOrderBefore("shop.db (sqlite)", "sqlite-shop (sqlite)")
	c.Shot("connections-before")

	// Double click the last saved connection: it becomes the live session.
	c.DoubleClickText("sqlite-shop (sqlite)")
	c.WaitFor("conn sqlite-shop", 30*time.Second)
	c.RequireContains("Connected to sqlite-shop")

	// The saved order is still there: the opened row did not move to the top of
	// the tree. (The session header repeats the name on row 0, so the tree rows
	// are compared below the panes' top border.)
	headerRow := c.RowOf("conn sqlite-shop")
	c.RequireContains("conn sqlite-shop (sqlite)")
	pgRow := c.RowOfFrom("pg-shop (postgres)", headerRow)
	liveRow := c.RowOfFrom("sqlite-shop (sqlite)", headerRow+1)
	if liveRow < pgRow {
		t.Fatalf("sqlite-shop row %d jumped above pg-shop row %d", liveRow, pgRow)
	}
	c.Shot("connections-after")

	// The caret stayed on the opened connection: Right unfolds its schemas, so
	// the tree is ready where the user is looking (a caret on the first row
	// would have opened the first saved connection instead).
	c.FocusObjects()
	c.Keys("unfold-opened", harness.KeyRight)
	c.WaitFor("main", 15*time.Second)
	c.Shot("opened-and-expanded")
}
