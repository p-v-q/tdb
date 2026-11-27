package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/p-v-q/tdb/textbuf"
)

// sendCmd is like send but keeps the returned tea.Cmd.
func sendCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func mousePress(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// mouseShiftPress is a left press with the shift modifier held: the gesture
// that extends the result grid's row selection (round 7 §3).
func mouseShiftPress(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Shift: true}
}

// mouseHover is a motion with no button held: the pointer merely moved.
func mouseHover(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion}
}

func mouseMotion(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}
}

func mouseRelease(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
}

func mouseWheel(down bool, x, y int) tea.MouseMsg {
	btn := tea.MouseButtonWheelDown
	if !down {
		btn = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: btn}
}

func sized(text string) Model {
	m := newTestModel(text)
	return send(m, tea.WindowSizeMsg{Width: 100, Height: 20})
}

// ---- Pane coordinate helpers ----
// The screen coordinates of the editor text area change with the layout, so the
// tests translate through these functions.

// editorGutterX returns the screen column of the editor text area's left edge
// (the start of the line-number gutter).
func editorGutterX(m Model) int {
	return m.paneRects().editor.X + m.gutterWidth()
}

// editorRowY returns the screen row of row `row` (0-based) in the editor text
// area.
func editorRowY(m Model, row int) int {
	return m.paneRects().editor.Y + 1 + row
}

// editorX converts a "display column in the text area" to a screen column.
func editorX(m Model, displayCol int) int {
	return editorGutterX(m) + displayCol
}

// editorBottomY returns the screen row of the last line in the editor text
// area.
func editorBottomY(m Model) int {
	r := m.paneRects().editor
	return r.Y + r.H - 1
}

func TestClickMovesCursor(t *testing.T) {
	m := sized("SELECT * FROM users")
	x, y := editorX(m, 5), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	c := m.buf.Cursor()
	if c.Row != 0 || c.Col != 5 {
		t.Fatalf("cursor = %+v, want {0 5}", c)
	}
	if m.hasSelection() {
		t.Fatal("a single click must not leave a selection")
	}
	if m.layout.focus != paneEditor {
		t.Fatalf("clicking the editor must focus it, got %v", m.layout.focus)
	}
}

func TestClickClampsToLineEnd(t *testing.T) {
	m := sized("abc")
	x, y := editorX(m, 50), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if c := m.buf.Cursor(); c.Col != 3 {
		t.Fatalf("cursor = %+v, want col 3 (end of line)", c)
	}
}

func TestClickOnWideCharacters(t *testing.T) {
	// "SELECT '中文' x": each CJK character occupies 2 display columns.
	m := sized("SELECT '中文' x")
	// Display column 12 is exactly the quote after the CJK characters.
	m = send(m, mousePress(editorX(m, 12), editorRowY(m, 0)))
	m = send(m, mouseRelease(editorX(m, 12), editorRowY(m, 0)))
	if c := m.buf.Cursor(); c.Col != 10 {
		t.Fatalf("cursor col = %d, want 10 (CJK wide-character alignment)", c.Col)
	}
	// Display column 9 falls on the right half of the first CJK character, so
	// the caret snaps to after it (rune index 9).
	m = send(m, mousePress(editorX(m, 9), editorRowY(m, 0)))
	m = send(m, mouseRelease(editorX(m, 9), editorRowY(m, 0)))
	if c := m.buf.Cursor(); c.Col != 9 {
		t.Fatalf("cursor col = %d, want 9", c.Col)
	}
	// Display column 8 is the left half of the first CJK character, so the
	// caret goes before it (rune index 8).
	m = send(m, mousePress(editorX(m, 8), editorRowY(m, 0)))
	m = send(m, mouseRelease(editorX(m, 8), editorRowY(m, 0)))
	if c := m.buf.Cursor(); c.Col != 8 {
		t.Fatalf("cursor col = %d, want 8", c.Col)
	}
}

func TestClickRespectsHorizontalScroll(t *testing.T) {
	m := sized(strings.Repeat("x", 200))
	m.scrollX = 20
	x, y := editorX(m, 4), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if c := m.buf.Cursor(); c.Col != 24 {
		t.Fatalf("cursor col = %d, want 24 (scrollX=20 + display column 4)", c.Col)
	}
}

func TestClickSecondRowUsesFirstRow(t *testing.T) {
	m := sized("one\ntwo\nthree")
	// The second row of the text area (buffer row 1).
	x, y := editorX(m, 2), editorRowY(m, 1)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	if c := m.buf.Cursor(); c.Row != 1 || c.Col != 2 {
		t.Fatalf("cursor = %+v, want {1 2}", c)
	}
}

func TestDragSelectsText(t *testing.T) {
	m := sized("SELECT * FROM users")
	y := editorRowY(m, 0)
	m = send(m, mousePress(editorX(m, 7), y))
	m = send(m, mouseMotion(editorX(m, 13), y))
	start, end, ok := m.selectionRange()
	if !ok {
		t.Fatal("dragging must produce a selection")
	}
	if start != (textbuf.Pos{Row: 0, Col: 7}) || end != (textbuf.Pos{Row: 0, Col: 13}) {
		t.Fatalf("selection = [%+v, %+v)", start, end)
	}
	if got := m.selectedText(); got != "* FROM" {
		t.Fatalf("selected text = %q", got)
	}
	m = send(m, mouseRelease(editorX(m, 13), y))
	// The selection survives the release.
	if !m.hasSelection() {
		t.Fatal("the selection must survive the mouse release")
	}
}

