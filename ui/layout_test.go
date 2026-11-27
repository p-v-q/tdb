package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/settings"
	"github.com/p-v-q/tdb/textbuf"
)

// defaultObjectsWidth and defaultResultsHeight are the factory pane sizes. They
// come from the settings defaults so the layout tests cannot drift from them.
var (
	defaultObjectsWidth  = settings.Default().Layout.ObjectsWidth
	defaultResultsHeight = settings.Default().Layout.ResultsHeight
)

// ---- Layout geometry (pure functions) ----

func TestLayoutDefaultRects(t *testing.T) {
	l := defaultLayout()
	r := l.compute(100, 20)

	// Menu bar row 0, toolbar row 1, panes 2..18, status bar row 19. The tab
	// strip belongs to the editor column and starts at the top of that column.
	if r.menubar != (Rect{X: 0, Y: 0, W: 100, H: menuBarHeight}) {
		t.Fatalf("menubar = %+v", r.menubar)
	}
	if r.toolbar != (Rect{X: 0, Y: 1, W: 100, H: toolbarHeight}) {
		t.Fatalf("toolbar = %+v", r.toolbar)
	}
	if r.area != (Rect{X: 0, Y: 2, W: 100, H: 17}) {
		t.Fatalf("area = %+v", r.area)
	}
	if r.objects != (Rect{X: 0, Y: 2, W: defaultObjectsWidth, H: 17}) {
		t.Fatalf("objects = %+v", r.objects)
	}
	if r.dividerV != (Rect{X: defaultObjectsWidth, Y: 2, W: 1, H: 17}) {
		t.Fatalf("dividerV = %+v", r.dividerV)
	}
	// The editor sits to the right of the divider, below its tab strip.
	if r.editor.X != defaultObjectsWidth+1 {
		t.Fatalf("editor.X = %d", r.editor.X)
	}
	if r.tabbar != (Rect{X: defaultObjectsWidth + 1, Y: 2, W: 100 - defaultObjectsWidth - 1, H: tabbarHeight}) {
		t.Fatalf("tabbar = %+v", r.tabbar)
	}
	// The results pane hugs the bottom and is defaultResultsHeight tall; the
	// editor takes the remaining height (tabs, title and divider are deducted).
	if r.results.H != defaultResultsHeight || r.results.Y+r.results.H != 19 {
		t.Fatalf("results = %+v", r.results)
	}
	if got, want := r.editor.Y, r.tabbar.Y+tabbarHeight; got != want {
		t.Fatalf("editor.Y = %d, want %d", got, want)
	}
	if got, want := r.editor.H, 17-defaultResultsHeight-1-tabbarHeight; got != want {
		t.Fatalf("editor.H = %d, want %d", got, want)
	}
}

func TestLayoutObjectsHidden(t *testing.T) {
	l := defaultLayout()
	l.objectsVisible = false
	r := l.compute(100, 20)

	// Once collapsed, a 1-column expand strip is left on the left edge and the
	// editor takes its place.
	if r.objects != (Rect{}) {
		t.Fatalf("objects should be the empty rectangle, got %+v", r.objects)
	}
	if r.objectsExpand != (Rect{X: 0, Y: 2, W: 1, H: 17}) {
		t.Fatalf("objectsExpand = %+v", r.objectsExpand)
	}
	if r.editor.X != 1 {
		t.Fatalf("editor.X = %d, want 1", r.editor.X)
	}
	if r.editor.W != 99 {
		t.Fatalf("editor.W = %d, want 99", r.editor.W)
	}
	if r.tabbar.X != 1 || r.tabbar.W != 99 {
		t.Fatalf("tabbar = %+v, want it above the editor", r.tabbar)
	}
}

func TestLayoutResultsHidden(t *testing.T) {
	l := defaultLayout()
	l.resultsVisible = false
	r := l.compute(100, 20)

	if r.results != (Rect{}) {
		t.Fatalf("results should be the empty rectangle, got %+v", r.results)
	}
	// A 1-row expand strip is left at the bottom edge and the editor takes the
	// full remaining height.
	if r.resultsExpand != (Rect{X: r.editor.X, Y: 18, W: r.editor.W, H: 1}) {
		t.Fatalf("resultsExpand = %+v", r.resultsExpand)
	}
	if r.editor.H != 15 {
		t.Fatalf("editor.H = %d, want 15", r.editor.H)
	}
}

