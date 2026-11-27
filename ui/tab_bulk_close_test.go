package ui

// tab_bulk_close_test.go covers the bulk entries of the tab context menu: close
// others, close to the left, close to the right and close all. They are offered
// only when they have something to close, and they act on the tab the menu was
// opened for, not on whatever tab happens to be active.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// threeTabs returns a model with three named tabs and the last one active.
func threeTabs(t *testing.T) Model {
	t.Helper()
	m := sized("")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(m.tabs) != 3 || m.active != 2 {
		t.Fatalf("setup: tabs = %d, active = %d", len(m.tabs), m.active)
	}
	for i, name := range []string{"one", "two", "three"} {
		m.tabs[i].name = name
	}
	return m
}

// clickTabMenuActionRun opens the menu on tab index and picks action from it.
// The status line is set synchronously by the action, so it can be read from
// the returned model without draining the timeout tick.
func clickTabMenuActionRun(m Model, index int, action tabMenuAction) Model {
	m = rightClickTab(m, index)
	next, _ := clickTabMenuAction(m, action)
	return next
}

func TestTabMenuOffersBulkClosesOnlyWhenUseful(t *testing.T) {
	m := sized("")
	m = rightClickTab(m, 0)
	if got, want := m.tabMenu.actions, []tabMenuAction{tabActRename, tabActDuplicate, tabActClose}; !equalActions(got, want) {
		t.Fatalf("one tab: actions = %v, want %v", got, want)
	}

	m = threeTabs(t)
	m = rightClickTab(m, 2) // the last tab: nothing to its right, nothing... but two to its left
	for _, a := range []tabMenuAction{tabActCloseOthers, tabActCloseLeft, tabActCloseAll} {
		if !hasTabAction(m.tabMenu.actions, a) {
			t.Fatalf("the last tab must offer %v: %v", a, m.tabMenu.actions)
		}
	}
	if hasTabAction(m.tabMenu.actions, tabActCloseRight) {
		t.Fatalf("the last tab must not offer Close to the right: %v", m.tabMenu.actions)
	}

	m = rightClickTab(m, 1) // a middle tab: left and right both exist
	for _, a := range []tabMenuAction{tabActCloseOthers, tabActCloseLeft, tabActCloseRight, tabActCloseAll} {
		if !hasTabAction(m.tabMenu.actions, a) {
			t.Fatalf("a middle tab must offer %v: %v", a, m.tabMenu.actions)
		}
	}
}

func TestTabMenuCloseOthersKeepsTheClickedTab(t *testing.T) {
	m := clickTabMenuActionRun(threeTabs(t), 1, tabActCloseOthers)
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, want only the clicked one", len(m.tabs))
	}
	if got := m.tabs[0].name; got != "two" {
		t.Fatalf("kept tab = %q, want the clicked one", got)
	}
	if m.active != 0 {
		t.Fatalf("active = %d, want the kept tab", m.active)
	}
	if !strings.Contains(m.status.message, "Other tabs closed") {
		t.Fatalf("status = %q, want the close confirmation", m.status.message)
	}
}

func TestTabMenuCloseToTheRightKeepsTheTargetAndTheLeftOnes(t *testing.T) {
	m := clickTabMenuActionRun(threeTabs(t), 1, tabActCloseRight)
	if len(m.tabs) != 2 || m.tabs[0].name != "one" || m.tabs[1].name != "two" {
		t.Fatalf("tabs = %v, want one and two", tabNames(m))
	}
	// The active tab (three) was on the closed side; the target takes over.
	if m.active != 1 {
		t.Fatalf("active = %d, want the target tab", m.active)
	}
}

func TestTabMenuCloseToTheLeftKeepsTheTargetAndTheRightOnes(t *testing.T) {
	m := clickTabMenuActionRun(threeTabs(t), 1, tabActCloseLeft)
	if len(m.tabs) != 2 || m.tabs[0].name != "two" || m.tabs[1].name != "three" {
		t.Fatalf("tabs = %v, want two and three", tabNames(m))
	}
	// The active tab (three) survived the move to the left.
	if m.active != 1 {
		t.Fatalf("active = %d, want the previously active tab", m.active)
	}
}

func TestTabMenuCloseAllLeavesAWelcomeTab(t *testing.T) {
	m := clickTabMenuActionRun(threeTabs(t), 0, tabActCloseAll)
	if len(m.tabs) != 1 {
		t.Fatalf("tabs = %d, want the fresh welcome tab", len(m.tabs))
	}
	if !m.welcome {
		t.Fatal("closing every tab must leave the welcome page")
	}
	if m.active != 0 {
		t.Fatalf("active = %d, want the only tab", m.active)
	}
}

func TestTabMenuBulkCloseKeepsTheOtherSessionsIntact(t *testing.T) {
	// Closing neighbours must not mutate the session the user keeps: its text
	// and its tab identity survive.
	m := threeTabs(t)
	m.tabs[1].buf.SetText("SELECT 42")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlT}) // active moves to a new tab
	m = rightClickTab(m, 1)
	kept := m.tabs[1]
	next, _ := clickTabMenuAction(m, tabActCloseOthers)
	m = next
	if len(m.tabs) != 1 || m.tabs[0] != kept {
		t.Fatal("the kept session must be the same tab object")
	}
	if got := m.tabs[0].buf.Text(); got != "SELECT 42" {
		t.Fatalf("kept text = %q", got)
	}
}

func hasTabAction(actions []tabMenuAction, want tabMenuAction) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

func equalActions(a, b []tabMenuAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tabNames(m Model) []string {
	out := make([]string, len(m.tabs))
	for i, s := range m.tabs {
		out[i] = s.name
	}
	return out
}
