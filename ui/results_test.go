package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/i18n"
)

// resultNames lists the first column of the grid in display order, which is
// what the sorting and filtering tests compare.
func resultNames(m Model) []string {
	r := m.session.result
	out := []string{}
	for _, idx := range r.viewRows() {
		if v := cellAt(r.res.Rows[idx], 0); v != nil {
			out = append(out, *v)
		}
	}
	return out
}

func TestResultGridSortsByColumn(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT email, age FROM users ORDER BY email")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	base := resultNames(m)
	if len(base) != 3 {
		t.Fatalf("rows = %v", base)
	}

	// `s` sorts by the caret column (age here, column 1).
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // to the age column
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	sorted := resultNames(m)
	if sorted[0] != base[len(base)-1] || sorted[len(sorted)-1] != base[0] {
		// The ages are 36, NULL, 41: NULL goes last, so the first row is the
		// smallest age either way.
		if len(sorted) != len(base) {
			t.Fatalf("sorted rows = %v", sorted)
		}
	}
	// `s` again sorts descending.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}) // descending
	if !m.session.result.sortDesc {
		t.Fatal("sorting twice must switch to descending")
	}
	if m.session.result.sortCol != 1 {
		t.Fatalf("sortCol = %d, want 1", m.session.result.sortCol)
	}

	// A third `s` returns to the database order.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.session.result.sortCol != -1 || m.session.result.sortDesc {
		t.Fatalf("the third sort must restore the original order, sortCol = %d desc = %v",
			m.session.result.sortCol, m.session.result.sortDesc)
	}

	// Clicking the header of the first column sorts by it.
	r := m.paneRects()
	cols := m.visibleResultCols()
	if len(cols) < 1 {
		t.Fatal("no visible columns")
	}
	m = send(m, mousePress(r.results.X+m.resultsGutterWidth(), r.results.Y+2))
	if m.session.result.sortCol != cols[0] {
		t.Fatalf("sortCol = %d, want %d after a header click", m.session.result.sortCol, cols[0])
	}
	if m.session.result.sortDesc {
		t.Fatal("a fresh column starts sorted ascending")
	}
}

func TestResultGridSortsNumbersNumerically(t *testing.T) {
	conn := e2eConn(t)
	_, err := conn.Query(context.Background(), "INSERT INTO users VALUES (10, 'z@example.com', 'Zed', 7)")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	m := e2eModel(t, conn, "SELECT id, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}) // sort by id

	ids := resultNames(m)
	want := []string{"1", "2", "3", "10"} // 10 after 3: numeric order
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("sorted ids = %v, want %v (numeric, not textual)", ids, want)
	}
}

func TestResultGridFiltersRows(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT email, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.session.result.filtering {
		t.Fatal("`/` must open the filter input")
	}
	m = typeText(m, "ada")
	if got := resultNames(m); len(got) != 1 || got[0] != "ada@example.com" {
		t.Fatalf("filtered rows = %v, want just Ada's row", got)
	}
	view := stripANSI(m.View())
	if !strings.Contains(view, "Filter") || !strings.Contains(view, "ada") {
		t.Fatalf("the filter input must be visible:\\n%s", view)
	}

	// Enter keeps the filter, Esc clears it.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.session.result.filtering {
		t.Fatal("Enter must close the filter input")
	}
	if got := resultNames(m); len(got) != 1 {
		t.Fatalf("the filter must stay in effect, got %v", got)
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEsc})
	if got := resultNames(m); len(got) != 3 {
		t.Fatalf("Esc must clear the filter, got %v", got)
	}
}