func TestLayoutMinSizeClamp(t *testing.T) {
	l := defaultLayout()
	l.objectsWidth = 2     // below the minimum
	l.resultsHeight = 100  // larger than the available space
	r := l.compute(60, 12) // a small terminal
	if r.objects.W < minObjectsWidth {
		t.Fatalf("objects.W = %d, must be >= %d", r.objects.W, minObjectsWidth)
	}
	if r.editor.H < minEditorHeight {
		t.Fatalf("editor.H = %d, must be >= %d", r.editor.H, minEditorHeight)
	}
	if r.results.H > r.objects.H-minEditorHeight {
		t.Fatalf("results.H = %d is too large and squeezes the editor", r.results.H)
	}
	// The panes must span the full width.
	if r.editor.X+r.editor.W != 60 {
		t.Fatalf("editor right edge = %d, want 60", r.editor.X+r.editor.W)
	}
}

// ---- Keyboard pane toggles ----

func TestCtrlBTogglesObjectsPane(t *testing.T) {
	m := sized("")
	if !m.layout.objectsVisible {
		t.Fatal("the objects pane must be visible by default")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if m.layout.objectsVisible {
		t.Fatal("Ctrl+B must hide the objects pane")
	}
	if !strings.Contains(m.View(), m.icons().expandClosed) {
		t.Fatalf("an expand button must appear once collapsed:\n%s", m.View())
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if !m.layout.objectsVisible {
		t.Fatal("a second Ctrl+B must restore the objects pane")
	}
}

func TestCtrlETogglesResultsPane(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if m.layout.resultsVisible {
		t.Fatal("Ctrl+E must hide the results pane")
	}
	view := m.View()
	if !strings.Contains(view, "Results hidden") {
		t.Fatalf("a hint must appear at the bottom once collapsed:\n%s", view)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if !m.layout.resultsVisible {
		t.Fatal("a second Ctrl+E must restore the results pane")
	}
}

func TestCtrlWClosesActiveTab(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT}) // a second tab
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(m.tabs))
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m.tabs) != 1 {
		t.Fatalf("Ctrl+W must close the active tab, tabs = %d", len(m.tabs))
	}
	// Closing the last tab leaves a fresh empty one instead of exiting:
	// quitting is Ctrl+Q's job.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlW})
	if len(m.tabs) != 1 {
		t.Fatalf("closing the last tab must leave a fresh one, tabs = %d", len(m.tabs))
	}
	if m.buf.Text() != "" {
		t.Fatalf("the fresh tab must be empty, text = %q", m.buf.Text())
	}
	if m.quitting {
		t.Fatal("closing tabs must not quit the app")
	}
}

func TestCloseFocusedPaneAction(t *testing.T) {
	m := sized("")
	m.layout.focus = paneResults
	m.closeFocusedPane()
	if m.layout.resultsVisible {
		t.Fatal("closeFocusedPane must close the focused pane")
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus must return to the editor after closing, got %v", m.layout.focus)
	}
	// With the editor focused, nothing can be closed.
	m.closeFocusedPane()
	if !m.layout.objectsVisible {
		t.Fatal("the editor cannot be closed, so nothing else must go")
	}
}

// ---- Focus: revealing a pane is one-way ----

