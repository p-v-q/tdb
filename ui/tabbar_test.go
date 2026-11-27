package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestStartsWithOneTab(t *testing.T) {
	m := sized("SELECT 1")
	// The fixture starts on the welcome page, whose tab is named for what it
	// is; this test is about the opening time an ordinary tab shows.
	m.welcomeToEditor()
	m.tabs[0].createdAt = time.Date(2026, 10, 20, 15, 4, 5, 0, time.UTC)
	if len(m.tabs) != 1 || m.active != 0 {
		t.Fatalf("tabs = %d, active = %d, want 1/0", len(m.tabs), m.active)
	}
	if got := m.tabLabel(0); got != "15:04:05" {
		t.Fatalf("tab label = %q, want the tab's opening time", got)
	}
}

func TestNewTabGetsItsOwnTimeTitle(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	label := m.tabLabel(1)
	if len(label) != len("15:04:05") || label[2] != ':' || label[5] != ':' {
		t.Fatalf("new tab label = %q, want HH:MM:SS", label)
	}
}

func TestTabShowsFileName(t *testing.T) {
	m := sized("SELECT 1")
	m.tabs[0].fileName = "queries/report.sql"
	view := m.View()
	if !strings.Contains(view, "report.sql") {
		t.Fatalf("the tab bar must show the file name:\n%s", view)
	}
	// The tab shows the base name, the title bar keeps the full one.
	if got := m.tabLabel(0); got != "report.sql" {
		t.Fatalf("tab label = %q, want the base name", got)
	}
	if !strings.Contains(m.sessionInfo(), "queries/report.sql") {
		t.Fatalf("the title bar must show the full file name: %q", m.sessionInfo())
	}
}