func TestDragBackwardsSelectionNormalizes(t *testing.T) {
	m := sized("SELECT * FROM users")
	y := editorRowY(m, 0)
	m = send(m, mousePress(editorX(m, 13), y))
	m = send(m, mouseMotion(editorX(m, 7), y))
	m = send(m, mouseRelease(editorX(m, 7), y))
	start, end, ok := m.selectionRange()
	if !ok || start.Col != 7 || end.Col != 13 {
		t.Fatalf("backwards drag selection = [%+v, %+v] ok=%v", start, end, ok)
	}
}

func TestDoubleClickSelectsWord(t *testing.T) {
	m := sized("SELECT * FROM users WHERE id = 1")
	// The "R" in "FROM": display column 9.
	x, y := editorX(m, 9), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mouseRelease(x, y))
	m = send(m, mousePress(x, y))
	if got := m.selectedText(); got != "FROM" {
		t.Fatalf("double-click selection = %q, want FROM", got)
	}
}

func TestTripleClickSelectsLine(t *testing.T) {
	m := sized("SELECT * FROM users")
	x, y := editorX(m, 3), editorRowY(m, 0)
	m = send(m, mousePress(x, y))
	m = send(m, mousePress(x, y))
	m = send(m, mousePress(x, y))
	if got := m.selectedText(); got != "SELECT * FROM users" {
		t.Fatalf("triple-click selection = %q, want the whole line", got)
	}
}

func TestWheelScrollsWithoutMovingCursor(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "line")
	}
	m := sized(strings.Join(lines, "\n"))
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 0})
	x, y := editorX(m, 2), editorRowY(m, 2)
	m = send(m, mouseWheel(true, x, y))
	if m.firstRow != 3 {
		t.Fatalf("firstRow = %d, want 3", m.firstRow)
	}
	m = send(m, mouseWheel(false, x, y))
	m = send(m, mouseWheel(false, x, y))
	if m.firstRow != 0 {
		t.Fatalf("firstRow = %d, want 0 (scrolling to the top must not overshoot)", m.firstRow)
	}
	if c := m.buf.Cursor(); c.Row != 0 {
		t.Fatalf("the cursor must not move with the wheel, got %+v", c)
	}
	// Clamp at the bottom.
	for i := 0; i < 8; i++ {
		m = send(m, mouseWheel(true, x, y))
	}
	if m.firstRow >= 40 {
		t.Fatalf("firstRow = %d must be less than the total line count", m.firstRow)
	}
}

func TestClickPopupItemAccepts(t *testing.T) {
	m := sized("")
	m = typeText(m, "SELECT * FROM us")
	if !m.popup.open {
		t.Fatal("the completion popup must be open")
	}
	x, y, _, _, ok := m.popupGeometry()
	if !ok {
		t.Fatal("the popup geometry must be available")
	}
	// Click the first candidate row below the header.
	m = send(m, mousePress(x+1, y+1))
	if m.popup.open {
		t.Fatal("clicking a candidate must close the popup")
	}
	if got := m.buf.Text(); got != "SELECT * FROM users" {
		t.Fatalf("text = %q", got)
	}
}

func TestRightClickClosesPopup(t *testing.T) {
	m := sized("")
	m = typeText(m, "SELECT * FROM us")
	if !m.popup.open {
		t.Fatal("the completion popup must be open")
	}
	m = send(m, tea.MouseMsg{X: 10, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if m.popup.open {
		t.Fatal("the right button must close the popup")
	}
}

func TestDragAutoScrollAtBottomEdge(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "abcdef")
	}
	m := sized(strings.Join(lines, "\n"))
	x := editorX(m, 1)
	m = send(m, mousePress(x, editorRowY(m, 0)))
	if m.firstRow != 0 {
		t.Fatalf("firstRow = %d", m.firstRow)
	}
	// Drag to the bottom row of the text area to trigger auto-scroll.
	for i := 0; i < 5; i++ {
		m = send(m, mouseMotion(x, editorBottomY(m)))
	}
	if m.firstRow < 4 {
		t.Fatalf("firstRow = %d, dragging to the edge must auto-scroll", m.firstRow)
	}
	if c := m.buf.Cursor(); c.Row < 4 {
		t.Fatalf("cursor row = %d, must follow the drag scrolling", c.Row)
	}
}

// ---- Result grid pointer selection (round 7 §3) ----