func TestFocusPaneRevealsHiddenPane(t *testing.T) {
	m := sized("")
	m.layout.objectsVisible = false
	m.layout.focus = paneEditor

	if revealed := m.focusPane(paneObjects); !revealed {
		t.Fatal("focusing a hidden objects pane must report a reveal")
	}
	if !m.layout.objectsVisible {
		t.Fatal("focusPane must show the objects pane first")
	}
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, want the objects pane", m.layout.focus)
	}
	// Pressing again on a visible pane only moves the focus: no reveal is
	// reported and the pane stays where it is.
	if revealed := m.focusPane(paneObjects); revealed {
		t.Fatal("focusing a visible pane must not report a reveal")
	}
	if !m.layout.objectsVisible {
		t.Fatal("focusing must never hide a pane")
	}

	// The results pane behaves the same way.
	m.layout.resultsVisible = false
	if revealed := m.focusPane(paneResults); !revealed {
		t.Fatal("focusing a hidden results pane must report a reveal")
	}
	if !m.layout.resultsVisible {
		t.Fatal("focusPane must show the results pane first")
	}
	if m.layout.focus != paneResults {
		t.Fatalf("focus = %v, want the results pane", m.layout.focus)
	}

	// The editor is always there: focusing it never touches visibility.
	m.focusPane(paneObjects)
	if revealed := m.focusPane(paneEditor); revealed {
		t.Fatal("the editor cannot be revealed, so no reveal may be reported")
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
	if !m.layout.objectsVisible || !m.layout.resultsVisible {
		t.Fatalf("focusing the editor must leave the panes alone: %+v", m.layout)
	}
}

// ---- Focus: the tree is ready to use the moment Alt+1 lands on it ----

func TestFocusObjectsSelectsFirstRow(t *testing.T) {
	m := sized("")
	// Fold the connection so the tree starts as a single, collapsed row and
	// the selection still has nowhere to be.
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, false)
	if m.objects.sel != -1 {
		t.Fatalf("the tree must start with no selection, sel = %d", m.objects.sel)
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, want the objects pane", m.layout.focus)
	}
	if m.objects.sel != 0 {
		t.Fatalf("sel = %d, want 0 — focusing must select a row", m.objects.sel)
	}
	if rows := m.objectsRows(); m.objects.sel >= len(rows) {
		t.Fatalf("sel = %d is out of range for %d rows", m.objects.sel, len(rows))
	}
	// …and the selection must be on screen, so the very next key acts on it.
	if h := m.objectsBodyHeight(); m.objects.sel < m.objects.scroll || m.objects.sel >= m.objects.scroll+h {
		t.Fatalf("sel = %d must be visible in scroll=%d..%d", m.objects.sel, m.objects.scroll, m.objects.scroll+h)
	}
	// Right unfolds the connection immediately: no Down press first.
	before := len(m.objectsRows())
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if after := len(m.objectsRows()); after <= before {
		t.Fatalf("Right must expand the connection, rows before=%d after=%d", before, after)
	}
}

// ---- Focus: a completion popup never outlives the pane that owned it ----

func TestAltFocusClosesCompletionPopup(t *testing.T) {
	// openPopup types enough to trigger completion and returns the model.
	openPopup := func(t *testing.T) Model {
		t.Helper()
		m := sized("")
		m = typeText(m, "SELECT * FROM us")
		if !m.popup.open {
			t.Fatal("completion must be open before the pane is switched")
		}
		return m
	}
	altDigit := func(m Model, digit rune) Model {
		return send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{digit}, Alt: true})
	}

	// The object tree owns the arrow keys now: the popup must not stay
	// behind, still covering the editor.
	m := openPopup(t)
	m = altDigit(m, '1')
	if m.popup.open {
		t.Fatal("Alt+1 must close the completion popup")
	}
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, want the objects pane", m.layout.focus)
	}

	// The results pane does the same.
	m = openPopup(t)
	m = altDigit(m, '3')
	if m.popup.open {
		t.Fatal("Alt+3 must close the completion popup")
	}
	if m.layout.focus != paneResults {
		t.Fatalf("focus = %v, want the results pane", m.layout.focus)
	}

	// The editor keeps it: focusing the editor changes nothing about it.
	m = openPopup(t)
	m = altDigit(m, '2')
	if !m.popup.open {
		t.Fatal("focusing the editor must leave the completion popup alone")
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("focus = %v, want the editor", m.layout.focus)
	}
}

// ---- Mouse: close button / expand button / divider drag ----

