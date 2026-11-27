package ui

// select_list.go implements the one dropdown every "a value out of a known
// set" field uses: the settings dialog's language and theme rows and the
// export dialog's format row. It replaces the ←/→ cycling those rows used —
// cycling is fine for two options (and stays as a left/right fallback), but a
// three-option list with a "follow the system" entry is hard to walk and
// impossible to survey.
//
// The component keeps only state and geometry; the Model callsite owns the
// meaning of the chosen value (it applies and persists the setting). The open
// list is modal inside its dialog: while it is open, every key belongs to it,
// so nothing leaks to the rows behind it.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// selectOption is one entry of a dropdown: the stable ID handed back on pick
// and the label shown, already localized by the callsite.
type selectOption struct {
	Value string
	Label string
}

// selectList is the state of one dropdown. It keeps two positions on purpose:
// sel is the keyboard/cursor highlight (the row Enter picks), hover is where
// the pointer is. Moving the pointer never moves sel, so hovering cannot
// change a value by accident.
type selectList struct {
	open    bool
	options []selectOption
	sel     int // highlighted option while open (keyboard; Enter picks it)
	hover   int // option under the pointer, -1 when the pointer is elsewhere
}

// Open opens the list on the current value; options keep their display order.
func (s *selectList) Open(options []selectOption, current string) {
	sel := 0
	for i, opt := range options {
		if opt.Value == current {
			sel = i
		}
	}
	s.open = true
	s.options = options
	s.sel = sel
	s.hover = -1 // the pointer has not been placed on this list yet
}

// Close closes the list without choosing anything and forgets the pointer
// position, so a stale hover can never reappear on the next open.
func (s *selectList) Close() {
	s.open = false
	s.hover = -1
}

// Chosen returns the value of the highlighted option.
func (s *selectList) Chosen() string {
	if s.sel < 0 || s.sel >= len(s.options) {
		return ""
	}
	return s.options[s.sel].Value
}

// handleKey moves the highlight or picks an option and reports what happened:
//
//   - chosen: the picked value — only meaningful when picked is true, and
//     allowed to be "" there (a "follow the system"-style option can carry
//     the empty string as its value);
//   - done: the interaction ended (pick or dismiss); the caller resumes the
//     dialog;
//   - picked: an option was actually chosen (Enter/Space). Dismissing with
//     Esc/Tab reports picked=false, so "gave up" and "picked the empty
//     value" can never be confused by the caller;
//   - handled: the key belongs to the open list and must not leak.
//
// While the list is open every key is handled: the list is modal inside the
// dialog, exactly like the conn form's type dropdown.
func (s *selectList) handleKey(key string) (chosen string, done bool, picked bool, handled bool) {
	if !s.open {
		return "", false, false, false
	}
	n := len(s.options)
	if n == 0 { // defensive: an empty list has nothing to choose from
		s.Close()
		return "", true, false, true
	}
	switch key {
	case "esc", "tab", "shift+tab":
		// Esc (and Tab) close the list only; the dialog stays as it was.
		s.Close()
		return "", true, false, true
	case "up":
		s.sel = ((s.sel-1)%n + n) % n
	case "down":
		s.sel = (s.sel + 1) % n
	case "home":
		s.sel = 0
	case "end":
		s.sel = n - 1
	case "enter", "space":
		chosen = s.Chosen()
		s.Close()
		return chosen, true, true, true
	}
	// Any other key is swallowed so typing cannot reach the rows behind.
	return "", false, false, true
}

// selectLayout is the computed geometry of an open dropdown, shared by
// rendering and hit testing.
type selectLayout struct {
	rect    Rect // outer box, borders included
	top     int  // first visible option
	visible int  // options that fit between the anchors
}

// layoutSelectList computes where the dropdown should be drawn: its frame is
// anchored just below the value (anchorX, anchorY), grows to the longest
// label and is clipped to the given right/bottom edges; options scroll to
// keep the highlight visible. The edges are the last column/row the frame may
// occupy (inside its dialog), so the dropdown can never cross the border of
// the dialog it belongs to.
func layoutSelectList(options []selectOption, sel, anchorX, anchorY, rightEdge, bottomEdge int) selectLayout {
	// The width follows the longest label plus padding and borders; clipped
	// to the room the dialog's inner width leaves.
	w := 4
	for _, opt := range options {
		w = max(w, lipgloss.Width(opt.Label)+4)
	}
	w = max(4, min(w, rightEdge-anchorX+1))
	y := anchorY + 1 // the frame starts right below the anchor row
	visible := clamp(len(options), 1, bottomEdge-y-1)
	y = min(y, bottomEdge-2) // never push the frame past the bottom edge
	top := clamp(sel-visible+1, 0, max(0, len(options)-visible))
	return selectLayout{rect: Rect{X: anchorX, Y: y, W: w, H: visible + 2}, top: top, visible: visible}
}

// selectRowAt maps a coordinate on an open dropdown to the option under it —
// a press that picks, or a motion that only hovers. Only a point inside the
// frame and on a visible option row counts: a point on the border would
// otherwise map to an option above or beside the viewport.
func selectRowAt(l selectLayout, options []selectOption, x, y int) (int, bool) {
	inside := x > l.rect.X && x < l.rect.X+l.rect.W-1 &&
		y > l.rect.Y && y < l.rect.Y+l.rect.H-1
	if !inside {
		return 0, false
	}
	row := y - l.rect.Y - 1
	if row >= l.visible {
		return 0, false
	}
	idx := l.top + row
	if idx >= len(options) {
		return 0, false
	}
	return idx, true
}

// renderSelectList renders the open dropdown as a block of lines to blit. It
// reuses the menu styles, like the conn form's type dropdown does.
func (m Model) renderSelectList(l selectLayout, s *selectList) []string {
	if l.rect.W < 4 || l.rect.H < 3 {
		return nil
	}
	inner := l.rect.W - 2
	border := m.theme.menuBorder

	out := make([]string, 0, l.rect.H)
	out = append(out, border.Render("┌"+strings.Repeat("─", inner)+"┐"))
	for i := 0; i < l.visible && l.top+i < len(s.options); i++ {
		style := m.theme.menuBox
		// The hovered row wears the selection look so the pointer reads on
		// the list; hover never moves sel, so the two may point at different
		// rows. When they land on the same row the selection wins — both are
		// the same selected style by design (round 7 §1).
		if l.top+i == s.sel || l.top+i == s.hover {
			style = m.theme.menuItemSel
		}
		label := padANSI(truncateANSI(s.options[l.top+i].Label, inner-2), inner-2)
		out = append(out, border.Render("│")+style.Render(" "+label+" ")+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return out
}