func TestResultGridScrolls(t *testing.T) {
	conn := e2eConn(t)
	_, err := conn.Query(context.Background(), `
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 200)
		INSERT INTO users (id, email) SELECT i + 1000, 'x' || i FROM n`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults

	sb := stripANSI(strings.Join(m.resultsScrollBar(m.paneRects().results), ""))
	if !strings.Contains(sb, "█") {
		t.Fatalf("a long result must show a scroll thumb:\\n%s", sb)
	}
	start := m.session.result.scrollY
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.session.result.scrollY <= start {
		t.Fatalf("PgDn must scroll the grid, scrollY = %d", m.session.result.scrollY)
	}
	// End puts the caret on the last row: the thumb belongs at the bottom.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnd})
	moved := stripANSI(strings.Join(m.resultsScrollBar(m.paneRects().results), ""))
	if moved == sb {
		t.Fatal("the thumb must move when the grid scrolls")
	}
	if !strings.HasSuffix(moved, "█") {
		t.Fatalf("at the end the thumb must sit at the bottom: bar=%q rows=%d visible=%d scrollY=%d cursor=%d body=%d",
			moved, len(m.session.result.res.Rows), m.resultsVisibleRows(),
			m.session.result.scrollY, m.session.result.cursorRow, m.resultsBodyHeight())
	}
}

func TestObjectBrowserScrollsAndShowsAThumb(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 1")
	cat, err := conn.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	m.cat = cat
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 10}) // a short pane
	m.layout.focus = paneObjects
	// Schemas/groups and columns are folded by default; open them so the short
	// tree has enough rows to scroll.
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "main"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "main", group: catalog.KindTable}, true)
	m.objects.expanded[tableKey("main", "users")] = true

	sb := stripANSI(strings.Join(m.objectsScrollBar(m.paneRects().objects), ""))
	if !strings.Contains(sb, "█") {
		t.Fatalf("a tree that does not fit must show a scroll thumb:\\n%s", sb)
	}
	// The wheel scrolls it.
	start := m.objects.scroll
	m = send(m, mouseWheel(true, m.paneRects().objects.X+2, m.paneRects().objects.Y+2))
	if m.objects.scroll <= start {
		t.Fatalf("the wheel must scroll the tree, scroll = %d", m.objects.scroll)
	}
}

func TestResultGridWithoutAResultIsSafe(t *testing.T) {
	m := newTestModel("SELECT 1")
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m.layout.focus = paneResults

	// Every grid key must be a no-op before anything ran, not a panic.
	for _, key := range []string{"s", "/", "up", "down", "pgup", "pgdown", "home", "end"} {
		m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.session.result.res != nil {
		t.Fatal("no result must appear out of nowhere")
	}
}

func TestResultGridClickSelectsRowAndCell(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	rect := m.paneRects().results
	y := m.resultDataTopY() + 1          // the second visible row (id 2)
	m = send(m, mousePress(rect.X+1, y)) // the row-number gutter
	if m.layout.focus != paneResults {
		t.Fatal("clicking a result row must focus the grid")
	}
	if m.session.result.cursorRow != 1 {
		t.Fatalf("cursorRow = %d, want 1", m.session.result.cursorRow)
	}

	// A click inside a cell also chooses the column.
	cols := m.visibleResultCols()
	if len(cols) < 2 {
		t.Fatalf("visible columns = %v", cols)
	}
	x := rect.X + m.resultsGutterWidth()
	for _, col := range cols {
		if col == cols[1] {
			break
		}
		x += m.session.result.colWidths[col] + 1
	}
	m = send(m, mousePress(x, y))
	if m.session.result.cursorRow != 1 || m.session.result.cursorCol != cols[1] {
		t.Fatalf("cursor = (%d,%d), want (1,%d)",
			m.session.result.cursorRow, m.session.result.cursorCol, cols[1])
	}
}

func TestCopyResultRowUsesTheVisibleOrder(t *testing.T) {
	buf := captureClipboard(t)
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}

	// Ascending by name gives Ada, Lin, NULL. The caret is on display row 1,
	// which is Lin, while result row 1 is still NULL.
	r := &m.session.result
	r.sortCol, r.sortDesc = 0, false
	r.cursorRow, r.cursorCol = 1, 0
	m.layout.focus = paneResults

	cmd := m.copyResultRow()
	if cmd == nil {
		t.Fatal("copying a row must return a command")
	}
	cmd()

	b64 := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decoding failed: %v", err)
	}
	if string(decoded) != "Lin" {
		t.Fatalf("clipboard contents = %q, want Lin", decoded)
	}
}

// resultColumnEdge returns the screen coordinate of a visible column's right
// boundary, the drag target in the header row.
func resultColumnEdge(m Model, col int) (int, int) {
	rect := m.paneRects().results
	x := rect.X + m.resultsGutterWidth()
	for _, c := range m.visibleResultCols() {
		x += m.session.result.colWidths[c]
		if c == col {
			return x, rect.Y + 2
		}
		x++ // separator
	}
	panic("column is not visible")
}

