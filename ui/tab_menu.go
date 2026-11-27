package ui

// tab_menu.go implements the tab bar's context menu: right-clicking a tab (or
// pressing Shift+F10 while the editor has the focus) offers the operations
// that apply to that tab — renaming it, duplicating it and closing it.
//
// The menu follows the shape of the object browser's and the result grid's
// context menus: the geometry is one pure function shared by rendering and hit
// testing, and the actions are dispatched from one place, so the mouse and the
// keyboard cannot drift apart.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// tabMenuAction is one action of the tab context menu.
type tabMenuAction int

// The tab context menu actions.
const (
	tabActNone tabMenuAction = iota
	tabActRename
	tabActDuplicate
	tabActClose
	tabActCloseOthers
	tabActCloseLeft
	tabActCloseRight
	tabActCloseAll
)

// tabMenuState is the open tab context menu.
type tabMenuState struct {
	open    bool
	x, y    int // screen coordinate of the click that opened it
	tab     int // index of the tab the menu acts on
	actions []tabMenuAction
	sel     int
}

// openTabMenu opens the context menu for the tab at the given index.
func (m *Model) openTabMenu(x, y int, index int) {
	if index < 0 || index >= len(m.tabs) {
		return
	}
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeFind()
	m.popup.close()
	m.tabMenu = tabMenuState{
		open:    true,
		x:       x,
		y:       y,
		tab:     index,
		actions: m.tabMenuActions(index),
	}
}

// tabMenuActions returns the actions offered for the tab at index. The bulk
// closes appear only when they have something to close: an entry that cannot do
// anything is noise, not a shortcut.
func (m Model) tabMenuActions(index int) []tabMenuAction {
	actions := []tabMenuAction{tabActRename, tabActDuplicate, tabActClose}
	if len(m.tabs) <= 1 {
		return actions
	}
	actions = append(actions, tabActCloseOthers)
	if index > 0 {
		actions = append(actions, tabActCloseLeft)
	}
	if index < len(m.tabs)-1 {
		actions = append(actions, tabActCloseRight)
	}
	return append(actions, tabActCloseAll)
}

// openTabMenuForActiveTab opens the context menu of the active tab. This is
// the keyboard entry point (Shift+F10): the tab bar is not a focus target of
// its own, so the shortcut lives with the editor, whose column the tabs
// belong to.
func (m *Model) openTabMenuForActiveTab() {
	if len(m.tabs) == 0 {
		return
	}
	rect := m.paneRects().tabbar
	m.openTabMenu(rect.X+1, rect.Y, m.active)
}

// closeTabMenu closes the context menu.
func (m *Model) closeTabMenu() { m.tabMenu.open = false }

// tabMenuLabelKey maps an action to its localized label.
func tabMenuLabelKey(a tabMenuAction) i18n.Key {
	switch a {
	case tabActRename:
		return i18n.KeyTabRename
	case tabActDuplicate:
		return i18n.KeyTabDuplicate
	case tabActClose:
		return i18n.KeyTabClose
	case tabActCloseOthers:
		return i18n.KeyTabCloseOthers
	case tabActCloseLeft:
		return i18n.KeyTabCloseLeft
	case tabActCloseRight:
		return i18n.KeyTabCloseRight
	case tabActCloseAll:
		return i18n.KeyTabCloseAll
	}
	return i18n.KeyTabRename
}

// ---- Geometry ----

// tabMenuInnerWidth is the content width of the context menu.
func (m Model) tabMenuInnerWidth() int {
	w := 0
	for _, a := range m.tabMenu.actions {
		w = max(w, lipgloss.Width(m.t(tabMenuLabelKey(a))))
	}
	return max(w+2, 12)
}

// tabMenuRect is the on-screen rectangle of the open context menu.
func (m Model) tabMenuRect() (Rect, bool) {
	if !m.tabMenu.open || len(m.tabMenu.actions) == 0 {
		return Rect{}, false
	}
	w := m.tabMenuInnerWidth() + 2
	h := len(m.tabMenu.actions) + 2
	x := clamp(m.tabMenu.x, 0, max(0, m.width-w))
	y := m.tabMenu.y + 1
	if y+h > m.height {
		y = m.tabMenu.y - h
	}
	y = clamp(y, 0, max(0, m.height-h))
	return Rect{X: x, Y: y, W: w, H: h}, true
}

