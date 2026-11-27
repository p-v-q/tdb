package ui

// panes.go holds the shared pane chrome: the title bar with its close button,
// the expand strip left behind by a closed pane, the dividers and the results
// placeholder. All three panes reuse these pieces so they look and behave
// identically.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
)

// renderPaneTitle renders a pane header: the title on the left, the close
// button on the right (the editor has none).
func (m Model) renderPaneTitle(rect Rect, id paneID, closable bool) []string {
	return m.renderPaneTitleWith(rect, id, closable, "")
}

// renderPaneTitleWith renders the same header with an extra, already styled
// fragment glued behind the title — the object browser appends its filter
// indicator there. The line never grows wider than the pane: the title side is
// truncated before the close button can be pushed out.
func (m Model) renderPaneTitleWith(rect Rect, id paneID, closable bool, extra string) []string {
	if rect.H < 1 || rect.W < 1 {
		return nil
	}
	left := m.paneTitleStyle(id).Render(" "+m.t(id.titleKey())) + extra
	right := " "
	if closable {
		closeStyle := m.theme.paneClose
		if m.layout.focus == id {
			closeStyle = m.theme.paneCloseActive
		}
		right = closeStyle.Render(m.icons().paneClose)
	}
	pad := rect.W - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 0 {
		left = truncateANSI(left, max(0, rect.W-lipgloss.Width(right)))
		pad = 0
	}
	return []string{left + strings.Repeat(" ", pad) + right}
}

// paneTitleStyle is the title style, highlighted while the pane has the focus.
func (m Model) paneTitleStyle(id paneID) lipgloss.Style {
	if m.layout.focus == id {
		return m.theme.paneTitleActive
	}
	return m.theme.paneTitle
}

// renderExpandStrip renders the strip a closed pane leaves at the screen edge.
// vertical is true for the left-hand column (objects pane), false for the
// bottom row (results pane).
func (m Model) renderExpandStrip(rect Rect, vertical bool, hint string) []string {
	if rect.W < 1 || rect.H < 1 {
		return nil
	}
	style := m.theme.expandBtn
	if vertical {
		out := make([]string, 0, rect.H)
		for i := 0; i < rect.H; i++ {
			if i == 0 {
				out = append(out, style.Render(m.icons().expandClosed))
				continue
			}
			out = append(out, m.theme.expandStrip.Render("│"))
		}
		return out
	}
	line := style.Render(m.icons().expandUp) + " " + m.theme.expandStrip.Render(hint)
	return []string{truncateANSI(line, rect.W)}
}

// renderResizingPanes draws the panes as empty frames while a divider drag is
// moving them. Only the chrome (title bar, close button, divider and a dim
// hint) is rendered: re-laying out a large result grid or the editor on every
// mouse motion is what made resizing feel slow, so the full contents come back
// on release.
func (m Model) renderResizingPanes(canvas []string, r paneRects) {
	hint := m.theme.paneHint.Render(" " + m.t(i18n.KeyPaneResizing))
	drawHint := func(rect Rect) {
		if rect.W < 1 || rect.H < 2 {
			return
		}
		line := padANSI(truncateANSI(hint, rect.W), rect.W)
		blit(canvas, Rect{X: rect.X, Y: rect.Y + 1, W: rect.W, H: 1}, []string{line})
	}
	if m.layout.objectsVisible {
		blit(canvas, Rect{X: r.objects.X, Y: r.objects.Y, W: r.objects.W, H: 1},
			m.renderPaneTitle(r.objects, paneObjects, true))
		drawHint(r.objects)
	} else {
		blit(canvas, r.objectsExpand, m.renderExpandStrip(r.objectsExpand, true, ""))
	}
	blit(canvas, Rect{X: r.editor.X, Y: r.editor.Y, W: r.editor.W, H: 1},
		m.renderPaneTitle(r.editor, paneEditor, false))
	drawHint(r.editor)
	if m.layout.resultsVisible {
		blit(canvas, Rect{X: r.results.X, Y: r.results.Y, W: r.results.W, H: 1},
			m.renderPaneTitle(r.results, paneResults, true))
		drawHint(r.results)
	} else {
		blit(canvas, r.resultsExpand,
			m.renderExpandStrip(r.resultsExpand, false, m.t(i18n.KeyPaneResultsHidden)))
	}
	if m.layout.objectsVisible {
		blit(canvas, r.dividerV, m.renderDividerV(r.dividerV))
	}
	if m.layout.resultsVisible {
		blit(canvas, r.dividerH, m.renderDividerH(r.dividerH))
	}
}

// renderDividerV renders the vertical divider (right of the objects pane),
// which can be dragged.
func (m Model) renderDividerV(rect Rect) []string {
	if rect.H < 1 {
		return nil
	}
	style := m.theme.divider
	if m.layout.dragging == dividerVertical {
		style = m.theme.dividerActive
	}
	out := make([]string, 0, rect.H)
	for i := 0; i < rect.H; i++ {
		out = append(out, style.Render("│"))
	}
	return out
}

// renderDividerH renders the horizontal divider (above the results pane), which
// can be dragged.
func (m Model) renderDividerH(rect Rect) []string {
	if rect.W < 1 {
		return nil
	}
	style := m.theme.divider
	if m.layout.dragging == dividerHorizontal {
		style = m.theme.dividerActive
	}
	return []string{style.Render(strings.Repeat("─", rect.W))}
}
