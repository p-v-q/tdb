package ui

// keybindings_dialog.go implements the Key Bindings overlay: a scrollable,
// read-only list of the shortcuts the application understands.
//
// The menu rows are generated from menuDefs on every draw instead of being
// copied into a second table, so the help can never advertise a binding the
// command table does not have. The three pane sections cover keys that the
// panes handle directly and that therefore never appear in the menu bar.
//
// Like the other dialogs it is modal: it takes the keyboard first, swallows
// every key it does not use, and is drawn above the panes.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// keybindingsDialog is the state of the modal Key Bindings list.
type keybindingsDialog struct {
	open   bool
	scroll int // first visible content line
}

// keybindingLine is one row of the overlay: a group heading or a label/shortcut
// pair.
type keybindingLine struct {
	group bool
	label string
	keys  string
}

// keybindingRow is one binding of a pane that is not a menu item.
type keybindingRow struct {
	labelKey i18n.Key
	keys     string
}

// keybindingGroup is a section for keys the panes own themselves.
type keybindingGroup struct {
	titleKey i18n.Key
	rows     []keybindingRow
}

// paneKeyBindings lists the keys handled directly by the panes. They live here,
// not in menuDefs, because they depend on the focused pane and are not commands
// the menu can run.
var paneKeyBindings = []keybindingGroup{
	{i18n.KeyKeysObjects, []keybindingRow{
		{i18n.KeyKeysMove, "↑↓"},
		{i18n.KeyKeysFold, "←→"},
		{i18n.KeyKeysOpen, "Enter"},
		{i18n.KeyKeysFilter, "/"},
		// The terminals send Shift+F10 as F13, and the application takes
		// both names for the same shortcut.
		{i18n.KeyKeysMenu, "⇧F10/F13"},
	}},
	{i18n.KeyKeysResults, []keybindingRow{
		{i18n.KeyKeysMove, "↑↓←→"},
		{i18n.KeyKeysSort, "s"},
		{i18n.KeyKeysFilter, "/"},
		{i18n.KeyKeysCopyCell, "y"},
		{i18n.KeyKeysCopyRow, displayShortcut("ctrl+c")},
		{i18n.KeyKeysResultSets, "[ ]"},
		// Ctrl+S applies the pending cell changes; Ctrl+Enter is the
		// fallback for the terminals whose tty swallows 0x13 (the
		// software flow control of IXON).
		{i18n.KeyResultApplyChanges, displayShortcut("ctrl+s") + " / " + displayShortcut("ctrl+enter")},
		{i18n.KeyKeysBack, "Esc"},
	}},
	{i18n.KeyKeysEditor, []keybindingRow{
		{i18n.KeyKeysComplete, displayShortcut("ctrl+space")},
		{i18n.KeyKeysAccept, "Tab/Enter"},
		// The tab bar belongs to the editor's column, so its menu is listed
		// here: the tab bar is not a focus target of its own.
		{i18n.KeyKeysMenu, "⇧F10/F13"},
	}},
}

// openKeybindings opens the overlay at the top of the list, dismissing the
// overlays that could otherwise share the keyboard with it.
func (m *Model) openKeybindings() {
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeSettings()
	m.closeConnDialog()
	m.closeExportDialog()
	m.closeHistoryDialog()
	m.closeFind()
	m.popup.close()
	m.keysDlg = keybindingsDialog{open: true}
}

// closeKeybindings dismisses the overlay.
func (m *Model) closeKeybindings() { m.keysDlg.open = false }

// keybindingsLines builds the displayed rows: every menu definition becomes a
// heading, followed by the items that actually have a global shortcut. Items
// that need transaction support are hidden on a connection that cannot provide
// it, exactly as menuItems does for the dropdowns.
//
// A group whose rows are all hidden is skipped as well: a heading with
// nothing under it (the application menu, whose items have no shortcut)
// would only take a line and tell the reader nothing.
func (m Model) keybindingsLines() []keybindingLine {
	var out []keybindingLine
	for _, def := range menuDefs {
		var rows []keybindingLine
		for _, it := range def.items {
			if it.shortcut == "" {
				continue
			}
			if it.needsTx && !m.supportsTransactions() {
				continue
			}
			rows = append(rows, keybindingLine{
				label: m.t(it.labelKey),
				keys:  displayItemShortcuts(it),
			})
		}
		if len(rows) == 0 {
			continue
		}
		out = append(out, keybindingLine{group: true, label: m.t(def.titleKey)})
		out = append(out, rows...)
	}
	for _, g := range paneKeyBindings {
		out = append(out, keybindingLine{group: true, label: m.t(g.titleKey)})
		for _, row := range g.rows {
			out = append(out, keybindingLine{label: m.t(row.labelKey), keys: row.keys})
		}
	}
	return out
}

// keybindingsLayout is the computed geometry of the overlay. Rendering and
// mouse hit testing share it, so the drawn box and the clickable box agree.
type keybindingsLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int // content width between the borders
	labelW     int // label column width
	listH      int // visible content rows
}

