package ui

// tabbar.go renders the editor tab strip and computes its geometry. The strip
// sits directly above the editor, inside the editor column, so the object
// browser keeps the full height of the window. Like the menu bar and the
// toolbar, the slots are computed once and shared by rendering and hit testing.

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// Tab slot kinds.
const (
	slotTab = iota
	slotNew
)

// tabSlot is one slot of the tab bar: a tab or the "new tab" button.
type tabSlot struct {
	kind  int
	index int // tab index for slotTab
	x, w  int
}

// maxTabNameLen is the longest name a tab takes from a statement: long enough
// for a readable SELECT, short enough to fit in the tab bar.
const maxTabNameLen = 32

// tabLabel returns the text shown on a tab.
//
// The name is resolved in a fixed order: a name the user gave the tab wins,
// then the name of the file it holds, then the automatically derived name
// (the object it shows, the kind of statement it holds), then the time it was
// opened. A wall-clock title is more useful than a generic "untitled": it
// tells several query tabs apart at a glance.
func (m Model) tabLabel(i int) string {
	t := m.tabs[i]
	if t.userName != "" {
		// A name the user typed can be as long as they want; the tab bar
		// shows it shortened, the rename dialog shows it whole.
		return trimTabName(stripControl(t.userName))
	}
	return m.tabAutoLabel(i)
}

// tabAutoLabel returns the name a tab shows without a name of the user's own:
// the name of the file it holds, the automatically derived name, or the time
// it was opened.
//
// The derived names come from the catalog or from the statement of the tab,
// so they may carry control characters (a table name may hold them). The tab
// bar draws the name as plain text: the characters are dropped.
func (m Model) tabAutoLabel(i int) string {
	t := m.tabs[i]
	if name := t.fileName; name != "" {
		return stripControl(filepath.Base(name))
	}
	if t.name != "" {
		return stripControl(t.name)
	}
	if t.welcome {
		// Nothing to derive a name from: the welcome page is named by what
		// it is, because a timestamp would say nothing about a page that
		// looks the same every time (and it would not say that this tab is
		// not an editor).
		return m.t(i18n.KeyWelcomeTab)
	}
	if !t.createdAt.IsZero() {
		return t.createdAt.Format("15:04:05")
	}
	return m.t(i18n.KeyHeaderUnnamed)
}

// tabNameFromSQL names a tab after the statement it holds: the first
// non-empty line, shortened to what the tab bar can show.
func tabNameFromSQL(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return trimTabName(line)
	}
	return ""
}

// trimTabName shortens a derived name to what a tab can show. The ellipsis
// says that the name was cut, so a truncated name is never mistaken for the
// whole statement.
func trimTabName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxTabNameLen {
		return name
	}
	return string(runes[:maxTabNameLen-1]) + "…"
}

// tabNameWithKind names a tab after an object and the kind of statement it
// holds: "public.users [SELECT]". The kind is in brackets because it is a
// detail, not the name of the tab.
func (m Model) tabNameWithKind(object, kind string) string {
	if object == "" {
		return kind
	}
	return object + " [" + kind + "]"
}

// tabSlots lays the tabs out along the tab bar row, starting at the first
// visible tab and stopping when the row is full.
func (m Model) tabSlots(rect Rect) []tabSlot {
	out := make([]tabSlot, 0, len(m.tabs)+1)
	x := rect.X
	for i := clamp(m.tabScroll, 0, max(0, len(m.tabs)-1)); i < len(m.tabs); i++ {
		cell := " " + m.tabLabel(i) + " " + m.icons().tabClose + " "
		w := lipgloss.Width(cell)
		if x+w > rect.X+rect.W {
			break
		}
		out = append(out, tabSlot{kind: slotTab, index: i, x: x, w: w})
		x += w
	}
	// The "new tab" button takes what is left, if anything fits.
	w := lipgloss.Width(" " + m.icons().tabNew + " ")
	if x+w <= rect.X+rect.W {
		out = append(out, tabSlot{kind: slotNew, x: x, w: w})
	}
	return out
}

// tabItemAt returns the slot under a screen coordinate, plus the column offset
// inside it (so a click on the close glyph can be told apart).
func (m Model) tabItemAt(x, y int) (tabSlot, int, bool) {
	r := m.paneRects()
	if !r.tabbar.Contains(x, y) {
		return tabSlot{}, 0, false
	}
	for _, s := range m.tabSlots(r.tabbar) {
		if x >= s.x && x < s.x+s.w {
			return s, x - s.x, true
		}
	}
	return tabSlot{}, 0, false
}

// handleTabClick acts on a click in the tab bar: the close glyph closes the
// tab, anywhere else selects it, and the "+" slot opens a new one.
func (m *Model) handleTabClick(slot tabSlot, off int) {
	switch slot.kind {
	case slotNew:
		m.newTab()
	case slotTab:
		if off >= slot.w-2 { // the close glyph is the tab's last-but-one column
			m.closeTabAt(slot.index)
			return
		}
		m.switchTab(slot.index)
	}
}

// ensureTabVisible scrolls the tab bar so that the active tab is on screen.
func (m *Model) ensureTabVisible() {
	rect := m.paneRects().tabbar
	if m.tabScroll > m.active {
		m.tabScroll = m.active
	}
	for m.tabScroll < m.active {
		visible := -1
		for _, s := range m.tabSlots(rect) {
			if s.kind == slotTab {
				visible = s.index
			}
		}
		if visible >= m.active {
			break
		}
		m.tabScroll++
	}
}

// renderTabBar renders the tab strip in the editor pane's first row.
func (m Model) renderTabBar(rect Rect) []string {
	if rect.W < 1 || rect.H < 1 {
		return nil
	}
	canvas := []string{m.theme.tabBar.Render(strings.Repeat(" ", rect.W))}
	for _, s := range m.tabSlots(rect) {
		switch s.kind {
		case slotTab:
			style := m.theme.tab
			// The active tab and the tab under the pointer share the active
			// look: hover only draws (round 7 §1), it never switches tabs, so
			// the two may differ — and when they agree the active style is
			// exactly what is drawn anyway.
			if s.index == m.active || s.index == m.tabHover {
				style = m.theme.tabActive
			}
			cell := style.Render(" " + m.tabLabel(s.index) + " " + m.icons().tabClose + " ")
			blit(canvas, Rect{X: s.x - rect.X, Y: 0, W: s.w, H: 1}, []string{cell})
		case slotNew:
			cell := m.theme.tabNew.Render(" " + m.icons().tabNew + " ")
			blit(canvas, Rect{X: s.x - rect.X, Y: 0, W: s.w, H: 1}, []string{cell})
		}
	}
	return canvas
}
