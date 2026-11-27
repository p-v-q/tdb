package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/sql"
)

// popupMaxRows is the maximum number of candidates shown at once.
const popupMaxRows = 8

// popupState is the completion popup state.
type popupState struct {
	open        bool
	items       []complete.Candidate
	selected    int
	scroll      int
	prefixStart sql.Pos // start of the text being replaced
}

// set resets the popup with a new candidate list.
func (p *popupState) set(items []complete.Candidate, prefixStart sql.Pos) {
	p.items = items
	p.prefixStart = prefixStart
	p.selected = 0
	p.scroll = 0
	p.open = len(items) > 0
}

// close hides the popup.
func (p *popupState) close() {
	p.open = false
	p.items = nil
	p.selected = 0
	p.scroll = 0
}

// current returns the selected candidate.
func (p *popupState) current() (complete.Candidate, bool) {
	if !p.open || p.selected >= len(p.items) {
		return complete.Candidate{}, false
	}
	return p.items[p.selected], true
}

// move moves the selection by delta rows, wrapping around.
func (p *popupState) move(delta int) {
	if len(p.items) == 0 {
		return
	}
	p.selected = (p.selected + delta + len(p.items)) % len(p.items)
	p.ensureVisible()
}

// page moves the selection by delta pages.
func (p *popupState) page(delta int) {
	if len(p.items) == 0 {
		return
	}
	p.selected = clamp(p.selected+delta*popupMaxRows, 0, len(p.items)-1)
	p.ensureVisible()
}

// ensureVisible scrolls so that the selected item stays in view.
func (p *popupState) ensureVisible() {
	if p.selected < p.scroll {
		p.scroll = p.selected
	}
	if p.selected >= p.scroll+popupMaxRows {
		p.scroll = p.selected - popupMaxRows + 1
	}
}

// rows returns the number of candidates actually displayed.
func (p *popupState) rows() int {
	if len(p.items) < popupMaxRows {
		return len(p.items)
	}
	return popupMaxRows
}

// cleanedCandidates returns the candidates as they will be drawn: label and
// detail are cleaned once, here, so that the width measured for a row and the
// text rendered into that row are the same string. Measuring the raw catalog
// text would size the popup for something else than what it draws — lipgloss
// reads an escape sequence as zero-width styling, so a name that carries one
// measures narrower than the cleaned text that is drawn into it — and the row
// would end up padded and clipped against the wrong box.
func cleanedCandidates(items []complete.Candidate) []complete.Candidate {
	out := make([]complete.Candidate, len(items))
	for i, c := range items {
		c.Label, c.Detail = stripControl(c.Label), stripControl(c.Detail)
		out[i] = c
	}
	return out
}

// render draws the popup as a block of lines (header, candidates, hint).
func (p *popupState) render(t theme, ic iconSet, tr i18n.Translator, width int) []string {
	rows := p.rows()
	visible := cleanedCandidates(p.items[p.scroll : p.scroll+rows])
	head := tr.T(i18n.KeyPopupHeader, p.selected+1, len(p.items))

	// Width = the widest of the header and the candidate rows.
	w := lipgloss.Width(head)
	for _, c := range visible {
		itemW := lipgloss.Width(ic.kind(c.Kind) + " " + c.Label + "  " + c.Detail)
		if itemW > w {
			w = itemW
		}
	}
	if w > width {
		w = width
	}

	out := make([]string, 0, rows+2)
	out = append(out, padANSI(t.popupHeader.Render(head), w))

	for i, c := range visible {
		idx := p.scroll + i
		labelStyle, detailStyle := t.popupLabel, t.popupDetail
		if idx == p.selected {
			labelStyle, detailStyle = t.popupSel, t.popupSel
		} else if c.Kind == complete.KindKeyword {
			// Keywords keep their editor color in the candidate list.
			labelStyle = t.keyword
		}
		content := t.popupKind.Render(ic.kind(c.Kind)) + " " +
			labelStyle.Render(c.Label) + "  " +
			detailStyle.Render(c.Detail)
		out = append(out, padANSI(clipANSI(content, w), w))
	}

	out = append(out, padANSI(t.popupHeader.Render(tr.T(i18n.KeyPopupHint)), w))
	return out
}

// width returns the rendered popup width (for layout calculations).
func (p *popupState) width(t theme, ic iconSet, tr i18n.Translator, maxWidth int) int {
	w := 0
	for _, l := range p.render(t, ic, tr, maxWidth) {
		if lw := lipgloss.Width(l); lw > w {
			w = lw
		}
	}
	return w
}

// padANSI pads a string to the given display width.
func padANSI(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// clipANSI truncates a string to the given display width.
func clipANSI(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return truncateANSI(s, width)
}