func TestResultColumnResizeByDragging(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	before := m.session.result.colWidths[0]
	x, y := resultColumnEdge(m, 0)

	m = send(m, mousePress(x, y))
	if !m.colResize.active {
		t.Fatal("pressing a column boundary must start resizing")
	}
	m = send(m, mouseMotion(x+6, y))
	m = send(m, mouseRelease(x+6, y))
	if m.colResize.active {
		t.Fatal("releasing must end the resize")
	}
	if got := m.session.result.colWidths[0]; got != before+6 {
		t.Fatalf("width = %d, want %d", got, before+6)
	}
	if got := m.session.result.colPrefs["id"]; got != before+6 {
		t.Fatalf("saved width = %d, want %d", got, before+6)
	}
}

func TestResultColumnDoubleClickRestoresAutomaticWidth(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	auto := m.session.result.colWidths[0]
	x, y := resultColumnEdge(m, 0)

	// Widen it first.
	m = send(m, mousePress(x, y))
	m = send(m, mouseMotion(x+8, y))
	m = send(m, mouseRelease(x+8, y))
	if got := m.session.result.colWidths[0]; got != auto+8 {
		t.Fatalf("width = %d, want %d", got, auto+8)
	}

	// Two clicks on the new boundary fit the column to its data again.
	x += 8
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if got := m.session.result.colWidths[0]; got != auto {
		t.Fatalf("width after double click = %d, want automatic %d", got, auto)
	}
	if _, ok := m.session.result.colPrefs["id"]; ok {
		t.Fatal("double click must forget the manual width")
	}
}

func TestResultColumnWidthSurvivesRerun(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	before := m.session.result.colWidths[0]
	x, y := resultColumnEdge(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseMotion(x+5, y))
	m = send(m, mouseRelease(x+5, y))

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if got := m.session.result.colWidths[0]; got != before+5 {
		t.Fatalf("width after rerun = %d, want %d", got, before+5)
	}
}

func TestResultColumnBoundaryHoverShowsResizeHint(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	x, y := resultColumnEdge(m, 0)
	m = send(m, mouseMotion(x, y))
	if m.colHover != 0 {
		t.Fatalf("hovered column = %d, want 0", m.colHover)
	}
	if !strings.Contains(m.status.message, "Drag to resize") {
		// The hint lives in the status bar's left side, not the timed message.
		if !strings.Contains(stripANSI(m.renderStatus()), "Drag to resize") {
			t.Fatalf("status bar is missing the resize hint: %q", stripANSI(m.renderStatus()))
		}
	}
}

func TestObjectScrollbarCanBeDragged(t *testing.T) {
	var tables []*catalog.Table
	for i := 0; i < 40; i++ {
		tables = append(tables, &catalog.Table{
			Schema: "big", Name: fmt.Sprintf("t%02d", i), Type: catalog.TypeTable,
		})
	}
	m := newTestModel("")
	m.cat = &catalog.Catalog{Schemas: []*catalog.Schema{{Name: "big", Tables: tables}}}
	m.resetObjects()
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "big"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "big", group: catalog.KindTable}, true)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 10})

	sb, ok := m.objectsScrollMetrics()
	if !ok || sb.thumbHeight == 0 {
		t.Fatalf("long tree must expose a scrollbar: %+v ok=%v", sb, ok)
	}
	// Grab the thumb and drag it to the bottom of the track.
	thumbY := sb.y + sb.thumbTop + sb.thumbHeight/2
	m = send(m, mousePress(sb.x, thumbY))
	m = send(m, mouseMotion(sb.x, sb.y+sb.height-1))
	m = send(m, mouseRelease(sb.x, sb.y+sb.height-1))
	want := len(m.objectsRows()) - m.objectsBodyHeight()
	if m.objects.scroll != want {
		t.Fatalf("scroll = %d, want %d after dragging to the bottom", m.objects.scroll, want)
	}
}