func TestCtrlTCreatesAndSwitchesTab(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(m.tabs) != 2 || m.active != 1 {
		t.Fatalf("tabs = %d, active = %d, want 2/1", len(m.tabs), m.active)
	}
	if m.buf.Text() != "" {
		t.Fatalf("a new tab must start empty, got %q", m.buf.Text())
	}
	// Each tab keeps its own buffer: typing here must not touch the first one.
	m = typeText(m, "abc")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.active != 0 {
		t.Fatalf("active = %d, want 0", m.active)
	}
	if got := m.buf.Text(); got != "SELECT 1" {
		t.Fatalf("the first tab's text = %q", got)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if got := m.buf.Text(); got != "abc" {
		t.Fatalf("the second tab's text = %q", got)
	}
}

func TestTabSwitchingWrapsAround(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if m.active != 0 {
		t.Fatalf("Ctrl+PgDown on the last tab must wrap to 0, active = %d", m.active)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.active != 2 {
		t.Fatalf("Ctrl+PgUp on the first tab must wrap to the last, active = %d", m.active)
	}
}

func TestTabClickSwitchesClosesAndOpens(t *testing.T) {
	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m.tabs[1].fileName = "second.sql"
	r := m.paneRects()

	slots := m.tabSlots(r.tabbar)
	if len(slots) < 3 {
		t.Fatalf("want two tabs and a + slot, got %+v", slots)
	}
	// Click the first tab, away from its close glyph.
	m = send(m, mousePress(slots[0].x+1, r.tabbar.Y))
	if m.active != 0 {
		t.Fatalf("clicking a tab must select it, active = %d", m.active)
	}
	// Click the close glyph of the second tab.
	second := slots[1]
	m = send(m, mousePress(second.x+second.w-2, r.tabbar.Y))
	if len(m.tabs) != 1 {
		t.Fatalf("clicking the close glyph must close the tab, tabs = %d", len(m.tabs))
	}
	// Click the "+" slot.
	slots = m.tabSlots(m.paneRects().tabbar)
	last := slots[len(slots)-1]
	if last.kind != slotNew {
		t.Fatalf("the last slot must be the + button, got %+v", last)
	}
	m = send(m, mousePress(last.x+1, m.paneRects().tabbar.Y))
	if len(m.tabs) != 2 {
		t.Fatalf("the + slot must open a tab, tabs = %d", len(m.tabs))
	}
}

func TestMiddleClickClosesTab(t *testing.T) {
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	slots := m.tabSlots(m.paneRects().tabbar)
	m = send(m, tea.MouseMsg{
		X: slots[0].x + 1, Y: m.paneRects().tabbar.Y,
		Action: tea.MouseActionPress, Button: tea.MouseButtonMiddle,
	})
	if len(m.tabs) != 1 {
		t.Fatalf("middle click must close the tab, tabs = %d", len(m.tabs))
	}
}

func TestTabBarScrollsToKeepActiveTabVisible(t *testing.T) {
	m := sized("")
	// Open more tabs than fit on a narrow tab bar. The browser takes the
	// minimum width so the editor (and its tabs) still has room.
	m.layout.objectsWidth = 12
	m.width, m.height = 40, 20
	for i := 0; i < 6; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	}
	m.ensureTabVisible()
	if m.tabScroll > m.active {
		t.Fatalf("tabScroll = %d, active = %d: the active tab must be visible", m.tabScroll, m.active)
	}
	found := false
	for _, s := range m.tabSlots(m.paneRects().tabbar) {
		if s.kind == slotTab && s.index == m.active {
			found = true
		}
	}
	if !found {
		t.Fatalf("the active tab (index %d) is not on screen, slots = %+v", m.active, m.tabSlots(m.paneRects().tabbar))
	}
}

// TestTabBarHoverHighlightsTabWithoutSwitching pins the pointer semantics of
// the tab strip (round 7 §1): motion highlights the tab under the pointer
// without switching to it, leaving the strip clears the highlight, and a
// click still switches.
func TestTabBarHoverHighlightsTabWithoutSwitching(t *testing.T) {
	// Lip Gloss emits no ANSI without a TTY: force colors so the highlight
	// style is really in the rendered cell, then restore the plain profile.
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	m := sized("SELECT 1")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT}) // two tabs; the new one is active
	// Back to the first tab: the hovered tab below must be an inactive one,
	// or the highlight would be indistinguishable from the active style.
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlPgUp})
	if m.active != 0 {
		t.Fatalf("active = %d, want the first tab after Ctrl+PgUp", m.active)
	}
	// Both tabs would auto-name themselves after their opening time (usually
	// the same second), which would make the two cells identical: give them
	// names of their own so the styles can be told apart.
	m.tabs[0].userName = "first"
	m.tabs[1].userName = "second"
	r := m.paneRects()
	slots := m.tabSlots(r.tabbar)
	if len(slots) < 3 {
		t.Fatalf("slots = %+v, want two tabs and a + slot", slots)
	}
	target := slots[1]
	if target.kind != slotTab {
		t.Fatalf("the second slot must be a tab, got %+v", target)
	}
	plain := m.renderTabBar(r.tabbar)
	wantCell := m.theme.tabActive.Render(" " + m.tabLabel(target.index) + " " + m.icons().tabClose + " ")

	m = send(m, mouseMotion(target.x+1, r.tabbar.Y))
	if m.tabHover != target.index {
		t.Fatalf("tabHover = %d, want %d", m.tabHover, target.index)
	}
	if m.active != 0 {
		t.Fatalf("active = %d, hovering must not switch tabs", m.active)
	}
	lines := m.renderTabBar(r.tabbar)
	if !strings.Contains(lines[0], wantCell) {
		t.Fatalf("the hovered tab must be drawn with the active style:\n%s", lines[0])
	}
	if strings.Contains(plain[0], wantCell) {
		t.Fatalf("without a hover the tab must keep the inactive style:\n%s", plain[0])
	}

	// Leaving the strip clears the highlight only.
	m = send(m, mouseMotion(r.tabbar.X+5, r.tabbar.Y+2)) // a row of the editor
	if m.tabHover != -1 {
		t.Fatalf("tabHover = %d, want -1 away from the strip", m.tabHover)
	}
	if m.active != 0 {
		t.Fatalf("active = %d, want it unchanged", m.active)
	}

	// A click still switches tabs: hovering changed nothing about it.
	m = send(m, mousePress(target.x+1, r.tabbar.Y))
	if m.active != target.index {
		t.Fatalf("active = %d, want %d after the click", m.active, target.index)
	}
}