func TestResultGridRowHoverDrawsWithoutMovingAnything(t *testing.T) {
	// Lip Gloss needs a colour profile for the styles to show up in the
	// strings; restore the plain one so later tests assert on visible text.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown}) // rows 1..2 selected
	cursorRow, selAnchor := m.session.result.cursorRow, m.session.result.selAnchor
	if selAnchor < 0 {
		t.Fatal("the test must start from a selection")
	}

	rect := m.paneRects().results
	top := m.resultDataTopY()
	m = send(m, mouseHover(rect.X+1, top+2)) // the third row
	if got := m.session.result.hover; got != 2 {
		t.Fatalf("hover = %d, want 2 (the row under the pointer)", got)
	}
	if m.session.result.cursorRow != cursorRow || m.session.result.selAnchor != selAnchor {
		t.Fatalf("hover moved the caret or the selection: cursor=%d anchor=%d, want %d/%d",
			m.session.result.cursorRow, m.session.result.selAnchor, cursorRow, selAnchor)
	}

	// The hover, the selection and a plain row must be three different looks
	// for the same row: render row 0 as selected, as hovered, and as neither
	// (the caret sits on row 1, so row 0 is never caret-styled).
	width, gutter := 60, m.resultsGutterWidth()
	selected := m.resultRow(0, 0, width, gutter) // selection range 0..1
	m.session.result.selAnchor = -1
	m.session.result.hover = 0
	hovered := m.resultRow(0, 0, width, gutter)
	m.session.result.hover = -1
	plain := m.resultRow(0, 0, width, gutter)
	if selected == plain {
		t.Fatalf("a selected row must be drawn differently from a plain one:\n%s", selected)
	}
	if hovered == plain {
		t.Fatalf("a hovered row must be drawn differently from a plain one:\n%s", hovered)
	}
	if hovered == selected {
		t.Fatalf("the hover must be distinguishable from the selection:\nhover:    %q\nselected: %q",
			hovered, selected)
	}
	// Restore the selection the render comparison dropped.
	m.session.result.selAnchor, m.session.result.cursorRow = selAnchor, cursorRow

	// Leaving the grid clears the hover and nothing else.
	m = send(m, mouseHover(rect.X+1, top+2))
	er := m.paneRects().editor
	m = send(m, mouseHover(er.X+2, er.Y+2))
	if m.session.result.hover != -1 {
		t.Fatalf("hover = %d, want -1 away from the rows", m.session.result.hover)
	}
	if m.session.result.cursorRow != cursorRow || m.session.result.selAnchor != selAnchor {
		t.Fatalf("leaving the grid changed cursor=%d anchor=%d, want %d/%d",
			m.session.result.cursorRow, m.session.result.selAnchor, cursorRow, selAnchor)
	}
}