func TestResultScrollbarCanBeDragged(t *testing.T) {
	conn := e2eConn(t)
	for i := 4; i < 30; i++ {
		if _, err := conn.Query(context.Background(),
			fmt.Sprintf("INSERT INTO users VALUES (%d, 'u%d@example.com', 'U%d', %d)", i, i, i, 20+i)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	sb, ok := m.resultsScrollMetrics()
	if !ok || sb.thumbHeight == 0 {
		t.Fatalf("long result must expose a scrollbar: %+v ok=%v", sb, ok)
	}
	if got, want := m.resultsScrollEdge().Y, m.resultDataTopY(); got != want {
		t.Fatalf("scrollbar starts at row %d, want data row %d", got, want)
	}
	// Grab the thumb and drag it to the last page.
	thumbY := sb.y + sb.thumbTop + sb.thumbHeight/2
	m = send(m, mousePress(sb.x, thumbY))
	m = send(m, mouseMotion(sb.x, sb.y+sb.height-1))
	m = send(m, mouseRelease(sb.x, sb.y+sb.height-1))
	want := sb.total - sb.visible
	if m.session.result.scrollY != want {
		t.Fatalf("scrollY = %d, want %d after dragging to the bottom", m.session.result.scrollY, want)
	}
}

// ---- Multi-row selection (round 7 §3) ----

// resultRowsModel runs a three-row query with the grid focused, which every
// selection test starts from: (1, Ada), (2, NULL), (3, Lin) by id.
func resultRowsModel(t *testing.T) Model {
	t.Helper()
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	m.layout.focus = paneResults
	return m
}

func TestResultGridShiftSelectionExtendsAndShrinks(t *testing.T) {
	m := resultRowsModel(t)
	step := func(k tea.KeyType) Model { return runToCompletion(m, tea.KeyMsg{Type: k}) }

	// The first shift press anchors at the caret and reaches one row further.
	m = step(tea.KeyShiftDown)
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("after one shift+down: anchor=%d cursor=%d selected=%d, want 0/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}
	// Two presses cover three rows, around the same anchor.
	m = step(tea.KeyShiftDown)
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 2 || got.selectedRowCount() != 3 {
		t.Fatalf("after two shift+downs: anchor=%d cursor=%d selected=%d, want anchor 0, cursor 2, 3 rows",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}
	// Shift+up shrinks the span back towards the anchor.
	m = step(tea.KeyShiftUp)
	if got := m.session.result; got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("after shift+up: cursor=%d selected=%d, want 1/2", got.cursorRow, got.selectedRowCount())
	}
	// Shift+End and Shift+Home walk to the edges of the view.
	m = step(tea.KeyShiftEnd)
	if got := m.session.result; got.cursorRow != 2 || got.selectedRowCount() != 3 {
		t.Fatalf("after shift+end: cursor=%d selected=%d, want 2/3", got.cursorRow, got.selectedRowCount())
	}
	m = step(tea.KeyShiftHome)
	if got := m.session.result; got.cursorRow != 0 || got.selectedRowCount() != 1 {
		t.Fatalf("after shift+home: cursor=%d selected=%d, want 0/1", got.cursorRow, got.selectedRowCount())
	}

	// The first press from a plain caret anchors at *that* caret: End puts the
	// caret on the last row, so shift+up selects rows 1 and 2.
	m = step(tea.KeyEnd)
	if m.session.result.selAnchor != -1 {
		t.Fatalf("a plain move must not select, selAnchor = %d", m.session.result.selAnchor)
	}
	m = step(tea.KeyShiftUp)
	if got := m.session.result; got.selAnchor != 2 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("shift+up from row 2: anchor=%d cursor=%d selected=%d, want 2/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}
	// A plain move collapses the selection, the way it does in the editor.
	m = step(tea.KeyDown)
	if m.session.result.selAnchor != -1 {
		t.Fatalf("a plain move must end the selection, selAnchor = %d", m.session.result.selAnchor)
	}
}

func TestResultGridSelectionDropsWhenTheViewChanges(t *testing.T) {
	m := resultRowsModel(t)
	establish := func(m Model) Model {
		m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
		if m.session.result.selAnchor < 0 {
			t.Fatal("the test must start from a selection")
		}
		return m
	}

	// Sorting the caret's column rebuilds the view.
	m = establish(m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.session.result.selAnchor != -1 {
		t.Fatalf("sorting must drop the selection, selAnchor = %d", m.session.result.selAnchor)
	}

	// Typing into the filter input rebuilds it too.
	m = establish(m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(m, "ada")
	if m.session.result.selAnchor != -1 {
		t.Fatalf("filtering must drop the selection, selAnchor = %d", m.session.result.selAnchor)
	}
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})

	// The context menu's sort and filter go through the same synchronisation.
	m = establish(m)
	m.setResultSort(1, true)
	if m.session.result.selAnchor != -1 {
		t.Fatalf("a menu sort must drop the selection, selAnchor = %d", m.session.result.selAnchor)
	}
	m = establish(m)
	m.filterResultCell(0, 1) // filter by the name of the first row
	if m.session.result.selAnchor != -1 {
		t.Fatalf("filtering by a cell must drop the selection, selAnchor = %d", m.session.result.selAnchor)
	}
}

// TestEveryNewSessionStartsWithoutARowSelection: selAnchor's zero value reads
// as "row 0 through the caret", which is a selection — so every path that
// builds a session has to start it at -1, or the first Esc in the grid clears
// a selection nobody made instead of leaving the pane.
func TestEveryNewSessionStartsWithoutARowSelection(t *testing.T) {
	check := func(what string, m Model) {
		t.Helper()
		if got := m.session.result.selAnchor; got != -1 {
			t.Fatalf("%s: selAnchor = %d, want -1 (no selection)", what, got)
		}
	}

	m := newTestModel("SELECT 1")
	check("the startup session", m)

	m.newTab()
	check("newTab", m)

	m.newTabNamed("query.sql", "SELECT 1")
	check("newTabNamed", m)

	m.duplicateTabAt(m.active)
	check("duplicateTabAt", m)

	// Closing the last tab falls back to a fresh welcome page.
	for len(m.tabs) > 1 {
		m.closeTab()
	}
	m.closeTab()
	if !m.welcome {
		t.Fatal("precondition: closing the last tab must land on the welcome page")
	}
	check("the welcome page behind the last closed tab", m)
}

// TestEveryNewSessionStartsWithoutAHover: hover's zero value reads as "row 0
// is under the pointer", which lights the first row up before a pointer has
// ever been on the grid — the same defect selAnchor has with "row 0 is
// selected". Every path that builds a session starts the state at -1 through
// newResultState, and rendering a frame never invents a hover either.
func TestEveryNewSessionStartsWithoutAHover(t *testing.T) {
	if got := newResultState(); got.hover != -1 || got.selAnchor != -1 {
		t.Fatalf("newResultState = hover %d selAnchor %d, want -1/-1", got.hover, got.selAnchor)
	}
	check := func(what string, m Model) {
		t.Helper()
		if got := m.session.result.hover; got != -1 {
			t.Fatalf("%s: hover = %d, want -1 (no row under the pointer)", what, got)
		}
	}

	m := newTestModel("SELECT 1")
	check("the startup session", m)

	m.newTab()
	check("newTab", m)

	m.newTabNamed("query.sql", "SELECT 1")
	check("newTabNamed", m)

	m.duplicateTabAt(m.active)
	check("duplicateTabAt", m)

	// Closing the last tab falls back to a fresh welcome page.
	for len(m.tabs) > 1 {
		m.closeTab()
	}
	m.closeTab()
	if !m.welcome {
		t.Fatal("precondition: closing the last tab must land on the welcome page")
	}
	check("the welcome page behind the last closed tab", m)

	// A frame changes nothing: the grid of a fresh session has no row to
	// highlight when the view is drawn.
	_ = m.View()
	check("after rendering a frame", m)
}

// TestEscLeavesTheGridInOnePressWhenNothingIsSelected: with no selection the
// grid is left with the first Esc — on a tab that never ran anything as well
// as on a result nobody selected (round 7 §3).
func TestEscLeavesTheGridInOnePressWhenNothingIsSelected(t *testing.T) {
	// A tab that has never shown a result.
	m := newTestModel("SELECT 1")
	m.newTab()
	m.layout.focus = paneResults
	m = keyByName(m, "esc")
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, one Esc must leave the grid on a fresh tab", m.layout.focus)
	}

	// A query result with no selection on it.
	m = resultRowsModel(t)
	if m.session.result.selAnchor != -1 {
		t.Fatalf("selAnchor = %d after the query, want -1 (no selection)", m.session.result.selAnchor)
	}
	m = keyByName(m, "esc")
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, one Esc must leave the grid without a selection", m.layout.focus)
	}
}

func TestResultGridEscLeavesTheSelectionFirst(t *testing.T) {
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if m.session.result.selAnchor < 0 {
		t.Fatal("the test must start from a selection")
	}
	m = keyByName(m, "esc")
	if m.session.result.selAnchor != -1 {
		t.Fatalf("the first Esc must drop the selection, selAnchor = %d", m.session.result.selAnchor)
	}
	if m.layout.focus != paneResults {
		t.Fatalf("focus = %v, the first Esc must stay in the grid", m.layout.focus)
	}
	m = keyByName(m, "esc")
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, the second Esc must leave the grid", m.layout.focus)
	}
}

func TestCopyResultSelectionRowsAsTSV(t *testing.T) {
	buf := captureClipboard(t)
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown}) // rows 1..3 selected

	// Ctrl+C takes the whole selection, in view order, without a header.
	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("copying a selection must return a command")
	}
	if got := statusMsgText(cmd()); got != "Copied 3 rows" {
		t.Fatalf("status message = %q, want %q", got, "Copied 3 rows")
	}
	want := "1\tAda\n2\tNULL\n3\tLin"
	if got := decodeClipboard(t, buf.String()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}

	// `y` copies the same rows while a selection exists.
	buf.Reset()
	m, cmd = sendCmd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y with a selection must return a command")
	}
	if got := statusMsgText(cmd()); got != "Copied 3 rows" {
		t.Fatalf("status message = %q, want %q", got, "Copied 3 rows")
	}
	if got := decodeClipboard(t, buf.String()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}

