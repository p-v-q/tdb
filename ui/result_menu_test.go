package ui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// resultCellPoint returns the screen coordinate of a cell in the current view.
func resultCellPoint(m Model, displayRow, col int) (int, int) {
	r := m.paneRects().results
	x := r.X + m.resultsGutterWidth()
	for _, c := range m.visibleResultCols() {
		if c == col {
			x += m.session.result.colWidths[c] / 2
			break
		}
		x += m.session.result.colWidths[c] + 1
	}
	y := m.resultDataTopY() + displayRow - m.session.result.scrollY
	return x, y
}

func resultRightClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight}
}

func resultActionIndex(m Model, want resultMenuAction) int {
	for i, a := range m.resultMenu.actions {
		if a == want {
			return i
		}
	}
	return -1
}

func clickResultAction(m Model, action resultMenuAction) (Model, tea.Cmd) {
	idx := resultActionIndex(m, action)
	if idx < 0 {
		return m, nil
	}
	r, ok := m.resultMenuRect()
	if !ok {
		return m, nil
	}
	return sendCmd(m, mousePress(r.X+2, r.Y+1+idx))
}

func clipboardText(t *testing.T, buf interface{ String() string }) string {
	t.Helper()
	raw := buf.String()
	b64 := strings.TrimSuffix(strings.TrimPrefix(raw, "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decoding failed: %v", err)
	}
	return string(decoded)
}

func TestResultRightClickCellCopiesCell(t *testing.T) {
	buf := captureClipboard(t)
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	x, y := resultCellPoint(m, 0, 1) // Ada's email
	m = send(m, resultRightClick(x, y))
	if !m.resultMenu.open {
		t.Fatal("right-clicking a result cell must open the context menu")
	}
	if m.resultMenu.kind != resultTargetCell || m.resultMenu.row != 0 || m.resultMenu.col != 1 {
		t.Fatalf("menu target = %+v", m.resultMenu)
	}
	if m.layout.focus != paneResults {
		t.Fatal("right-clicking the grid must focus it")
	}
	view := stripANSI(m.View())
	for _, want := range []string{
		"Copy cell", "Copy row", "Copy column", "Sort ascending",
		"Sort descending", "Filter by this value", "Export results",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("result menu is missing %q:\n%s", want, view)
		}
	}

	next, cmd := clickResultAction(m, resultActCopyCell)
	if next.resultMenu.open {
		t.Fatal("choosing an action must close the menu")
	}
	if cmd == nil {
		t.Fatal("copy cell must return a command")
	}
	cmd()
	if got := clipboardText(t, buf); got != "ada@example.com" {
		t.Fatalf("clipboard = %q, want Ada's email", got)
	}
}

func TestResultMenuCopiesNULLAndEmptyCells(t *testing.T) {
	buf := captureClipboard(t)
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT name FROM users WHERE id = 2")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m.session.result.cursorRow, m.session.result.cursorCol = 0, 0

	m, cmd := sendCmd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y must copy the selected cell")
	}
	cmd()
	if got := clipboardText(t, buf); got != "NULL" {
		t.Fatalf("clipboard = %q, want NULL", got)
	}
}

func TestResultMenuSwitchesResultSets(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT 'first' AS value;\nSELECT 'second' AS value")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlA})
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	x, y := resultCellPoint(m, 0, 0)
	m = send(m, resultRightClick(x, y))
	if resultActionIndex(m, resultActPrevSet) < 0 || resultActionIndex(m, resultActNextSet) < 0 {
		t.Fatalf("menu actions = %v, want result-set navigation", m.resultMenu.actions)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Previous result set", "Next result set"} {
		if !strings.Contains(view, want) {
			t.Fatalf("menu is missing %q:\n%s", want, view)
		}
	}

	m, _ = clickResultAction(m, resultActPrevSet)
	if m.session.resultIdx != 0 {
		t.Fatalf("active result set = %d, want 0", m.session.resultIdx)
	}
	if got := *m.session.result.res.Rows[0][0]; got != "first" {
		t.Fatalf("active value = %q, want first", got)
	}
}

func TestResultHeaderRightClickSorts(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	x, _ := resultCellPoint(m, 0, 0)
	y := m.paneRects().results.Y + 2 // header row
	m = send(m, resultRightClick(x, y))
	if !m.resultMenu.open || m.resultMenu.kind != resultTargetHeader {
		t.Fatalf("header menu = %+v", m.resultMenu)
	}

	m, _ = clickResultAction(m, resultActSortDesc)
	if m.session.result.sortCol != 0 || !m.session.result.sortDesc {
		t.Fatalf("sort = col %d desc %v, want column 0 descending",
			m.session.result.sortCol, m.session.result.sortDesc)
	}
}

func TestResultMenuFiltersByCell(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT name FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})

	x, y := resultCellPoint(m, 0, 0) // Ada
	m = send(m, resultRightClick(x, y))
	m, _ = clickResultAction(m, resultActFilterCell)
	if m.session.result.filter != "Ada" {
		t.Fatalf("filter = %q, want Ada", m.session.result.filter)
	}
	if got := len(m.session.result.viewRows()); got != 1 {
		t.Fatalf("filtered rows = %d, want 1", got)
	}
}

func TestShiftF10OpensResultMenuAtCaret(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m.session.result.cursorRow, m.session.result.cursorCol = 1, 1

	// Bubble Tea has no separate Shift+F10 key type; terminals that support it
	// send the name as runes, which is how the existing tree menu test does it.
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+f10")})
	if !m.resultMenu.open || m.resultMenu.row != 1 || m.resultMenu.col != 1 {
		t.Fatalf("menu = %+v, want the caret cell", m.resultMenu)
	}
}

// TestF13OpensTheResultMenuAtCaret pins the shortcut for the terminals that
// send Shift+F10 as F13 ("\x1b[25~", "\x1b[1;2P"): the F13 key must open the
// context menu of the cell under the caret, like Shift+F10 does.
func TestF13OpensTheResultMenuAtCaret(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m.session.result.cursorRow, m.session.result.cursorCol = 1, 1

	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if !m.resultMenu.open || m.resultMenu.row != 1 || m.resultMenu.col != 1 {
		t.Fatalf("menu = %+v, want the caret cell", m.resultMenu)
	}
}

// TestShiftF10OpensTheResultMenuWithoutASelection: like a right click, the
// keyboard entry point is a fresh caret placement for the menu. The menu
// acts on the cell under the caret and ignores a row selection, so the
// selection is dropped instead of surviving next to a menu that does not
// use it (the right-click path already does this).
func TestShiftF10OpensTheResultMenuWithoutASelection(t *testing.T) {
	conn := e2eConn(t)
	m := e2eModel(t, conn, "SELECT id, email FROM users ORDER BY id")
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyF5})
	m.layout.focus = paneResults
	m = runToCompletion(m, tea.KeyMsg{Type: tea.KeyShiftDown})
	if !m.session.result.hasRowSelection() {
		t.Fatal("the test must start from a row selection")
	}

	m = send(m, tea.KeyMsg{Type: tea.KeyF13})
	if !m.resultMenu.open {
		t.Fatal("Shift+F10 must open the result menu")
	}
	if m.session.result.selAnchor != -1 {
		t.Fatalf("selAnchor = %d, opening the menu must drop the row selection",
			m.session.result.selAnchor)
	}
	if m.session.result.cursorRow != 1 {
		t.Fatalf("cursorRow = %d, want 1 (the caret stays where the selection ended)",
			m.session.result.cursorRow)
	}
}