func TestResultGridShiftClickAnchorsAtTheCaret(t *testing.T) {
	m := resultRowsModel(t)
	rect := m.paneRects().results
	top := m.resultDataTopY()

	// A plain click puts the caret on row 1 and selects nothing.
	m = send(m, mousePress(rect.X+1, top+1))
	m = send(m, mouseRelease(rect.X+1, top+1))
	if m.session.result.cursorRow != 1 {
		t.Fatalf("cursorRow = %d, want 1", m.session.result.cursorRow)
	}
	if m.session.result.hasRowSelection() {
		t.Fatal("a plain click must leave no selection")
	}

	// Shift+click anchors at the caret it takes over from and extends to the
	// clicked row: rows 1 and 2 of the view are selected (anchor 1, cursor 0).
	m = send(m, mouseShiftPress(rect.X+1, top))
	m = send(m, mouseRelease(rect.X+1, top))
	if got := m.session.result; got.selAnchor != 1 || got.cursorRow != 0 || got.selectedRowCount() != 2 {
		t.Fatalf("shift+click: anchor=%d cursor=%d selected=%d, want 1/0/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}

	// A plain click clears the selection and moves the caret.
	m = send(m, mousePress(rect.X+1, top+2))
	m = send(m, mouseRelease(rect.X+1, top+2))
	if m.session.result.selAnchor != -1 {
		t.Fatalf("a plain click must clear the selection, selAnchor = %d", m.session.result.selAnchor)
	}
	if m.session.result.cursorRow != 2 {
		t.Fatalf("cursorRow = %d, want 2", m.session.result.cursorRow)
	}
}

func TestResultGridDragSelectsRows(t *testing.T) {
	m := resultRowsModel(t)
	rect := m.paneRects().results
	top := m.resultDataTopY()

	m = send(m, mousePress(rect.X+1, top))    // press on the first row
	m = send(m, mouseMotion(rect.X+1, top+2)) // drag down over three rows
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 2 || got.selectedRowCount() != 3 {
		t.Fatalf("drag down: anchor=%d cursor=%d selected=%d, want 0/2/3",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}
	m = send(m, mouseMotion(rect.X+1, top)) // and back up to the anchor
	if got := m.session.result; got.cursorRow != 0 || got.selectedRowCount() != 1 {
		t.Fatalf("drag back: cursor=%d selected=%d, want 0/1", got.cursorRow, got.selectedRowCount())
	}
	m = send(m, mouseMotion(rect.X+1, top+1))
	m = send(m, mouseRelease(rect.X+1, top+1))
	if m.session.result.rowDrag {
		t.Fatal("the release must end the row drag")
	}
	if got := m.session.result; got.selAnchor != 0 || got.selectedRowCount() != 2 {
		t.Fatalf("after the release: anchor=%d selected=%d, want the selection kept (0/2)",
			got.selAnchor, got.selectedRowCount())
	}

	// A plain click afterwards starts over: the old selection is gone.
	m = send(m, mousePress(rect.X+1, top+2))
	m = send(m, mouseRelease(rect.X+1, top+2))
	if m.session.result.selAnchor != -1 {
		t.Fatalf("a plain click must clear the selection, selAnchor = %d", m.session.result.selAnchor)
	}
}

func TestResultGridColumnDragWinsOverRowSelection(t *testing.T) {
	m := resultRowsModel(t)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown}) // a selection the drags must not touch
	anchor, cursor := m.session.result.selAnchor, m.session.result.cursorRow
	if anchor < 0 {
		t.Fatal("the test must start from a selection")
	}

	// Resizing a column owns the pointer: motion over the rows changes the
	// width, never the selection, and no row drag starts.
	x, y := resultColumnEdge(m, 0)
	m = send(m, mousePress(x, y))
	if !m.colResize.active {
		t.Fatal("pressing the column boundary must start a resize")
	}
	if m.session.result.rowDrag {
		t.Fatal("the column press must not arm a row drag")
	}
	m = send(m, mouseMotion(x+5, m.resultDataTopY()+1))
	if !m.colResize.active {
		t.Fatal("the resize must stay active until the release")
	}
	if m.session.result.selAnchor != anchor || m.session.result.cursorRow != cursor {
		t.Fatalf("row drag stole the event: anchor=%d cursor=%d, want %d/%d",
			m.session.result.selAnchor, m.session.result.cursorRow, anchor, cursor)
	}
	if m.session.result.rowDrag || m.session.result.hover != -1 {
		t.Fatalf("motion during the resize armed a row drag or a hover: rowDrag=%v hover=%d",
			m.session.result.rowDrag, m.session.result.hover)
	}
	m = send(m, mouseRelease(x+5, y))
	if m.colResize.active {
		t.Fatal("the release must end the resize")
	}
	if m.session.result.selAnchor != anchor {
		t.Fatalf("the selection must survive the resize, selAnchor = %d", m.session.result.selAnchor)
	}
}

func TestResultGridScrollbarDragWinsOverRowSelection(t *testing.T) {
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
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	anchor, cursor := m.session.result.selAnchor, m.session.result.cursorRow
	if anchor < 0 {
		t.Fatal("the test must start from a selection")
	}

	sb, ok := m.resultsScrollMetrics()
	if !ok || sb.thumbHeight == 0 {
		t.Fatalf("long result must expose a scrollbar: %+v ok=%v", sb, ok)
	}
	thumbY := sb.y + sb.thumbTop + sb.thumbHeight/2
	m = send(m, mousePress(sb.x, thumbY))
	if !m.scrollDrag.active {
		t.Fatal("pressing the thumb must start a scrollbar drag")
	}
	if m.session.result.rowDrag {
		t.Fatal("the scrollbar press must not arm a row drag")
	}
	m = send(m, mouseMotion(sb.x, sb.y+sb.height-1))
	m = send(m, mouseMotion(m.paneRects().results.X+1, m.resultDataTopY()+1))
	if !m.scrollDrag.active {
		t.Fatal("the scrollbar drag must stay active until the release")
	}
	if m.session.result.selAnchor != anchor || m.session.result.cursorRow != cursor || m.session.result.rowDrag {
		t.Fatalf("row drag stole the event: anchor=%d cursor=%d rowDrag=%v, want %d/%d/false",
			m.session.result.selAnchor, m.session.result.cursorRow, m.session.result.rowDrag, anchor, cursor)
	}
	m = send(m, mouseRelease(sb.x, sb.y+sb.height-1))
	if m.scrollDrag.active {
		t.Fatal("the release must end the scrollbar drag")
	}
	if m.session.result.selAnchor != anchor || m.session.result.selectedRowCount() != 2 {
		t.Fatalf("the selection must survive the scroll: anchor=%d selected=%d",
			m.session.result.selAnchor, m.session.result.selectedRowCount())
	}
}

// TestSwitchTabDisarmsTheRowDragOfEverySession: rowDrag belongs to a gesture,
// and a gesture never survives a tab switch. The release that ends it may
// arrive for the *new* session (or never arrive at all when the button was
// let go outside the terminal window), so the switch itself has to clear the
// flag — in the tab left behind as well as in the one that becomes active.
func TestSwitchTabDisarmsTheRowDragOfEverySession(t *testing.T) {
	m := resultRowsModel(t)
	m.newTabNamed("second", "SELECT 1")
	if len(m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(m.tabs))
	}
	// Back to the result tab through the shortcut a keyboard user has.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+pgdown")})
	if m.active != 0 {
		t.Fatalf("active tab = %d, want the result tab", m.active)
	}

	rect := m.paneRects().results
	top := m.resultDataTopY()
	m = send(m, mouseHover(rect.X+1, top+1))
	if m.session.result.hover != 1 {
		t.Fatalf("hover = %d, want 1 (precondition)", m.session.result.hover)
	}
	m = send(m, mousePress(rect.X+1, top)) // button down, no release ever
	if !m.session.result.rowDrag {
		t.Fatal("precondition: the press must arm the row drag")
	}
	leftBehind := m.session

	// Switch tabs while the button is still held.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+pgdown")})
	if m.active != 1 {
		t.Fatalf("active tab = %d, want the second tab", m.active)
	}
	if leftBehind.result.rowDrag {
		t.Fatal("switching tabs must disarm the row drag of the tab left behind")
	}
	if leftBehind.result.hover != -1 {
		t.Fatalf("hover of the tab left behind = %d, want -1", leftBehind.result.hover)
	}
	if m.session.result.rowDrag || m.session.result.hover != -1 {
		t.Fatalf("the new session starts with rowDrag=%v hover=%d, want false/-1",
			m.session.result.rowDrag, m.session.result.hover)
	}

	// The release arrives late, for the session that was switched to: it
	// must not resurrect anything anywhere.
	m = send(m, mouseRelease(rect.X+1, top))
	if leftBehind.result.rowDrag || m.session.result.rowDrag {
		t.Fatal("the late release must leave every row drag disarmed")
	}

	// Back in the grid: a motion over the rows starts nothing from the
	// stale anchor either.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+pgdown")})
	m = send(m, mouseMotion(rect.X+1, top+2))
	if m.session.result.rowDrag {
		t.Fatal("a motion without an armed press must not start a row drag")
	}
	if m.session.result.cursorRow != 0 || m.session.result.selAnchor != -1 {
		t.Fatalf("motion changed the grid: cursor=%d anchor=%d, want 0/-1",
			m.session.result.cursorRow, m.session.result.selAnchor)
	}
}