// tabMenuItemAt reports whether a coordinate hits an item of the open context
// menu.
func (m Model) tabMenuItemAt(x, y int) (int, bool) {
	r, ok := m.tabMenuRect()
	if !ok || !r.Contains(x, y) {
		return 0, false
	}
	idx := y - r.Y - 1
	if idx < 0 || idx >= len(m.tabMenu.actions) {
		return 0, false
	}
	return idx, true
}

// ---- Interaction ----

// handleTabMenuKey handles a key press while the context menu is open and
// reports whether it consumed the key.
func (m Model) handleTabMenuKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	switch key {
	case "esc", "shift+f10":
		m.closeTabMenu()
		return m, nil, true
	case "up":
		n := len(m.tabMenu.actions)
		m.tabMenu.sel = (m.tabMenu.sel - 1 + n) % n
		return m, nil, true
	case "down":
		n := len(m.tabMenu.actions)
		m.tabMenu.sel = (m.tabMenu.sel + 1) % n
		return m, nil, true
	case "home":
		m.tabMenu.sel = 0
		return m, nil, true
	case "end":
		m.tabMenu.sel = len(m.tabMenu.actions) - 1
		return m, nil, true
	case "enter", "space":
		if m.tabMenu.sel < 0 || m.tabMenu.sel >= len(m.tabMenu.actions) {
			m.closeTabMenu()
			return m, nil, true
		}
		return m, m.runTabMenuAction(m.tabMenu.actions[m.tabMenu.sel]), true
	}
	// Printable input dismisses the menu without leaking into the editor.
	m.closeTabMenu()
	return m, nil, true
}

// runTabMenuAction performs a context menu action.
func (m *Model) runTabMenuAction(a tabMenuAction) tea.Cmd {
	tab := m.tabMenu.tab
	m.closeTabMenu()
	switch a {
	case tabActRename:
		m.openTabRename(tab)
		return nil
	case tabActDuplicate:
		if tab < 0 || tab >= len(m.tabs) {
			return nil
		}
		m.duplicateTabAt(tab)
		return m.setMessage(m.t(i18n.KeyTabDuplicated))
	case tabActClose:
		if tab < 0 || tab >= len(m.tabs) {
			return nil
		}
		if !m.closeTabAt(tab) {
			return nil // it asked first; the dialog reports the state
		}
		return m.setMessage(m.t(i18n.KeyTabClosed))
	case tabActCloseOthers:
		if tab < 0 || tab >= len(m.tabs) || len(m.tabs) == 1 {
			return nil
		}
		if !m.closeTabsExcept(tab) {
			return nil
		}
		return m.setMessage(m.t(i18n.KeyTabOthersClosed))
	case tabActCloseLeft:
		if tab <= 0 || tab >= len(m.tabs) {
			return nil
		}
		if !m.closeTabsToLeft(tab) {
			return nil
		}
		return m.setMessage(m.t(i18n.KeyTabLeftClosed))
	case tabActCloseRight:
		if tab < 0 || tab >= len(m.tabs)-1 {
			return nil
		}
		if !m.closeTabsToRight(tab) {
			return nil
		}
		return m.setMessage(m.t(i18n.KeyTabRightClosed))
	case tabActCloseAll:
		if len(m.tabs) <= 1 {
			return nil
		}
		if !m.closeAllTabs() {
			return nil
		}
		return m.setMessage(m.t(i18n.KeyTabAllClosed))
	}
	return nil
}

// ---- Rendering ----

// renderTabMenu renders the context menu with a border, on top of the panes.
func (m Model) renderTabMenu() (Rect, []string, bool) {
	r, ok := m.tabMenuRect()
	if !ok || r.H < 3 || r.W < 4 {
		return Rect{}, nil, false
	}
	inner := r.W - 2
	border := m.theme.menuBorder

	out := []string{border.Render("┌" + strings.Repeat("─", inner) + "┐")}
	for i, a := range m.tabMenu.actions {
		style := m.theme.menuBox
		if i == m.tabMenu.sel {
			style = m.theme.menuItemSel
		}
		label := m.t(tabMenuLabelKey(a))
		pad := max(inner-1-lipgloss.Width(label), 1)
		line := style.Render(" " + label + strings.Repeat(" ", pad))
		out = append(out, border.Render("│")+line+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return r, out, true
}
