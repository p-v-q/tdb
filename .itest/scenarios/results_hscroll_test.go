package scenarios

// results_hscroll_test.go covers the result grid's horizontal scrollbar: a
// query wider than the pane shows a track under the columns, and dragging it
// reveals the columns that were off screen.

import (
	"strings"
	"testing"
	"time"

	"tdb-itest/harness"
	"tdb-itest/scenario"
)

// wideSelect builds a statement with enough wide columns to overflow the grid.
func wideSelect() string {
	names := []string{
		"first_long_column_name", "second_long_column_name", "third_long_column_name",
		"fourth_long_column_name", "fifth_long_column_name", "sixth_long_column_name",
		"seventh_long_column_name", "eighth_long_column_name", "ninth_long_column_name",
		"tenth_long_column_name", "eleventh_long_column_name", "twelfth_long_column_name",
	}
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = "'" + strings.Repeat(string(rune('a'+i)), 22) + "' AS " + name
	}
	return "SELECT " + strings.Join(parts, ", ") + ";"
}

// TestResultsHorizontalScrollbar runs a wide query and scrolls it sideways with
// the bar: the first columns go, the last ones come in.
func TestResultsHorizontalScrollbar(t *testing.T) {
	c := scenario.New(t)
	dbPath := scenario.ShopSQLite(t, c)
	c.Start("-d", "sqlite", "-dsn", dbPath)
	c.WaitFor("conn shop.db", 25*time.Second)

	c.Keys("select-all", harness.KeyCtrlA)
	c.TypeSQL("wide", wideSelect())
	c.Keys("run", harness.KeyF5)
	c.WaitFor("1 row in", 30*time.Second)

	// The header row of the grid is the row of the first visible column name.
	headerRow := c.RowOf("first_long_column_name")
	if _, off := c.S.FindTextInRow("twelfth_long_column_name", headerRow); off {
		t.Fatal("the last column is visible before scrolling")
	}
	c.Shot("wide-before")

	// The bar is on the last body row of the results pane.
	barRow := c.RowOf("░")
	if barRow < 0 {
		t.Fatal("no scrollbar track on screen")
	}
	thumbRow := c.RowOf("█")
	if thumbRow < 0 || thumbRow != barRow {
		t.Fatalf("thumb row = %d, track row = %d, want the same row", thumbRow, barRow)
	}
	c.Shot("wide-bar")

	// Drag the thumb to the right end of its track.
	thumbX := c.ColOf("█")
	trackEnd := c.RowOfLineEnd(barRow) - 3
	if err := c.S.Drag(thumbX, barRow, trackEnd, barRow); err != nil {
		t.Fatalf("drag the horizontal scrollbar: %v", err)
	}
	if err := c.S.Settle(300 * time.Millisecond); err != nil {
		t.Fatalf("settle: %v", err)
	}
	c.WaitTextInRow("twelfth_long_column_name", headerRow, 10*time.Second)
	c.Shot("wide-scrolled")

	// A single click on the left end of the track goes back to the start.
	if err := c.S.MouseClickButton(c.ColOf("░"), barRow, harness.MouseLeft); err != nil {
		t.Fatalf("click the horizontal scrollbar: %v", err)
	}
	c.WaitTextInRow("first_long_column_name", headerRow, 10*time.Second)
	c.Shot("wide-back")
}