// TestResultGridPressOnEmptySpaceDisarmsAStaleRowDrag: a press that hits no
// row (the empty space below the rows, the summary line) starts no drag, so
// it must also end a drag whose release never arrived. Otherwise the motion
// that follows extends the old selection from an anchor the user cannot see
// anymore.
func TestResultGridPressOnEmptySpaceDisarmsAStaleRowDrag(t *testing.T) {
	m := resultRowsModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 40}) // room below the three rows
	rect := m.paneRects().results
	top := m.resultDataTopY()
	below := rect.Y + rect.H - 1
	if _, _, ok := m.resultPointAt(rect.X+1, below); ok {
		t.Fatalf("precondition: y=%d must fall below the three rows", below)
	}

	// A press on a row arms the drag; its release never arrives.
	m = send(m, mousePress(rect.X+1, top))
	if !m.session.result.rowDrag {
		t.Fatal("precondition: the press must arm the row drag")
	}
	// The selection of the test: rows 0..1.
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 {
		t.Fatalf("precondition selection: anchor=%d cursor=%d, want 0/1", got.selAnchor, got.cursorRow)
	}

	// A shift press on the empty space hits no row: it disarms the stale
	// drag and leaves the selection exactly where it was.
	m = send(m, mouseShiftPress(rect.X+1, below))
	if m.session.result.rowDrag {
		t.Fatal("a press that hit no row must disarm the row drag")
	}
	m = send(m, mouseMotion(rect.X+1, top+2)) // over the third row
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("the motion extended a stale drag: anchor=%d cursor=%d rows=%d, want 0/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}

	// The same for a plain press: it clears the selection (its documented
	// job) and no motion may bring one back from the old anchor.
	m = send(m, mousePress(rect.X+1, below))
	if m.session.result.rowDrag {
		t.Fatal("a plain press on empty space must disarm the row drag too")
	}
	if m.session.result.selAnchor != -1 {
		t.Fatalf("selAnchor = %d after a plain press on empty space, want -1", m.session.result.selAnchor)
	}
	m = send(m, mouseMotion(rect.X+1, top+2))
	if m.session.result.selAnchor != -1 || m.session.result.cursorRow != 1 {
		t.Fatalf("the motion re-armed a selection: anchor=%d cursor=%d, want -1/1",
			m.session.result.selAnchor, m.session.result.cursorRow)
	}
	m = send(m, mouseRelease(rect.X+1, below))
}