func TestCopyResultCellWithoutASelection(t *testing.T) {
	buf := captureClipboard(t)
	m := resultRowsModel(t)
	// The caret on the second row, second column: the NULL name.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDown})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.session.result.hasRowSelection() {
		t.Fatal("a plain move must leave no selection")
	}

	// `y` still copies the single cell under the caret, NULL spelled out.
	_, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y must return a copy command")
	}
	msg := cmd()
	if !strings.Contains(statusMsgText(msg), "Copied") {
		t.Fatalf("status message = %q, want a copy confirmation", statusMsgText(msg))
	}
	if got := decodeClipboard(t, buf.String()); got != "NULL" {
		t.Fatalf("clipboard = %q, want NULL", got)
	}

	// Ctrl+C still copies the whole row under the caret.
	buf.Reset()
	_, cmd = sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must return a copy command")
	}
	cmd()
	if got := decodeClipboard(t, buf.String()); got != "2\tNULL" {
		t.Fatalf("clipboard = %q, want the caret row as TSV", got)
	}
}

// failWriter is a clipboard that cannot be written to; the copy commands must
// report the failure instead of claiming success.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("no terminal") }

func TestResultSelectionCopyFailureReportsError(t *testing.T) {
	old := clipboardWriter
	clipboardWriter = failWriter{}
	t.Cleanup(func() { clipboardWriter = old })

	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	cmd := m.copyResultSelection()
	if cmd == nil {
		t.Fatal("copying a selection must return a command")
	}
	if got := statusMsgText(cmd()); !strings.Contains(got, "Copy failed") {
		t.Fatalf("status message = %q, want the copy failure", got)
	}
}