func TestCloseButtonAndExpandButton(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	// Click the ✕ at the right of the objects pane title bar.
	cb := closeBtnRect(r.objects)
	m = send(m, mousePress(cb.X, cb.Y))
	m = send(m, mouseRelease(cb.X, cb.Y))
	if m.layout.objectsVisible {
		t.Fatal("clicking ✕ must close the objects pane")
	}
	if m.layout.focus == paneObjects {
		t.Fatal("focus must not remain on the objects pane after closing it")
	}
	// Click the left-edge expand strip to restore it.
	r = m.paneRects()
	m = send(m, mousePress(r.objectsExpand.X, r.objectsExpand.Y+2))
	if !m.layout.objectsVisible {
		t.Fatal("clicking the expand strip must restore the objects pane")
	}
	// Same for the results pane.
	r = m.paneRects()
	cb = closeBtnRect(r.results)
	m = send(m, mousePress(cb.X, cb.Y))
	if m.layout.resultsVisible {
		t.Fatal("clicking ✕ must close the results pane")
	}
	r = m.paneRects()
	m = send(m, mousePress(r.resultsExpand.X+3, r.resultsExpand.Y))
	if !m.layout.resultsVisible {
		t.Fatal("clicking the bottom expand strip must restore the results pane")
	}
}

func TestDragVerticalDivider(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	// Hold the divider and drag 10 columns to the right.
	m = send(m, mousePress(r.dividerV.X, r.dividerV.Y+3))
	if m.layout.dragging != dividerVertical {
		t.Fatal("pressing the divider must start a drag")
	}
	m = send(m, mouseMotion(r.dividerV.X+10, r.dividerV.Y+3))
	if m.layout.objectsWidth != defaultObjectsWidth+10 {
		t.Fatalf("objectsWidth while dragging = %d, want %d", m.layout.objectsWidth, defaultObjectsWidth+10)
	}
	m = send(m, mouseRelease(r.dividerV.X+10, r.dividerV.Y+3))
	if m.layout.dragging != dividerNone {
		t.Fatal("releasing must end the drag")
	}
	// The editor moves right and shrinks accordingly.
	r2 := m.paneRects()
	if r2.editor.X != r.editor.X+10 {
		t.Fatalf("editor.X = %d, want %d", r2.editor.X, r.editor.X+10)
	}
	if r2.editor.W != r.editor.W-10 {
		t.Fatalf("editor.W = %d, want %d", r2.editor.W, r.editor.W-10)
	}
}

func TestDragVerticalDividerClamps(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	m = send(m, mousePress(r.dividerV.X, r.dividerV.Y+3))
	m = send(m, mouseMotion(1, r.dividerV.Y+3)) // drag left off-screen
	if m.layout.objectsWidth != minObjectsWidth {
		t.Fatalf("objectsWidth = %d, must clamp to the minimum %d", m.layout.objectsWidth, minObjectsWidth)
	}
	m = send(m, mouseMotion(m.width-1, r.dividerV.Y+3)) // drag right off-screen
	if m.layout.objectsWidth > m.width-minEditorWidth-dividerSize {
		t.Fatalf("objectsWidth = %d must not squeeze the editor out", m.layout.objectsWidth)
	}
	if r2 := m.paneRects(); r2.editor.W < minEditorWidth {
		t.Fatalf("editor.W = %d, must be >= %d", r2.editor.W, minEditorWidth)
	}
}

func TestDragHorizontalDivider(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	// The divider sits above the results pane; dragging it up makes the results
	// pane taller, up to the size that still leaves the editor its minimum.
	m = send(m, mousePress(r.dividerH.X+5, r.dividerH.Y))
	if m.layout.dragging != dividerHorizontal {
		t.Fatal("pressing the horizontal divider must start a drag")
	}
	m = send(m, mouseMotion(r.dividerH.X+5, r.dividerH.Y-4))
	want := defaultResultsHeight + 4
	if hi := r.area.H - minEditorHeight - dividerSize; want > hi {
		want = hi
	}
	if m.layout.resultsHeight != want {
		t.Fatalf("resultsHeight = %d, want %d", m.layout.resultsHeight, want)
	}
	m = send(m, mouseRelease(r.dividerH.X+5, r.dividerH.Y-4))
	r2 := m.paneRects()
	delta := m.layout.resultsHeight - defaultResultsHeight
	if r2.editor.H != r.editor.H-delta {
		t.Fatalf("editor.H = %d, want %d", r2.editor.H, r.editor.H-delta)
	}
}

// ---- Object browser ----

