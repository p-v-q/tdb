package ui

// resize_preview_test.go covers what the screen shows while a pane divider is
// being dragged: the panes keep their chrome (title, close button, divider) and
// a dim "resizing…" hint, but their contents are not laid out — that is what
// made a drag feel slow — and they come back when the button is released.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// resizeDragModel is a model with an editor that is easy to recognize on screen.
func resizeDragModel(t *testing.T) Model {
	t.Helper()
	m := sized("SELECT_RESIZE_MARKER;")
	m.welcome = false
	return m
}

func TestDividerPressAloneKeepsTheContents(t *testing.T) {
	m := resizeDragModel(t)
	if view := stripANSI(m.View()); !strings.Contains(view, "SELECT_RESIZE_MARKER") {
		t.Fatalf("setup: the editor text must be on screen:\n%s", view)
	}
	r := m.paneRects()
	m = send(m, mousePress(r.dividerV.X, r.dividerV.Y+1))
	if m.layout.dragging != dividerVertical {
		t.Fatal("the press must arm the vertical divider drag")
	}
	if m.layout.resizing() {
		t.Fatal("a press without motion must not hide the contents")
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "SELECT_RESIZE_MARKER") {
		t.Fatal("a click on the divider must not blank the panes")
	}
}

func TestDividerDragDrawsChromeOnlyAndReleaseRestores(t *testing.T) {
	m := resizeDragModel(t)
	r := m.paneRects()
	m = send(m, mousePress(r.dividerV.X, r.dividerV.Y+1))
	m = send(m, tea.MouseMsg{
		X: r.dividerV.X + 3, Y: r.dividerV.Y + 1,
		Action: tea.MouseActionMotion,
	})
	if !m.layout.resizing() {
		t.Fatalf("a moved divider drag must switch to the cheap preview (dragging=%v moved=%v)",
			m.layout.dragging, m.layout.dragMoved)
	}
	during := stripANSI(m.View())
	if !strings.Contains(during, "resizing") {
		t.Fatalf("the preview must say what is happening:\n%s", during)
	}
	if strings.Contains(during, "SELECT_RESIZE_MARKER") {
		t.Fatalf("the editor contents must not be laid out during the drag:\n%s", during)
	}
	// The chrome stays: pane titles and the divider itself are still drawn.
	for _, want := range []string{"SQL Editor", "Objects"} {
		if !strings.Contains(during, want) {
			t.Fatalf("the preview is missing the pane chrome %q:\n%s", want, during)
		}
	}

	m = send(m, mouseRelease(r.dividerV.X+3, r.dividerV.Y+1))
	if m.layout.resizing() || m.layout.dragging != dividerNone {
		t.Fatal("the release must end the resizing preview")
	}
	after := stripANSI(m.View())
	if !strings.Contains(after, "SELECT_RESIZE_MARKER") {
		t.Fatalf("the contents must come back after the release:\n%s", after)
	}
	if strings.Contains(after, "resizing") {
		t.Fatalf("the hint must be gone after the release:\n%s", after)
	}
}

func TestDividerDragSkipsLargeResultLayout(t *testing.T) {
	// A wide, long result is the expensive part of a frame. With the preview
	// active its text must not reach the screen at all.
	m := resultModel(t, 8, 12, maxColWidth, 40)
	m.welcome = false
	r := m.paneRects()
	m = send(m, mousePress(r.dividerH.X+2, r.dividerH.Y))
	m = send(m, tea.MouseMsg{
		X: r.dividerH.X + 2, Y: r.dividerH.Y - 3,
		Action: tea.MouseActionMotion,
	})
	if !m.layout.resizing() {
		t.Fatal("the horizontal divider drag must enter the preview")
	}
	during := stripANSI(m.View())
	if strings.Contains(during, "aaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("the result grid must not be laid out during the drag:\n%s", during)
	}
}
