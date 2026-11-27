package ui

// toolbar.go implements the button bar under the menu bar: the commands that
// deserve a single click (new tab, save, run, format) plus settings and help.
// Like the menu bar, the button table is pure data and is shared by rendering
// and hit testing, so what is drawn is exactly what is clickable.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// toolButton is one toolbar button. A button with sep set only draws a
// separator between groups and cannot be clicked. The accel rune is the
// mnemonic letter underlined in the label, matching the menus.
type toolButton struct {
	labelKey i18n.Key
	icon     toolbarIconKey
	accel    rune
	action   menuAction
	sep      bool
}

// toolButtons is the toolbar content, left to right. Buttons are text on their
// own background: a terminal cannot draw icons, and a label is the only thing
// that reads as a button at any font or width.
var toolButtons = []toolButton{
	{labelKey: i18n.KeyMenuNewTab, icon: iconNewTab, accel: 'n', action: actNewTab},
	{labelKey: i18n.KeyMenuSave, icon: iconSave, accel: 's', action: actSave},
	{labelKey: i18n.KeyMenuRun, icon: iconRun, accel: 'r', action: actRun},
	{labelKey: i18n.KeyToolFormat, icon: iconFormat, accel: 'f', action: actFormat},
	{sep: true},
	{labelKey: i18n.KeyMenuSettings, icon: iconSettings, accel: 's', action: actPreferences},
	{labelKey: i18n.KeyMenuHelp, icon: iconHelp, accel: 'h', action: actKeymap},
}

// toolGap is the number of blank columns between toolbar slots.
const toolGap = 1

// toolSlot is the on-screen rectangle of one slot (button or separator).
type toolSlot struct {
	index int // index into toolButtons
	x, w  int
}

// toolSlots lays the buttons out along the toolbar row. Rendering and hit
// testing share the result.
func (m Model) toolSlots(rect Rect) []toolSlot {
	out := make([]toolSlot, 0, len(toolButtons))
	x := rect.X
	for i, b := range toolButtons {
		w := 3 // a separator is one column wide, with a gap on each side
		if !b.sep {
			w = m.toolCellWidth(b)
		}
		if x+w > rect.X+rect.W {
			break
		}
		out = append(out, toolSlot{index: i, x: x, w: w})
		x += w + toolGap
	}
	return out
}

// toolCell is the content of a button: its icon when the icon set has one, and
// its label otherwise. A button shows one or the other, never both: an icon
// with its name printed next to it is neither an icon nor a clear label.
func (m Model) toolCell(b toolButton) string {
	if icon := m.icons().icon(b.icon); icon != "" {
		return icon
	}
	return m.t(b.labelKey)
}

// toolCellWidth is the width of a button cell, padding included.
func (m Model) toolCellWidth(b toolButton) int {
	return lipgloss.Width(m.toolCell(b)) + 2
}

// toolItemAt returns the index of the toolbar button under a screen coordinate.
func (m Model) toolItemAt(x, y int) (int, bool) {
	r := m.paneRects()
	if !r.toolbar.Contains(x, y) {
		return 0, false
	}
	for _, s := range m.toolSlots(r.toolbar) {
		if toolButtons[s.index].sep {
			continue
		}
		if x >= s.x && x < s.x+s.w {
			return s.index, true
		}
	}
	return 0, false
}

// renderToolbar renders the toolbar row, highlighting the button under the
// pointer.
func (m Model) renderToolbar(rect Rect) []string {
	if rect.W < 1 || rect.H < 1 {
		return nil
	}
	canvas := []string{m.theme.menuBarFill.Render(strings.Repeat(" ", rect.W))}
	for _, s := range m.toolSlots(rect) {
		b := toolButtons[s.index]
		if b.sep {
			// A dim vertical rule centred in its slot.
			sep := m.theme.toolSep.Render("│")
			blit(canvas, Rect{X: s.x + 1, Y: 0, W: 1, H: 1}, []string{sep})
			continue
		}
		style := m.theme.toolBtn
		if s.index == m.toolHover {
			style = m.theme.toolBtnHover
		}
		// A filled pill: an icon in icon mode, the label with its mnemonic
		// letter underlined otherwise.
		content := m.toolCell(b)
		if m.icons().icon(b.icon) == "" {
			content = labelWithMnemonic(content, b.accel, style)
		}
		cell := style.Render(" ") + content + style.Render(" ")
		blit(canvas, Rect{X: s.x, Y: 0, W: s.w, H: 1}, []string{cell})
	}
	return canvas
}