// keybindingsLayout computes a centred box that fits the content but never
// grows past the screen.
func (m Model) keybindingsLayout() keybindingsLayout {
	area := m.paneRects().area
	lines := m.keybindingsLines()

	labelW, keysW, contentW := 0, 0, 0
	for _, line := range lines {
		if line.group {
			contentW = max(contentW, lipgloss.Width(line.label))
			continue
		}
		labelW = max(labelW, lipgloss.Width(line.label))
		keysW = max(keysW, lipgloss.Width(line.keys))
	}
	if labelW > 0 {
		contentW = max(contentW, labelW+2+keysW)
	}

	// One space of padding on each side plus the two border columns.
	w := min(max(contentW+4, 24), max(4, area.W))
	// Border, title, content and hint: four rows besides the list itself.
	h := min(len(lines)+4, max(4, area.H))
	return keybindingsLayout{
		x:      area.X + max(0, (area.W-w)/2),
		y:      area.Y + max(0, (area.H-h)/2),
		w:      w,
		h:      h,
		innerW: max(1, w-2),
		labelW: labelW,
		listH:  max(1, h-4),
	}
}

// keybindingsMaxScroll is the last scroll offset that still fills the list.
func (m Model) keybindingsMaxScroll() int {
	return max(0, len(m.keybindingsLines())-m.keybindingsLayout().listH)
}

// scrollKeybindings moves the list by delta rows, clamped to the content.
func (m *Model) scrollKeybindings(delta int) {
	m.keysDlg.scroll = clamp(m.keysDlg.scroll+delta, 0, m.keybindingsMaxScroll())
}

// handleKeybindingsKey handles a key press while the overlay is open. It always
// reports handled: a modal list must not let typing leak into the editor or the
// result grid behind it.
func (m *Model) handleKeybindingsKey(key string) bool {
	switch key {
	case "esc", "f1":
		m.closeKeybindings()
	case "up":
		m.scrollKeybindings(-1)
	case "down":
		m.scrollKeybindings(1)
	case "pgup":
		m.scrollKeybindings(-max(1, m.keybindingsLayout().listH))
	case "pgdown":
		m.scrollKeybindings(max(1, m.keybindingsLayout().listH))
	case "home":
		m.keysDlg.scroll = 0
	case "end":
		m.keysDlg.scroll = m.keybindingsMaxScroll()
	}
	return true
}

// keybindingsRect is the clickable outer rectangle.
func (m Model) keybindingsRect() Rect {
	l := m.keybindingsLayout()
	return Rect{X: l.x, Y: l.y, W: l.w, H: l.h}
}

// handleKeybindingsMouse scrolls with the wheel, dismisses on an outside click
// and swallows everything else. While the overlay is open the pointer must not
// reach the panes underneath it.
func (m Model) handleKeybindingsMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if tea.MouseEvent(msg).IsWheel() {
		delta := 3
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelLeft {
			delta = -3
		}
		m.scrollKeybindings(delta)
		return m, nil
	}
	if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress &&
		!m.keybindingsRect().Contains(msg.X, msg.Y) {
		m.closeKeybindings()
	}
	return m, nil
}

// ---- Rendering ----

// renderKeybindingsDialog renders the overlay and returns its screen origin
// plus the block of lines to blit.
func (m Model) renderKeybindingsDialog() (x, y int, block []string, ok bool) {
	if !m.keysDlg.open {
		return 0, 0, nil, false
	}
	l := m.keybindingsLayout()
	// Below five rows there is no room for a border, a title, one content row
	// and the hint; leaving the overlay undrawn is better than drawing a
	// mangled box.
	if l.w < 4 || l.h < 5 {
		return 0, 0, nil, false
	}

	lines := m.keybindingsLines()
	scroll := clamp(m.keysDlg.scroll, 0, max(0, len(lines)-l.listH))
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))

	title := padANSI(truncateANSI(" "+m.t(i18n.KeyKeysTitle), l.innerW), l.innerW)
	out = append(out, border.Render("│")+m.theme.dlgTitle.Render(title)+border.Render("│"))

	for i := 0; i < l.listH; i++ {
		idx := scroll + i
		var line string
		switch {
		case idx >= len(lines):
			line = box.Render(strings.Repeat(" ", l.innerW))
		case lines[idx].group:
			text := padANSI(truncateANSI(" "+lines[idx].label, l.innerW), l.innerW)
			line = m.theme.dlgLabel.Render(text)
		default:
			line = m.renderKeybindingRow(lines[idx], l)
		}
		out = append(out, border.Render("│")+line+border.Render("│"))
	}

	hint := padANSI(truncateANSI(" "+m.t(i18n.KeyKeysHint), l.innerW), l.innerW)
	out = append(out, border.Render("│")+box.Render(hint)+border.Render("│"))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// renderKeybindingRow lays out one label/shortcut pair: the label on the left,
// the shortcut right-aligned so the bindings form one readable column.
func (m Model) renderKeybindingRow(line keybindingLine, l keybindingsLayout) string {
	label := padANSI(truncateANSI(line.label, l.labelW), l.labelW)
	gap := l.innerW - (1 + l.labelW + 2 + lipgloss.Width(line.keys) + 1)
	gap = max(gap, 1)
	text := m.theme.dlgBox.Render(" ") +
		m.theme.dlgValue.Render(label) +
		m.theme.dlgBox.Render(strings.Repeat(" ", gap)) +
		m.theme.dlgDesc.Render(line.keys) +
		m.theme.dlgBox.Render(" ")
	return padANSI(truncateANSI(text, l.innerW), l.innerW)
}