// TestResultRowDragIgnoresSweepsAfterTheReleaseWentMissing: a drag armed on a
// result row whose button goes up outside the window never receives its
// release event, so nothing clears the flag — and the sweep that follows
// across the grid must not keep growing the selection from that anchor. Only a
// motion that still carries the left button is a drag.
func TestResultRowDragIgnoresSweepsAfterTheReleaseWentMissing(t *testing.T) {
	m := resultRowsModel(t)
	rect := m.paneRects().results
	top := m.resultDataTopY()

	m = send(m, mousePress(rect.X+1, top)) // press on row 0
	if !m.session.result.rowDrag {
		t.Fatal("precondition: the press must arm the row drag")
	}
	m = send(m, mouseMotion(rect.X+1, top+1)) // held, dragged onto row 1
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("drag down: anchor=%d cursor=%d selected=%d, want 0/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}

	// The button goes up outside the window: no release arrives. Everything
	// the terminal reports afterwards is a pointer move — with no button held,
	// and, once another button comes into play, with the wrong one.
	m = send(m, mouseHover(rect.X+1, top+2))
	m = send(m, mouseHover(rect.X+1, top))
	m = send(m, tea.MouseMsg{X: rect.X + 1, Y: top + 2,
		Action: tea.MouseActionMotion, Button: tea.MouseButtonRight})
	if got := m.session.result; got.selAnchor != 0 || got.cursorRow != 1 || got.selectedRowCount() != 2 {
		t.Fatalf("a sweep extended the drag whose release never arrived: anchor=%d cursor=%d selected=%d, want 0/1/2",
			got.selAnchor, got.cursorRow, got.selectedRowCount())
	}
}

// TestAnyPressDisarmsTheRowDrag: rowDrag belongs to one gesture, and a press
// starts a new one — wherever the button goes down and whichever button it
// is. Without that rule a stale drag (its release was lost outside the window,
// or swallowed by an earlier branch) survives a click on chrome the grid never
// sees, and the next left-button motion across the rows keeps extending a
// selection from an anchor the user cannot see anymore.
func TestAnyPressDisarmsTheRowDrag(t *testing.T) {
	// pressThenSweep arms the drag on row 0, runs one press on some chrome,
	// and then sweeps across the rows with the left button held: the press has
	// to disarm the drag, so the sweep moves nothing.
	pressThenSweep := func(t *testing.T, press func(m Model) Model) {
		t.Helper()
		m := resultRowsModel(t)
		rect := m.paneRects().results
		top := m.resultDataTopY()

		m = send(m, mousePress(rect.X+1, top))
		if !m.session.result.rowDrag {
			t.Fatal("precondition: the press must arm the row drag")
		}
		m = press(m)
		if m.session.result.rowDrag {
			t.Fatal("a press must disarm the row drag of the gesture before it")
		}
		m = send(m, mouseMotion(rect.X+1, top+2))
		if got := m.session.result; got.selAnchor != -1 || got.cursorRow != 0 || got.selectedRowCount() != 0 {
			t.Fatalf("the sweep extended a stale drag: anchor=%d cursor=%d selected=%d, want -1/0/0",
				got.selAnchor, got.cursorRow, got.selectedRowCount())
		}
	}

	// freeToolbarX finds a column of the toolbar row that is not a button, so
	// the press is a click on the bar itself and runs no action.
	freeToolbarX := func(t *testing.T, m Model) int {
		t.Helper()
		tr := m.paneRects().toolbar
		for x := tr.X; x < tr.X+tr.W; x++ {
			if _, ok := m.toolItemAt(x, tr.Y); !ok {
				return x
			}
		}
		t.Fatal("the toolbar row leaves no column to press on")
		return 0
	}

	t.Run("toolbar", func(t *testing.T) {
		pressThenSweep(t, func(m Model) Model {
			return send(m, mousePress(freeToolbarX(t, m), m.paneRects().toolbar.Y))
		})
	})

	t.Run("menu bar", func(t *testing.T) {
		pressThenSweep(t, func(m Model) Model {
			// Clicking a title opens the menu and clicking it again closes
			// it: both presses belong to the click, neither may leave the
			// drag armed once the menu is out of the way again.
			idx, ok := m.menuTitleAt(m.paneRects().menubar.X, m.paneRects().menubar.Y)
			if !ok {
				t.Fatal("the menu bar must expose its first title")
			}
			x, y := m.paneRects().menubar.X, m.paneRects().menubar.Y
			m = send(m, mousePress(x, y))
			if m.menu.open != idx {
				t.Fatalf("menu.open = %d, want %d (precondition)", m.menu.open, idx)
			}
			return send(m, mousePress(x, y))
		})
	})

	t.Run("right button", func(t *testing.T) {
		pressThenSweep(t, func(m Model) Model {
			x := freeToolbarX(t, m)
			return send(m, tea.MouseMsg{X: x, Y: m.paneRects().toolbar.Y,
				Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
		})
	})
}

// TestResultGridPressIsSwallowedWhileTheCellEditorIsOpen: the cell editor
// owns the grid while it is open (the way it owns the keyboard), so a press
// must not move the caret off the cell the edit is bound to and must not
// commit the typed value into whatever cell the pointer happens to sit on.
func TestResultGridPressIsSwallowedWhileTheCellEditorIsOpen(t *testing.T) {
	conn := e2eConn(t)
	m := gridEditModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = gridEditResult(t, m)
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyRight}) // off the primary key
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.session.result.editing == nil {
		t.Fatalf("precondition: the cell editor must open: %q", m.status.message)
	}
	editing := m.session.result.editing
	if editing.row != 0 || editing.col != 1 {
		t.Fatalf("the edit is bound to (%d,%d), want the caret cell (0,1)", editing.row, editing.col)
	}

	rect := m.paneRects().results
	y := m.resultDataTopY() + 1 // the second row, a different cell
	m = send(m, mousePress(rect.X+1, y))
	m = send(m, mouseMotion(rect.X+1, y))
	m = send(m, mouseRelease(rect.X+1, y))

	if m.session.result.editing != editing {
		t.Fatal("the press must leave the open cell editor alone")
	}
	if m.session.result.cursorRow != 0 || m.session.result.cursorCol != 1 {
		t.Fatalf("caret = (%d,%d), the press must not move it off the edited cell (0,1)",
			m.session.result.cursorRow, m.session.result.cursorCol)
	}
	if n := m.session.result.pendingCount(); n != 0 {
		t.Fatalf("pending changes = %d, the press must not commit the edit", n)
	}

	// Esc still ends the edit the way it always did, without writing it.
	m = keyByName(m, "esc")
	if m.session.result.editing != nil {
		t.Fatal("Esc must close the cell editor")
	}
	if n := m.session.result.pendingCount(); n != 0 {
		t.Fatalf("pending changes = %d, cancelling must not write anything", n)
	}
}

// TestResultGridScrollDropsTheHover: hover is a view position derived from
// the screen, so a scroll under a still pointer leaves it naming another
// row. The grid must drop it instead of highlighting a row the pointer
// never entered (round 7 §1).
func TestResultGridScrollDropsTheHover(t *testing.T) {
	// Lip Gloss needs a colour profile for the row styles to show up in the
	// strings; restore the plain one so later tests assert on visible text.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	conn := e2eConn(t)
	for i := 4; i < 60; i++ {
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
	m.layout.focus = paneResults

	rect := m.paneRects().results
	top := m.resultDataTopY()
	m = send(m, mouseHover(rect.X+1, top+1)) // the second row
	if m.session.result.hover != 1 {
		t.Fatalf("hover = %d, want 1 (the row under the pointer)", m.session.result.hover)
	}
	gutter := m.resultsGutterWidth()
	hovered := m.resultRow(1, 1, 60, gutter)

	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyPgDown}) // scrolls under the pointer
	if m.session.result.scrollY == 0 {
		t.Fatal("the grid must scroll for this test")
	}
	if m.session.result.hover != -1 {
		t.Fatalf("hover = %d after scrolling, want -1 (stale)", m.session.result.hover)
	}
	if plain := m.resultRow(1, 1, 60, gutter); plain == hovered {
		t.Fatalf("the scrolled grid must not keep the hover highlight:\n%s", plain)
	}
}

