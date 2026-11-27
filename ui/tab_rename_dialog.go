package ui

// tab_rename_dialog.go implements the small modal input that renames a tab.
//
//   - Enter accepts the name (an empty name restores the automatic one),
//     Esc cancels.
//   - ←/→ move the caret, Backspace and Delete remove characters, and a
//     paste arrives as one chunk of text.
//   - The dialog is modal: it swallows every key and click, so nothing leaks
//     into the editor behind it.
//
// The name is stored in the tab's session, so it survives switching tabs and
// re-rendering.

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// tabRenameDialog is the state of the modal rename input.
type tabRenameDialog struct {
	open  bool
	tab   int    // index of the tab being renamed
	value string // the text typed so far
	caret int    // caret position in value, in runes
}

// openTabRename opens the rename input for the tab at the given index.
//
// The input starts with the name the user gave the tab, so an existing name
// can be edited instead of typed again. A tab without a name of the user's
// own starts empty: the automatic name is shown below the input, and an
// empty input keeps it.
func (m *Model) openTabRename(index int) {
	if index < 0 || index >= len(m.tabs) {
		return
	}
	m.closeMenu()
	m.closeObjectsMenu()
	m.closeResultMenu()
	m.closeTabMenu()
	m.closeFind()
	m.popup.close()
	name := m.tabs[index].userName
	m.tabRename = tabRenameDialog{
		open:  true,
		tab:   index,
		value: name,
		caret: len([]rune(name)),
	}
}

// closeTabRename dismisses the input without changing the name.
func (m *Model) closeTabRename() { m.tabRename.open = false }

// acceptTabRename applies the name typed in the dialog. An empty name clears
// the name of the user's own, which restores the automatic one.
func (m *Model) acceptTabRename() tea.Cmd {
	index := m.tabRename.tab
	name := strings.TrimSpace(m.tabRename.value)
	m.closeTabRename()
	if index < 0 || index >= len(m.tabs) {
		return nil
	}
	m.tabs[index].userName = name
	return m.setMessage(m.t(i18n.KeyTabRenamed))
}

// tabRenameInsert puts text at the caret and moves the caret behind it.
func (m *Model) tabRenameInsert(text string) {
	runes := []rune(m.tabRename.value)
	caret := clamp(m.tabRename.caret, 0, len(runes))
	insert := []rune(tabRenameClean(text))
	next := make([]rune, 0, len(runes)+len(insert))
	next = append(next, runes[:caret]...)
	next = append(next, insert...)
	next = append(next, runes[caret:]...)
	m.tabRename.value = string(next)
	m.tabRename.caret = caret + len(insert)
}

// tabRenameClean drops the control characters from a name: a pasted escape
// sequence or a newline must never reach the tab bar, which draws the name as
// plain text.
func tabRenameClean(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

// tabRenameBackspace removes the character before the caret.
func (m *Model) tabRenameBackspace() {
	runes := []rune(m.tabRename.value)
	caret := clamp(m.tabRename.caret, 0, len(runes))
	if caret == 0 {
		return
	}
	m.tabRename.value = string(runes[:caret-1]) + string(runes[caret:])
	m.tabRename.caret = caret - 1
}

// tabRenameDelete removes the character under the caret.
func (m *Model) tabRenameDelete() {
	runes := []rune(m.tabRename.value)
	caret := clamp(m.tabRename.caret, 0, len(runes))
	if caret >= len(runes) {
		return
	}
	m.tabRename.value = string(runes[:caret]) + string(runes[caret+1:])
}

// ---- Interaction ----

// handleTabRenameKey handles a key press while the rename input is open and
// reports whether it consumed the key. The dialog is modal, so it consumes
// everything.
func (m Model) handleTabRenameKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	key := msg.String()
	if msg.Type == tea.KeySpace {
		// Bubble Tea names the space key after its character; normalize it so
		// the key handling can use one stable name.
		key = "space"
	}
	switch key {
	case "esc":
		m.closeTabRename()
		return m, nil, true
	case "enter":
		return m, m.acceptTabRename(), true
	case "left":
		m.tabRename.caret = max(0, m.tabRename.caret-1)
		return m, nil, true
	case "right":
		m.tabRename.caret = min(len([]rune(m.tabRename.value)), m.tabRename.caret+1)
		return m, nil, true
	case "home":
		m.tabRename.caret = 0
		return m, nil, true
	case "end":
		m.tabRename.caret = len([]rune(m.tabRename.value))
		return m, nil, true
	case "backspace":
		m.tabRenameBackspace()
		return m, nil, true
	case "delete":
		m.tabRenameDelete()
		return m, nil, true
	}
	if msg.Paste && len(msg.Runes) > 0 {
		// A paste arrives as one message whose name is the bracketed text,
		// so the plain-key check below would swallow it. Insert it whole,
		// like the object filter does.
		m.tabRenameInsert(string(msg.Runes))
		return m, nil, true
	}
	if len(msg.Runes) == 0 {
		return m, nil, true
	}
	// Only keys whose key name IS the text they carry are text: modified
	// keys (Alt+2) carry runes as well, and those belong to the shortcut
	// layer this modal input shields.
	if key != string(msg.Runes) && key != "space" {
		return m, nil, true
	}
	m.tabRenameInsert(string(msg.Runes))
	return m, nil, true
}