// objectRowIndex returns the tree row of a table, or -1.
func objectRowIndex(m Model, table string) int {
	for i, row := range m.objectsRows() {
		if row.kind == rowTable && row.table != nil && row.table.Name == table {
			return i
		}
	}
	return -1
}

// schemaRowIndex returns the tree row of a schema, or -1.
func schemaRowIndex(m Model, schema string) int {
	for i, row := range m.objectsRows() {
		if row.kind == rowSchema && row.schema == schema {
			return i
		}
	}
	return -1
}

func TestObjectsPaneClickAndInsert(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	// The tree starts with the connections; click the "users" table row.
	idx := objectRowIndex(m, "users")
	if idx < 0 {
		t.Fatalf("no users row in %+v", m.objectsRows())
	}
	y := r.objects.Y + 1 + idx
	m = send(m, mousePress(r.objects.X+2, y))
	if m.layout.focus != paneObjects {
		t.Fatalf("clicking the objects pane must focus it, got %v", m.layout.focus)
	}
	if m.objects.sel != idx {
		t.Fatalf("sel = %d, want %d", m.objects.sel, idx)
	}
	// Enter inserts the table name and jumps back to the editor.
	m = send(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := m.buf.Text(); got != "users" {
		t.Fatalf("inserted text = %q, want users", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatal("focus must return to the editor after inserting")
	}
}

func TestObjectsPaneKeyboardNavigation(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = 0
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.objects.sel != 2 {
		t.Fatalf("sel = %d, want 2", m.objects.sel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.objects.sel != 1 {
		t.Fatalf("sel = %d, want 1", m.objects.sel)
	}
	// home/end
	m = send(m, tea.KeyMsg{Type: tea.KeyEnd})
	rows := m.objectsRows()
	if m.objects.sel != len(rows)-1 {
		t.Fatalf("End must jump to the last row, sel = %d", m.objects.sel)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.objects.sel != 0 {
		t.Fatalf("Home must jump to the first row, sel = %d", m.objects.sel)
	}
	// esc hands the focus back
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.layout.focus != paneEditor {
		t.Fatal("Esc must hand the focus back to the editor")
	}
}

func TestObjectsPaneDoubleClickOpensViewData(t *testing.T) {
	// A double click on a table name is "View data": the bounded SELECT opens
	// in a tab of its own and runs (see objects_double_click_test.go for the
	// deeper cases).
	m := sized("")
	r := m.paneRects()
	x, y := r.objects.X+2, r.objects.Y+1+objectRowIndex(m, "users")
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, a table double click must open the data tab", len(m.tabs))
	}
	if got := m.buf.Text(); !strings.Contains(got, `SELECT * FROM "public"."users" LIMIT 200;`) {
		t.Fatalf("view data text = %q", got)
	}
	if m.layout.focus != paneEditor {
		t.Fatal("the data tab must take the focus")
	}
}

func TestObjectsPaneSchemaToggleLeftRight(t *testing.T) {
	m := sized("")
	m.layout.focus = paneObjects
	m.objects.sel = schemaRowIndex(m, "public")
	before := len(m.objectsRows())
	publicTables := len(m.cat.FindSchema("public").Tables)
	m = send(m, tea.KeyMsg{Type: tea.KeyLeft}) // collapse public
	after := len(m.objectsRows())
	if after >= before {
		t.Fatalf("collapsing must drop rows, before=%d after=%d", before, after)
	}
	// Nothing of that schema is left in the tree.
	for _, row := range m.objectsRows() {
		if row.schema == "public" && row.kind != rowConn && row.kind != rowSchema {
			t.Fatalf("the collapsed tree still contains %+v", row)
		}
	}
	if publicTables == 0 {
		t.Fatal("the demo catalog must have tables in public")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight}) // expand again
	if got := len(m.objectsRows()); got != before {
		t.Fatalf("expanding must restore %d rows, got %d", before, got)
	}
}

func TestObjectsWheelScrollsList(t *testing.T) {
	// Build a catalog with more rows than fit in the pane.
	var tables []*catalog.Table
	for i := 0; i < 40; i++ {
		tables = append(tables, &catalog.Table{Schema: "big", Name: fmt.Sprintf("t%02d", i), Type: catalog.TypeTable})
	}
	m := newTestModel("")
	m.cat = &catalog.Catalog{Schemas: []*catalog.Schema{{Name: "big", Tables: tables}}}
	m.resetObjects()
	m.objectsSetOpen(objectsRow{kind: rowConn, conn: 0}, true)
	m.objectsSetOpen(objectsRow{kind: rowSchema, schema: "big"}, true)
	m.objectsSetOpen(objectsRow{kind: rowGroup, schema: "big", group: catalog.KindTable}, true)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 10})

	r := m.paneRects()
	visible := m.objectsBodyHeight()
	if visible >= len(m.objectsRows()) {
		t.Fatalf("precondition failed: the pane should not fit %d rows", len(m.objectsRows()))
	}
	m = send(m, mouseWheel(true, r.objects.X+2, r.objects.Y+2)) // wheel down
	if m.objects.scroll == 0 {
		t.Fatal("scroll must increase after wheel-down on a long list")
	}
	// Scrolling back to the top must not overshoot.
	for i := 0; i < 20; i++ {
		m = send(m, mouseWheel(false, r.objects.X+2, r.objects.Y+2))
	}
	if m.objects.scroll != 0 {
		t.Fatalf("scroll = %d, must clamp to 0", m.objects.scroll)
	}
}

// ---- The editor keeps working after layout changes ----

func TestEditorWorksAfterLayoutChanges(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlB}) // hide the objects pane
	m = typeText(m, "SELECT * FROM us")
	if !m.popup.open {
		t.Fatal("completion must still work after a layout change")
	}
	m = keyByName(m, "tab")
	if got := m.buf.Text(); got != "SELECT * FROM users" {
		t.Fatalf("text = %q", got)
	}
	// Clicking the editor must still place the caret accurately.
	x, y := editorX(m, 3), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if c := m.buf.Cursor(); c.Col != 3 {
		t.Fatalf("cursor col = %d, want 3", c.Col)
	}
}