// ---- Clipboard / paste ----

func captureClipboard(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := clipboardWriter
	clipboardWriter = &buf
	t.Cleanup(func() { clipboardWriter = old })
	return &buf
}

func TestCtrlCCopiesSelection(t *testing.T) {
	buf := captureClipboard(t)
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Fatal("Ctrl+C must not quit while there is a selection")
	}
	if cmd == nil {
		t.Fatal("it must return a copy command")
	}
	msg := cmd()
	if !strings.Contains(statusMsgText(msg), "Copied") {
		t.Fatalf("status message = %v", msg)
	}
	if !strings.HasPrefix(buf.String(), "\x1b]52;c;") {
		t.Fatalf("OSC52 sequence = %q", buf.String())
	}
	b64 := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decoding failed: %v", err)
	}
	if string(decoded) != "* FROM" {
		t.Fatalf("clipboard contents = %q", decoded)
	}
}

func TestCtrlCQuitsWithoutSelection(t *testing.T) {
	m := sized("SELECT 1")
	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting || cmd == nil {
		t.Fatal("Ctrl+C must quit when there is no selection")
	}
}

func TestCtrlXCutsSelection(t *testing.T) {
	buf := captureClipboard(t)
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyCtrlX})
	if cmd != nil {
		cmd()
	}
	if got := m.buf.Text(); got != "SELECT  users" {
		t.Fatalf("after cut = %q", got)
	}
	b64 := strings.TrimSuffix(strings.TrimPrefix(buf.String(), "\x1b]52;c;"), "\x07")
	decoded, _ := base64.StdEncoding.DecodeString(b64)
	if string(decoded) != "* FROM" {
		t.Fatalf("clipboard = %q", decoded)
	}
	if m.hasSelection() {
		t.Fatal("the selection must be cleared after cutting")
	}
}

func TestCtrlASelectsAll(t *testing.T) {
	m := sized("SELECT * FROM users")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	if got := m.selectedText(); got != "SELECT * FROM users" {
		t.Fatalf("select all = %q", got)
	}
}

// ---- Keyboard selection ----

func TestShiftArrowExtendsSelection(t *testing.T) {
	m := sized("SELECT * FROM users")
	// The fixture starts on the welcome page, where the arrow keys steer the
	// action list; this test is about the editor's shift selection.
	m.welcomeToEditor()
	shiftRight := func(m Model) Model { return send(m, tea.KeyMsg{Type: tea.KeyShiftRight}) }
	shiftLeft := func(m Model) Model { return send(m, tea.KeyMsg{Type: tea.KeyShiftLeft}) }

	m = shiftRight(m)
	m = shiftRight(m)
	if got := m.selectedText(); got != "SE" {
		t.Fatalf("shift+right selection = %q, want SE", got)
	}
	// A plain move clears the selection.
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.hasSelection() {
		t.Fatal("a plain move must clear the selection")
	}
	// Start a new selection at the caret and extend left.
	m = shiftLeft(m)
	if got := m.selectedText(); got != "L" {
		t.Fatalf("shift+left selection = %q, want L", got)
	}
	// Back at the anchor → empty selection but the anchor survives; extending
	// right again re-grows it.
	m = shiftRight(m)
	if m.hasSelection() {
		t.Fatalf("the selection must be empty at the anchor, got %q", m.selectedText())
	}
	m = shiftRight(m)
	if got := m.selectedText(); got != "E" {
		t.Fatalf("past the anchor = %q, want E", got)
	}
}

// ---- Editing and selection interaction ----

func TestTypingReplacesSelection(t *testing.T) {
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	m = typeText(m, "1=1")
	if got := m.buf.Text(); got != "SELECT 1=1 users" {
		t.Fatalf("after replacing = %q", got)
	}
	if m.hasSelection() {
		t.Fatal("typing must clear the selection")
	}
}

func TestBackspaceDeletesSelection(t *testing.T) {
	m := sized("SELECT * FROM users")
	// The fixture starts on the welcome page, where Backspace means nothing;
	// this test is about deleting a selection in the editor.
	m.welcomeToEditor()
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	m = keyByName(m, "backspace")
	if got := m.buf.Text(); got != "SELECT  users" {
		t.Fatalf("after deleting = %q", got)
	}
	if m.hasSelection() {
		t.Fatal("deleting must clear the selection")
	}
}

func TestPasteInsertsMultiline(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("SELECT 1\r\nFROM users"),
		Paste: true,
	})
	lines := strings.Split(m.buf.Text(), "\n")
	if len(lines) != 2 || lines[0] != "SELECT 1" || lines[1] != "FROM users" {
		t.Fatalf("after pasting = %q", m.buf.Text())
	}
}

func TestPasteReplacesSelection(t *testing.T) {
	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Paste: true})
	if got := m.buf.Text(); got != "SELECT x users" {
		t.Fatalf("after pasting over the selection = %q", got)
	}
}

// ---- Rendering ----