func TestResultSummaryShowsSelectionAndFitsANarrowPane(t *testing.T) {
	// A three-column query so the filter below keeps all three rows: emails
	// all contain "e", and the selection stays a two-row one.
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email, name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	m.layout.focus = paneResults

	// The summary says how many rows are selected.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if got := m.resultSummary(m.session.result); !strings.Contains(got, "2 rows selected") {
		t.Fatalf("summary = %q, want it to contain %q", got, "2 rows selected")
	}

	// A filter plus a selection makes the line long enough to overflow a
	// 40-column pane; typing the filter dropped the old selection, so it is
	// established again on the filtered view.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = typeText(m, "e")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	summary := m.resultSummary(m.session.result)
	if !strings.Contains(summary, "2 rows selected") || !strings.Contains(summary, "filtered") {
		t.Fatalf("summary = %q, want the filter and the selection count", summary)
	}
	if w := lipgloss.Width(" " + summary); w <= 40 {
		t.Fatalf("summary width = %d, want more than 40 so the clipping below is exercised (%q)", w, summary)
	}

	lines := m.renderResultBody(40, 10)
	if len(lines) == 0 {
		t.Fatal("the body must render")
	}
	if w := lipgloss.Width(lines[0]); w > 40 {
		t.Fatalf("summary line is %d columns wide, want at most 40: %q", w, lines[0])
	}

	// And on a 40-column terminal nothing overflows anywhere: not one line of
	// the whole view, and the summary at its own pane's width.
	m = send(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	for i, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("view line %d is %d columns wide: %q", i, w, line)
		}
	}
	rect := m.paneRects().results
	paneLines := m.renderResultBody(rect.W, rect.H-1)
	if len(paneLines) == 0 {
		t.Fatal("the body must render in the narrow pane")
	}
	if w := lipgloss.Width(paneLines[0]); w > rect.W {
		t.Fatalf("summary line is %d columns wide, want at most %d: %q", w, rect.W, paneLines[0])
	}
}

