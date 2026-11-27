package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// menu_render.go draws the menu bar and its dropdown. Geometry comes from the
// same functions used by hit testing.

// ---- Rendering ----

// renderMenuBar renders the menu bar, highlighting the open menu.
func (m Model) renderMenuBar(rect Rect) []string {
	if rect.W < 1 || rect.H < 1 {
		return nil
	}
	titles := m.menuTitles(rect)
	line := m.theme.menuBarFill.Render(strings.Repeat(" ", rect.W))
	canvas := []string{line}

	for _, t := range titles {
		def := menuDefs[t.index]
		style := m.theme.menuTitle
		switch {
		case t.index == m.menu.open:
			style = m.theme.menuTitleActive
		case t.index == m.menu.hover:
			style = m.theme.menuHover
		}
		if t.index == appMenuIndex {
			// The application name is bold, as on macOS.
			style = m.theme.menuApp
			switch {
			case t.index == m.menu.open:
				style = m.theme.menuAppActive
			case t.index == m.menu.hover:
				style = m.theme.menuHover
			}
		}
		blit(canvas, Rect{X: t.x, Y: 0, W: t.w, H: 1},
			[]string{style.Render(" " + m.t(def.titleKey) + " ")})
	}

	// The right-hand side carries the session info (macOS keeps its status
	// items there). It is clipped so it never runs into the menu titles.
	last := rect.X
	if n := len(titles); n > 0 {
		last = titles[n-1].x + titles[n-1].w
	}
	if avail := rect.X + rect.W - last; avail > 2 {
		// Prefer a shorter summary over cutting the end of the text off.
		for _, info := range []string{m.sessionInfo(), m.sessionInfoShort(), m.sessionConnInfo()} {
			text := m.theme.menuInfo.Render(" " + truncateANSI(info, avail-2) + " ")
			if w := lipgloss.Width(text); w <= avail {
				blit(canvas, Rect{X: rect.X + rect.W - w, Y: 0, W: w, H: 1}, []string{text})
				break
			}
		}
	}
	return canvas
}

// renderMenuDropdown renders the open dropdown with a border, on top of the
// panes.
func (m Model) renderMenuDropdown(rect Rect) []string {
	dr, ok := m.menuDropdownScreenRect(rect)
	if !ok || dr.H < 3 || dr.W < 4 {
		return nil
	}
	def := menuDefs[m.menu.open]
	inner := dr.W - 2
	border := m.theme.menuBorder

	out := []string{border.Render("┌" + strings.Repeat("─", inner) + "┐")}
	for i, it := range m.menuItems(def) {
		style := m.theme.menuBox
		if i == m.menu.sel {
			style = m.theme.menuItemSel
		}
		// The content width is always inner: leading space, label (mnemonic
		// underlined), flexible gap, shortcut.
		label := m.t(it.labelKey)
		hint := displayItemShortcuts(it)
		gap := inner - 1 - lipgloss.Width(label) - lipgloss.Width(hint)
		if gap < 1 {
			gap = 1
		}
		line := style.Render(" ") +
			labelWithMnemonic(label, it.accel, style) +
			style.Render(strings.Repeat(" ", gap)) +
			style.Render(hint)
		out = append(out, border.Render("│")+line+border.Render("│"))
	}
	out = append(out, border.Render("└"+strings.Repeat("─", inner)+"┘"))
	return out
}