func TestSelectionRendersHighlight(t *testing.T) {
	// Lip Gloss does not emit ANSI without a TTY by default, so force color on
	// in the test and restore the plain profile afterwards: it is global state
	// and later tests assert on the visible text.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("SELECT * FROM users")
	m.sel.anchor = &textbuf.Pos{Row: 0, Col: 7}
	m.buf.SetCursor(textbuf.Pos{Row: 0, Col: 13})
	view := m.View()
	// The selection is rendered in reverse video: the first editor text line
	// must contain several SGR sequences (gutter + selection runs).
	line := strings.Split(view, "\n")[editorRowY(m, 0)]
	if strings.Count(line, "\x1b[") < 3 {
		t.Fatalf("the selection line must contain reverse-video sequences: %q", line)
	}
	if !strings.Contains(view, "selected") {
		t.Fatalf("the status bar must show the selection info:\n%s", view)
	}
}

// statusMsgText extracts the text of a message command (when it is a status
// message).
func statusMsgText(msg tea.Msg) string {
	if sm, ok := msg.(statusMessageMsg); ok {
		return string(sm)
	}
	return ""
}

// TestDialogHitRectsStayInsideOn40x80 pins the geometry every dialog owes
// the pointer (round 7 §1): on the smallest useful screens the modal box and
// every dropdown hit rectangle stay inside the pane area, every drawn row of
// a list maps back to a real entry, and a point on a border or outside the
// frame maps to nothing at all.
func TestDialogHitRectsStayInsideOn40x80(t *testing.T) {
	for _, size := range []struct{ w, h int }{{40, 80}, {80, 40}} {
		m := dialogModel(t, size.w, size.h)
		area := m.paneRects().area
		inside := func(name string, r Rect) {
			t.Helper()
			if r.X < area.X || r.Y < area.Y || r.X+r.W > area.X+area.W || r.Y+r.H > area.Y+area.H {
				t.Fatalf("%dx%d: %s rect %+v escapes the area %+v", size.w, size.h, name, r, area)
			}
		}
		// selectRows checks that every option row the frame shows maps back
		// to a real option, and that the frame's left border maps to nothing.
		selectRows := func(name string, l selectLayout, options []selectOption) {
			t.Helper()
			inside(name, l.rect)
			for row := 0; row < l.visible; row++ {
				idx, ok := selectRowAt(l, options, l.rect.X+1, l.rect.Y+1+row)
				if !ok || idx < 0 || idx >= len(options) {
					t.Fatalf("%dx%d: %s row %d maps to %d (ok = %v), options = %d",
						size.w, size.h, name, row, idx, ok, len(options))
				}
			}
			if _, ok := selectRowAt(l, options, l.rect.X, l.rect.Y+1); ok {
				t.Fatalf("%dx%d: the %s border must not map to an option", size.w, size.h, name)
			}
		}

		// Settings dialog and its dropdown.
		m.dlg.open = true
		l := m.dialogLayout()
		inside("settings", Rect{X: l.x, Y: l.y, W: l.w, H: l.h})
		m.dlg.choice.Open(m.localeOptions(), "")
		cl, ok := m.settingsChoiceLayout()
		if !ok {
			t.Fatalf("%dx%d: the settings dropdown must have a layout", size.w, size.h)
		}
		selectRows("settings dropdown", cl, m.dlg.choice.options)
		m.dlg.choice.Close()
		m.dlg.open = false

		// Export dialog and its dropdown.
		m.exportDlg = exportDialog{open: true, path: "rows.csv"}
		el := m.exportLayout()
		inside("export", Rect{X: el.x, Y: el.y, W: el.w, H: el.h})
		m.exportDlg.focus = 1
		m.openExportChoice()
		ecl, ok := m.exportChoiceLayout()
		if !ok {
			t.Fatalf("%dx%d: the export dropdown must have a layout", size.w, size.h)
		}
		selectRows("export dropdown", ecl, m.exportDlg.choice.options)
		m.closeExportDialog()

		// Connection form and its own option list (it is not a selectList:
		// its hit test is connDropRowAt).
		m.openConnDialog()
		m.connDlg.focus = 1 // the type field
		m = keyByName(m, "enter")
		if !m.connDlg.dropdown {
			t.Fatalf("%dx%d: enter on the type field must open the list", size.w, size.h)
		}
		d, _, visible, ok := m.connDropdownLayout()
		if !ok {
			t.Fatalf("%dx%d: the connection dropdown must have a layout", size.w, size.h)
		}
		inside("conn dropdown", d)
		options := m.connDlg.fields[m.connDlg.focus].options
		for row := 0; row < visible; row++ {
			idx, ok := m.connDropRowAt(d.X+1, d.Y+1+row)
			if !ok || idx < 0 || idx >= len(options) {
				t.Fatalf("%dx%d: conn dropdown row %d maps to %d (ok = %v), options = %d",
					size.w, size.h, row, idx, ok, len(options))
			}
		}
		if _, ok := m.connDropRowAt(d.X, d.Y+1); ok {
			t.Fatalf("%dx%d: the connection list border must not map to an option", size.w, size.h)
		}
		m.closeConnDialog()

		// Vault manager: its body rows are its entries, in order.
		m.openVaultMode(vaultManage)
		vl := m.vaultLayout()
		inside("vault", Rect{X: vl.x, Y: vl.y, W: vl.w, H: vl.h})
		for i := range vaultManageEntries() {
			row := m.vaultRowAt(vl.x+2, vl.rowY+i)
			if row != i {
				t.Fatalf("%dx%d: vault row %d maps to %d", size.w, size.h, i, row)
			}
		}
		if row := m.vaultRowAt(vl.x-1, vl.rowY); row != -1 {
			t.Fatalf("%dx%d: outside the vault box maps to %d, want -1", size.w, size.h, row)
		}
		m.closeVault()
	}
}
