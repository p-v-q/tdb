package ui

// hscroll_test.go covers the horizontal scrollbar of the result grid: it appears
// only when the columns are wider than the pane, it is drawn on the last body
// row, it costs one data row, and clicking or dragging it scrolls the columns.

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/driver"
)

// resultModel builds a model showing one result set with the given columns and
// one row, at a normal terminal size with the results pane visible.
func resultModel(t *testing.T, columns int, nameLen int, valueLen int, rows int) Model {
	t.Helper()
	m := sized("")
	m.welcome = false
	m.layout.resultsVisible = true
	m.layout.focus = paneResults

	cols := make([]driver.ResultColumn, columns)
	for i := range cols {
		cols[i] = driver.ResultColumn{Name: strings.Repeat(string(rune('a'+i%26)), nameLen), Type: "text"}
	}
	wide := strings.Repeat("x", valueLen)
	data := make([][]*string, rows)
	for r := range data {
		row := make([]*string, columns)
		for c := range row {
			v := wide
			row[c] = &v
		}
		data[r] = row
	}
	m.session.result.setResult(&driver.Result{
		Columns:  cols,
		Rows:     data,
		Duration: time.Millisecond,
	}, nil, "SELECT wide")
	return m
}

func TestResultsHorizontalScrollbarShowsWhenColumnsOverflow(t *testing.T) {
	m := resultModel(t, 8, 12, maxColWidth, 5)
	if !m.layout.resultsVisible {
		t.Fatal("setup: the results pane must be visible")
	}
	if !m.resultsHScrollBarShown() {
		t.Fatalf("content = %d cells, pane columns = %d: the bar must show",
			m.resultContentWidth(), m.resultsGridWidth())
	}
	hs, ok := m.resultsHScrollMetrics()
	if !ok {
		t.Fatal("the visible bar must have geometry")
	}
	if hs.y != m.paneRects().results.Y+m.resultsBodyHeight() {
		t.Fatalf("bar row = %d, want the pane's last body row", hs.y)
	}
	if hs.thumbWidth >= hs.width {
		t.Fatalf("thumb %d fills the whole track %d: the grid fits after all", hs.thumbWidth, hs.width)
	}
	// The bar takes one row from the data.
	rows := m.renderResultBody(m.paneRects().results.W, m.resultsBodyHeight())
	bar := stripANSI(rows[len(rows)-1])
	if !strings.ContainsAny(bar, "░█") {
		t.Fatalf("last body row = %q, want the scrollbar track", bar)
	}
	// The vertical bar shrinks by the same row, so the two never overlap.
	if v, ok := m.resultsScrollMetrics(); ok && v.y+v.height > hs.y {
		t.Fatalf("vertical bar ends at %d, horizontal bar starts at %d", v.y+v.height, hs.y)
	}
}

func TestResultsHorizontalScrollbarClickMovesColumns(t *testing.T) {
	m := resultModel(t, 8, 12, maxColWidth, 5)
	before := m.visibleResultCols()
	if len(before) == 0 || before[0] != 0 {
		t.Fatalf("setup: visible columns = %v", before)
	}
	hs, ok := m.resultsHScrollMetrics()
	if !ok {
		t.Fatal("no horizontal scrollbar to click")
	}
	// Click near the right end of the track: the view jumps to the right.
	m.beginHScrollDrag(hs, hs.x+hs.width-1)
	m.finishHScrollDrag()
	after := m.visibleResultCols()
	if m.session.result.scrollX == 0 {
		t.Fatalf("scrollX = 0 after clicking the right end of the bar (columns %v)", after)
	}
	if after[0] <= before[0] {
		t.Fatalf("first visible column = %d, want it past %d", after[0], before[0])
	}
	if last := after[len(after)-1]; last != len(m.session.result.colWidths)-1 && last < len(m.session.result.colWidths)-1 {
		t.Fatalf("the last column %d is not reachable, view = %v", len(m.session.result.colWidths)-1, after)
	}
}

func TestResultsHorizontalScrollbarDragScrollsColumns(t *testing.T) {
	m := resultModel(t, 8, 12, maxColWidth, 5)
	m.session.result.scrollX = 0
	hs, ok := m.resultsHScrollMetrics()
	if !ok {
		t.Fatal("no horizontal scrollbar to drag")
	}
	// Press on the thumb, then drag it to the right end.
	m.beginHScrollDrag(hs, hs.x+hs.thumbLeft+hs.thumbWidth/2)
	if !m.hScrollDrag.active {
		t.Fatal("the press must start a horizontal drag")
	}
	m.dragHScroll(hs.x + hs.width - 1)
	if m.session.result.scrollX == 0 {
		t.Fatal("dragging the thumb right must scroll the columns")
	}
	m.finishHScrollDrag()
	if m.hScrollDrag.active {
		t.Fatal("the release must end the drag")
	}
	if m.layout.focus != paneResults {
		t.Fatal("the drag must keep the grid focused")
	}
}

func TestResultsHorizontalScrollbarHiddenWhenColumnsFit(t *testing.T) {
	m := resultModel(t, 2, 4, 3, 3)
	if m.resultsHScrollBarShown() {
		t.Fatalf("content = %d cells, pane = %d: narrow columns must not show a bar",
			m.resultContentWidth(), m.resultsGridWidth())
	}
	rows := m.renderResultBody(m.paneRects().results.W, m.resultsBodyHeight())
	for _, row := range rows {
		if strings.ContainsAny(stripANSI(row), "░█") {
			t.Fatalf("no bar row expected, got %q", stripANSI(row))
		}
	}
}

func TestResultsHorizontalScrollBarScrollsWithTheKeyboard(t *testing.T) {
	// The bar reflects the column scroll the arrow keys already drive: moving
	// the caret right past the last visible column scrolls and moves the thumb.
	m := resultModel(t, 8, 12, maxColWidth, 5)
	hs0, ok := m.resultsHScrollMetrics()
	if !ok {
		t.Fatal("no bar")
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	for i := 0; i < len(m.session.result.colWidths); i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.session.result.scrollX == 0 {
		t.Fatal("moving the caret across the columns must scroll the grid")
	}
	hs1, ok := m.resultsHScrollMetrics()
	if !ok {
		t.Fatal("no bar after scrolling")
	}
	if hs1.thumbLeft <= hs0.thumbLeft {
		t.Fatalf("thumb moved from %d to %d, want it to the right", hs0.thumbLeft, hs1.thumbLeft)
	}
}