// handleTabRenameMouse handles a mouse event while the rename input is open.
// A click outside the box dismisses it; every other click is swallowed.
func (m Model) handleTabRenameMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	l := m.tabRenameLayout()
	if !(Rect{X: l.x, Y: l.y, W: l.w, H: l.h}).Contains(msg.X, msg.Y) {
		m.closeTabRename()
		return m, nil
	}
	return m, nil
}

// ---- Geometry ----

// tabRenameLayout is the computed geometry of the dialog. Rendering and mouse
// hit testing share it, so the drawn box and the clickable box agree.
type tabRenameLayout struct {
	x, y, w, h int // outer box, borders included
	innerW     int // content width between the borders
}

func (m Model) tabRenameLayout() tabRenameLayout {
	// Every line is padded with a space on the left, so the content width
	// must fit the text plus that space (and one on the right).
	innerW := lipgloss.Width(m.t(i18n.KeyTabRenamePrompt)) + 2
	innerW = max(innerW, lipgloss.Width(m.t(i18n.KeyTabRenameHint))+2)
	innerW = max(innerW, lipgloss.Width(m.tabRenameAutoName())+2)
	innerW = max(innerW, lipgloss.Width(m.tabRename.value)+2)
	innerW = max(innerW, 24)
	if m.width > 4 {
		innerW = min(innerW, m.width-4)
	}
	w := innerW + 2
	h := 6 // border, prompt, input, automatic name, hint, border

	// Centre it in the area between the menu bar and the status bar.
	area := m.paneRects().area
	x := area.X + max(0, (area.W-w)/2)
	y := area.Y + max(0, (area.H-h)/2)
	return tabRenameLayout{x: x, y: y, w: w, h: h, innerW: innerW}
}

// tabRenameAutoName is the name the tab would show without a name of the
// user's own. The dialog shows it, so an empty input is not a mystery.
func (m Model) tabRenameAutoName() string {
	if m.tabRename.tab < 0 || m.tabRename.tab >= len(m.tabs) {
		return ""
	}
	return m.t(i18n.KeyTabRenameAuto, m.tabAutoLabel(m.tabRename.tab))
}

// ---- Rendering ----

// renderTabRenameDialog renders the rename input and returns its screen
// origin plus the block of lines to blit.
func (m Model) renderTabRenameDialog() (x, y int, block []string, ok bool) {
	if !m.tabRename.open {
		return 0, 0, nil, false
	}
	l := m.tabRenameLayout()
	if l.w < 4 || l.h < 3 {
		return 0, 0, nil, false
	}
	border, box := m.theme.dlgBorder, m.theme.dlgBox
	rule := strings.Repeat("─", l.w-2)
	line := func(style lipgloss.Style, text string) string {
		return border.Render("│") +
			style.Render(padANSI(truncateANSI(text, l.innerW), l.innerW)) +
			border.Render("│")
	}

	out := make([]string, 0, l.h)
	out = append(out, border.Render("┌"+rule+"┐"))
	out = append(out, line(m.theme.dlgTitle, " "+m.t(i18n.KeyTabRenamePrompt)))
	out = append(out, line(box, m.renderTabRenameInput(l.innerW)))
	out = append(out, line(m.theme.dlgDesc, " "+m.tabRenameAutoName()))
	out = append(out, line(m.theme.dlgBox, " "+m.t(i18n.KeyTabRenameHint)))
	out = append(out, border.Render("└"+rule+"┘"))
	return l.x, l.y, out, true
}

// renderTabRenameInput renders the text of the input with the caret in it.
func (m Model) renderTabRenameInput(width int) string {
	runes := []rune(m.tabRename.value)
	caret := clamp(m.tabRename.caret, 0, len(runes))
	before := padANSI(string(runes[:caret]), caret)
	after := padANSI(string(runes[caret:]), max(0, width-caret-1))
	return " " + m.theme.dlgValue.Render(before) +
		m.theme.cursor.Render(" ") + m.theme.dlgValue.Render(after)
}