func TestViewRendersAllPanes(t *testing.T) {
	m := sized("SELECT 1")
	view := m.View()
	for _, want := range []string{"Objects", "SQL Editor", "Results", "Ln 1, Col 1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the view is missing %q:\n%s", want, view)
		}
	}
	// Every line has the same width (the canvas is aligned).
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if w := lipgloss.Width(l); w != m.width {
			t.Fatalf("line %d width = %d, want %d", i, w, m.width)
		}
	}
}

// ---- Regression: the panes do not disturb the text buffer ----

func TestLayoutDoesNotAffectBufferOps(t *testing.T) {
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	m = typeText(m, "1=1")
	if got := m.buf.Text(); got != "SELECT 1=1 users" {
		t.Fatalf("text = %q", got)
	}
}

// TestFocusPaneEndsObjectsFilterInput guards the filter input against staying
// armed when a shortcut moves the focus away: the query must stay, the input
// must close, and returning to the tree must not resume typing.
func TestFocusPaneEndsObjectsFilterInput(t *testing.T) {
	m := sized("")
	m = objectFilterStart(t, m)
	m = typeText(m, "users")

	m.focusPane(paneEditor)
	if m.objects.filtering {
		t.Fatal("leaving the objects pane must end the filter input")
	}
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, leaving must keep the query", m.objects.filter)
	}

	// Alt+1 focuses the tree again; an ordinary key must reach the editor
	// instead of being appended to the stored query.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	if m.layout.focus != paneObjects {
		t.Fatalf("focus = %v, Alt+1 must focus the objects pane", m.layout.focus)
	}
	m = typeText(m, "x")
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, a plain key must not append to a closed input", m.objects.filter)
	}
	if !strings.Contains(m.buf.Text(), "x") {
		t.Fatalf("text = %q, the key must reach the editor", m.buf.Text())
	}

	// Esc still clears a query typed in a reopened input.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeText(m, "users")
	if m.objects.filter != "users" {
		t.Fatalf("filter = %q, want the reopened input to collect the query", m.objects.filter)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.objects.filtering || m.objects.filter != "" {
		t.Fatalf("filter = %q, filtering = %v, Esc must clear both",
			m.objects.filter, m.objects.filtering)
	}
}