// ---- Ctrl+C outside the grid ----

// TestCtrlCOutsideTheGridCopiesTheRowSelectionInsteadOfQuitting pins the
// exit condition: a grid that still reports rows selected owns Ctrl+C even
// after the focus moved out of it (here with Tab). Quitting in that state
// would roll back the transaction, close the connection and drop the
// selection the summary line is still showing — the key must copy instead.
func TestCtrlCOutsideTheGridCopiesTheRowSelectionInsteadOfQuitting(t *testing.T) {
	buf := captureClipboard(t)
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown}) // rows 1..3 selected
	if !m.session.result.hasRowSelection() {
		t.Fatal("the test must start from a row selection")
	}

	// Tab hands the keyboard to the editor; the selection stays on screen.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
	if !m.session.result.hasRowSelection() {
		t.Fatal("leaving the grid must not drop the selection")
	}

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Fatal("Ctrl+C must not quit while the grid has rows selected")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C must return the copy command instead of quitting")
	}
	if got := statusMsgText(cmd()); got != "Copied 3 rows" {
		t.Fatalf("status message = %q, want %q", got, "Copied 3 rows")
	}
	want := "1\tAda\n2\tNULL\n3\tLin"
	if got := decodeClipboard(t, buf.String()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}

// TestCtrlCQuitsFromTheEditorWhenTheGridHasNoSelection is the regression
// guard for the other half of the condition: with a result on screen but
// nothing selected anywhere, Ctrl+C in the editor quits exactly as it did
// before the copy branch existed.
func TestCtrlCQuitsFromTheEditorWhenTheGridHasNoSelection(t *testing.T) {
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyTab}) // focus the editor
	if m.session.result.res == nil {
		t.Fatal("precondition: the grid must hold a result")
	}
	if m.session.result.hasRowSelection() {
		t.Fatal("precondition: nothing may be selected")
	}

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("Ctrl+C must quit when there is nothing to copy")
	}
	if cmd == nil {
		t.Fatal("the quit path must return a command")
	}
	outcome := cmd()
	if _, ok := outcome.(connCloseMsg); ok {
		updated, quitCmd := m.Update(outcome)
		m = updated.(Model)
		cmd = quitCmd
	}
	if cmd == nil {
		t.Fatal("the completed shutdown must produce a quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("the command must be tea.Quit")
	}
}

// ---- Defensive anchor clamp ----

// TestStartResultSelectionClampsTheAnchorToTheView: the anchor names a view
// position, so it has to be inside the view. A caret left past the end of a
// shrunken result would otherwise select rows that are not there, and an
// empty view starts no selection at all.
func TestStartResultSelectionClampsTheAnchorToTheView(t *testing.T) {
	one, two := "1", "2"
	res := &driver.Result{
		Columns: []driver.ResultColumn{{Name: "id", Type: "INTEGER"}},
		Rows:    [][]*string{{&one}, {&two}},
	}
	r := resultState{res: res, cursorRow: 99, selAnchor: -1}
	r.startResultSelection()
	if r.selAnchor != 1 {
		t.Fatalf("anchor = %d, want it clamped to the last view row (1)", r.selAnchor)
	}

	empty := resultState{cursorRow: 3, selAnchor: -1} // no result at all
	empty.startResultSelection()
	if empty.selAnchor != -1 {
		t.Fatalf("anchor = %d, an empty view must start no selection", empty.selAnchor)
	}
}

// ---- Selection state across executions and result sets (review gaps) ----

// TestSetResultDropsTheRowSelection: a re-run replaces the rows, so the
// anchor of the old view must be gone with them.
func TestSetResultDropsTheRowSelection(t *testing.T) {
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if m.session.result.selAnchor < 0 {
		t.Fatal("the test must start from a selection")
	}

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5}) // execute again
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	if m.session.result.selAnchor != -1 {
		t.Fatalf("selAnchor = %d after setResult, want -1 (a new result is a new view)",
			m.session.result.selAnchor)
	}
}

