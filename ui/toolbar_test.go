package ui

import (
	"strings"
	"testing"
)

// toolSlotAt finds the slot of a button by its (localized) label.
func toolSlotAt(m Model, label string) (toolSlot, bool) {
	for _, s := range m.toolSlots(m.paneRects().toolbar) {
		b := toolButtons[s.index]
		if !b.sep && m.t(b.labelKey) == label {
			return s, true
		}
	}
	return toolSlot{}, false
}

func TestToolbarShowsButtons(t *testing.T) {
	m := sized("")
	view := stripANSI(m.View())
	// The default is the icon set: buttons show their icon and keep the name
	// out of the bar (it appears in the status bar when the pointer asks).
	ic := m.icons()
	if ic.newTab == "" {
		t.Fatal("the icon set must have a button icon")
	}
	for _, want := range []string{ic.newTab, ic.save, ic.run, ic.format, ic.settings, ic.help} {
		if !strings.Contains(view, want) {
			t.Fatalf("the toolbar is missing a button (icon %q):\n%s", want, view)
		}
	}
	if strings.Contains(view, "New Tab") {
		t.Fatalf("icon buttons must not print their names:\n%s", view)
	}
	// The toolbar sits between the menu bar and the tab bar.
	r := m.paneRects()
	if r.toolbar.Y != r.menubar.Y+r.menubar.H || r.tabbar.Y != r.toolbar.Y+r.toolbar.H {
		t.Fatalf("toolbar = %+v, tabbar = %+v", r.toolbar, r.tabbar)
	}
}

func TestToolbarButtonClickRunsAction(t *testing.T) {
	cases := []struct {
		label string
		check func(Model) bool
	}{
		{"New Tab", func(m Model) bool { return len(m.tabs) == 2 }},
		{"Run", func(m Model) bool { return strings.Contains(m.status.message, "No connection") }},
		{"Format", func(m Model) bool { return m.buf.Text() == "SELECT\n  1" }},
		{"Settings", func(m Model) bool { return m.dlg.open }},
		{"Help", func(m Model) bool { return m.keysDlg.open }},
	}
	for _, c := range cases {
		text := ""
		if c.label == "Format" {
			text = "select 1"
		}
		m := sized(text)
		slot, ok := toolSlotAt(m, c.label)
		if !ok {
			t.Fatalf("no %q button on the toolbar", c.label)
		}
		m = send(m, mousePress(slot.x+1, m.paneRects().toolbar.Y))
		if !c.check(m) {
			t.Fatalf("clicking %q did not work: status=%q tabs=%d dialog=%v",
				c.label, m.status.message, len(m.tabs), m.dlg.open)
		}
	}
}

func TestToolbarSaveButtonSavesTheActiveTab(t *testing.T) {
	m := sized("SELECT 1")
	m.fileName = t.TempDir() + "/q.sql"
	slot, ok := toolSlotAt(m, "Save")
	if !ok {
		t.Fatal("no Save button on the toolbar")
	}
	_, cmd := sendCmd(m, mousePress(slot.x+1, m.paneRects().toolbar.Y))
	if cmd == nil {
		t.Fatal("the Save button must return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("saving must report the result in the status bar")
	}
}

func TestToolbarFallsBackToLabels(t *testing.T) {
	m := sized("")
	m.settings.Icons = false // no Nerd Font: the labels come back
	view := stripANSI(m.View())
	for _, want := range []string{"New Tab", "Save", "Run", "Format", "Settings", "Help"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the plain toolbar is missing the %q button:\n%s", want, view)
		}
	}
	if m.icons().newTab != "" {
		t.Fatal("the plain set must not have toolbar icons")
	}
}

func TestToolbarHoverHighlights(t *testing.T) {
	m := sized("")
	r := m.paneRects()
	slot, ok := toolSlotAt(m, "Run")
	if !ok {
		t.Fatal("no Run button on the toolbar")
	}
	m = send(m, mouseMotion(slot.x+1, r.toolbar.Y))
	if m.toolHover != slot.index {
		t.Fatalf("toolHover = %d, want %d", m.toolHover, slot.index)
	}
	// The hovered button names itself in the status bar.
	if !strings.Contains(stripANSI(m.View()), "Run") {
		t.Fatalf("hovering a button must show its name:\n%s", m.View())
	}
	// Moving away clears the highlight.
	m = send(m, mouseMotion(m.paneRects().area.X+5, m.paneRects().area.Y+2))
	if m.toolHover != -1 {
		t.Fatalf("toolHover = %d, want -1 away from the toolbar", m.toolHover)
	}
}
