package scenarios

// pane_resize_test.go covers what the screen shows while a pane divider is
// dragged: the panes keep their titles and the divider gets the active style,
// but the contents (editor text, result rows) are not laid out until the button
// is released — that is what made resizing feel slow.

import (
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// horizontalDividerRow returns the screen row of the divider above the results
// pane, found from the pane title below it.
func horizontalDividerRow(c *scenario.Ctx) int {
	c.T.Helper()
	titleRow := c.RowOf("Results")
	if titleRow < 2 {
		c.Fail("cannot find the results pane title")
	}
	return titleRow - 1
}

func TestPaneResizeDrawsChromeOnly(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	// Give both panes content worth not re-laying out on every motion.
	c.Keys("select-all", harness.KeyCtrlA)
	c.TypeSQL("resize-sql", "SELECT id, email, city FROM users;")
	c.Keys("run", harness.KeyF5)
	c.WaitFor("5 rows in", 30*time.Second)
	c.RequireContains("ada@example.com", "SELECT id, email, city FROM users;")
	c.Shot("before-resize")

	divider := horizontalDividerRow(c)
	mid := 80 // anywhere along the divider, which spans the editor pane
	if err := c.S.PressLeft(mid, divider); err != nil {
		t.Fatalf("press the divider: %v", err)
	}
	if err := c.S.DragTo(mid, divider-3); err != nil {
		t.Fatalf("drag the divider: %v", err)
	}
	// During the drag the panes are frames plus the hint; the contents are
	// deliberately not rendered.
	c.WaitFor("resizing", 10*time.Second)
	// The status message may keep saying how many rows the query returned (it
	// is one cheap line); the grid and the editor are what must not be laid
	// out.
	c.RequireNotContains("ada@example.com", "SELECT id, email, city FROM users;")
	c.RequireContains("SQL Editor", "Results")
	c.Shot("during-resize")

	// Releasing renders the full contents again.
	if err := c.S.ReleaseLeft(mid, divider-3); err != nil {
		t.Fatalf("release the divider: %v", err)
	}
	c.WaitFor("5 rows in", 20*time.Second)
	c.RequireContains("ada@example.com", "SELECT id, email, city FROM users;")
	c.RequireNotContains("resizing")
	c.Shot("after-resize")
}