// TestEmptyResultHandlesShiftKeysAndCopyingSafely: a result set with no rows
// has nothing to anchor a selection on. The shift keys must be no-ops (no
// panic), and both the selection copy and Ctrl+C must stay silent instead of
// writing an empty clipboard payload.
func TestEmptyResultHandlesShiftKeysAndCopyingSafely(t *testing.T) {
	buf := captureClipboard(t)
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users WHERE id < 0")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if m.session.result.err != nil {
		t.Fatalf("query failed: %v", m.session.result.err)
	}
	if rows := len(m.session.result.viewRows()); rows != 0 {
		t.Fatalf("view rows = %d, want 0 for this test", rows)
	}
	m.layout.focus = paneResults

	for _, k := range []tea.KeyType{
		tea.KeyShiftDown, tea.KeyShiftUp, tea.KeyShiftHome, tea.KeyShiftEnd,
	} {
		m = runToCompletion(m, tea.KeyMsg{Type: k}) // must not panic and must not anchor
	}
	if m.session.result.hasRowSelection() {
		t.Fatalf("an empty view cannot have a selection, anchor = %d", m.session.result.selAnchor)
	}
	if cmd := m.copyResultSelection(); cmd != nil {
		t.Fatal("copying an empty selection must return no command")
	}
	_, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("Ctrl+C with nothing to copy must not return a command")
	}
	if buf.Len() != 0 {
		t.Fatalf("the clipboard must stay untouched, wrote %q", buf.String())
	}
}

// TestCopyResultSelectionMessageIsLocalized: the multi-row copy rewrites the
// clipboard confirmation ("Copied N characters" → "Copied N rows") by
// comparing the exact success line the locale produces, so the comparison
// has to match in Chinese too.
func TestCopyResultSelectionMessageIsLocalized(t *testing.T) {
	buf := captureClipboard(t)
	m := resultRowsModel(t)
	m.translator = i18n.New(i18n.ZhHans)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown}) // rows 1..3

	cmd := m.copyResultSelection()
	if cmd == nil {
		t.Fatal("copying a selection must return a command")
	}
	if got := statusMsgText(cmd()); got != "已复制 3 行" {
		t.Fatalf("status message = %q, want %q", got, "已复制 3 行")
	}
	want := "1\tAda\n2\tNULL\n3\tLin"
	if got := decodeClipboard(t, buf.String()); got != want {
		t.Fatalf("clipboard = %q, want %q", got, want)
	}
}

// TestResultSetSwitchKeepsSelectionsIndependent: each result set carries its
// own selection across a switch, and a set that never got one does not
// inherit it from the set it was switched away from.
func TestResultSetSwitchKeepsSelectionsIndependent(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users ORDER BY id;\nSELECT name FROM users ORDER BY id")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	if got := m.session.resultSetCount(); got != 2 {
		t.Fatalf("result sets = %d, want 2", got)
	}
	m.layout.focus = paneResults

	// The active set (the second one) gets a two-row selection.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 {
		t.Fatalf("second set selection: anchor=%d cursor=%d, want 0/1", got.selAnchor, got.cursorRow)
	}

	// The first set has no selection of its own.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if m.session.result.selAnchor != -1 {
		t.Fatalf("the first set inherited the anchor %d", m.session.result.selAnchor)
	}
	// Give it one: anchor at row 1 (a plain move first, so the shift press
	// anchors at the caret it starts from).
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyDown})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	firstAnchor, firstCursor := m.session.result.selAnchor, m.session.result.cursorRow
	if firstAnchor != 1 || firstCursor != 2 {
		t.Fatalf("first set selection: anchor=%d cursor=%d, want 1/2", firstAnchor, firstCursor)
	}

	// Back to the second set: its selection survived untouched.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("second set after two switches: anchor=%d cursor=%d rows=%d, want 0/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}

	// And back again: the first set kept its own.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	if got := m.session.result; got.selAnchor != firstAnchor || got.cursorRow != firstCursor {
		t.Fatalf("first set after two switches: anchor=%d cursor=%d, want %d/%d",
			got.selAnchor, got.cursorRow, firstAnchor, firstCursor)
	}
}
