package ui

// find.go implements the incremental find prompt shown in the status bar. The
// prompt owns the keyboard while it is open; the match itself is selected in
// the editor, so the ordinary selection rendering highlights it without a
// second highlight layer.

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/p-v-q/tdb/i18n"
	"github.com/p-v-q/tdb/textbuf"
)

// findState is the state of the status-bar find prompt.
type findState struct {
	open    bool
	query   string
	caret   int // caret position in query, in runes
	matches int // number of matches for query
}

// openFind opens the find prompt. A single-line selection seeds the query, the
// way desktop editors do; otherwise the previous query is kept and the search
// starts at the caret.
func (m *Model) openFind() {
	m.closeMenu()
	m.closeObjectsMenu()
	m.popup.close()
	m.layout.focus = paneEditor

	seeded := false
	if a, b, ok := m.selectionRange(); ok {
		text := m.buf.Slice(a, b)
		if text != "" && !strings.ContainsAny(text, "\r\n") {
			// The selection comes from a file, whose bytes may hold control
			// characters; the query is drawn in the status bar, so it is
			// cleaned here — the prompt then shows and searches the same
			// text. A selection on a break stays unseeded, as before.
			clean := stripControl(text)
			m.find.query = clean
			m.find.caret = len([]rune(clean))
			seeded = true
		}
	}
	if !seeded {
		m.find.caret = len([]rune(m.find.query))
	}
	m.find.open = true
	m.find.matches = len(findOffsets(m.buf.Text(), m.find.query))
	if !seeded && m.find.query != "" {
		m.findSelect(m.buf.Cursor(), true)
	}
}

// closeFind dismisses the prompt and leaves the current match selected.
func (m *Model) closeFind() { m.find.open = false }

// handleFindKey edits the query or moves between matches.
func (m Model) handleFindKey(key string, runes []rune) (Model, tea.Cmd, bool) {
	switch key {
	case "esc":
		m.closeFind()
		return m, nil, true
	case "enter", "ctrl+f":
		m.findNext(true)
		return m, nil, true
	case "up":
		// The previous match. Bubble Tea v1 has no "shift+enter" key
		// name, so the up arrow is the only way back, and the hint in the
		// status bar says so.
		m.findNext(false)
		return m, nil, true
	case "left":
		m.find.caret = max(0, m.find.caret-1)
		return m, nil, true
	case "right":
		m.find.caret = min(len([]rune(m.find.query)), m.find.caret+1)
		return m, nil, true
	case "tab":
		// The prompt is the only field; Tab must not move the pane focus.
		return m, nil, true
	case "home":
		m.find.caret = 0
		return m, nil, true
	case "end":
		m.find.caret = len([]rune(m.find.query))
		return m, nil, true
	case "backspace":
		q := []rune(m.find.query)
		if m.find.caret > 0 && m.find.caret <= len(q) {
			q = append(q[:m.find.caret-1], q[m.find.caret:]...)
			m.find.query = string(q)
			m.find.caret--
			m.findQueryChanged()
		}
		return m, nil, true
	case "delete":
		q := []rune(m.find.query)
		if m.find.caret >= 0 && m.find.caret < len(q) {
			q = append(q[:m.find.caret], q[m.find.caret+1:]...)
			m.find.query = string(q)
			m.findQueryChanged()
		}
		return m, nil, true
	}
	if len(runes) == 0 {
		// Other control keys (tab switching, menu access, …) keep working.
		return m, nil, false
	}
	q := []rune(m.find.query)
	caret := clamp(m.find.caret, 0, len(q))
	// A paste arrives as one message whose runes hold the whole text, and it
	// may carry escape sequences. The prompt draws the query in the status
	// bar, so the control characters are dropped here on the way in — the
	// prompt then stores and draws the same text — and once more when the
	// query is rendered.
	runes = []rune(stripControl(string(runes)))
	if len(runes) == 0 {
		return m, nil, true // a paste that held nothing printable
	}
	next := make([]rune, 0, len(q)+len(runes))
	next = append(next, q[:caret]...)
	next = append(next, runes...)
	next = append(next, q[caret:]...)
	m.find.query = string(next)
	m.find.caret = caret + len(runes)
	m.findQueryChanged()
	return m, nil, true
}

// findQueryChanged re-runs the search from the start of the current match (or
// the caret), so extending a query keeps the match in place.
func (m *Model) findQueryChanged() {
	if m.find.query == "" {
		m.find.matches = 0
		return
	}
	from := m.buf.Cursor()
	if a, _, ok := m.selectionRange(); ok {
		from = a
	}
	m.findSelect(from, true)
}

// findNext moves to the next (or previous) match, wrapping around.
func (m *Model) findNext(forward bool) {
	if m.find.query == "" {
		m.find.matches = 0
		return
	}
	from := m.buf.Cursor()
	if a, b, ok := m.selectionRange(); ok {
		if forward {
			from = b
		} else {
			from = a
		}
	}
	m.findSelect(from, forward)
}

// findSelect selects the first match at or after from (forward), or the last
// one before from (backward). Searches wrap around the buffer.
func (m *Model) findSelect(from textbuf.Pos, forward bool) {
	text := m.buf.Text()
	offsets := findOffsets(text, m.find.query)
	m.find.matches = len(offsets)
	if len(offsets) == 0 {
		m.clearSelection()
		return
	}
	fromOffset := m.offsetAt(from)
	idx := -1
	if forward {
		for i, off := range offsets {
			if off >= fromOffset {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0
		}
	} else {
		for i := len(offsets) - 1; i >= 0; i-- {
			if offsets[i] < fromOffset {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = len(offsets) - 1
		}
	}
	start := posAtOffset(text, offsets[idx])
	end := posAtOffset(text, offsets[idx]+len([]rune(m.find.query)))
	m.sel.anchor = &start
	m.buf.SetCursor(end)
	m.ensureCursorVisible()
}

// offsetAt converts a buffer position to a rune offset.
func (m Model) offsetAt(p textbuf.Pos) int {
	offset := 0
	for row := 0; row < p.Row && row < m.buf.Lines(); row++ {
		offset += len(m.buf.Line(row)) + 1
	}
	return offset + p.Col
}

// posAtOffset converts a rune offset in text to a buffer position.
func posAtOffset(text string, offset int) textbuf.Pos {
	runes := []rune(text)
	offset = clamp(offset, 0, len(runes))
	row, col := 0, 0
	for i := 0; i < offset; i++ {
		if runes[i] == '\n' {
			row++
			col = 0
		} else {
			col++
		}
	}
	return textbuf.Pos{Row: row, Col: col}
}

// findOffsets returns the non-overlapping rune offsets of query in text. The
// comparison is case-insensitive, which suits SQL and matches the browser's
// other search behavior.
func findOffsets(text, query string) []int {
	q := []rune(query)
	if len(q) == 0 {
		return nil
	}
	t := []rune(text)
	var offsets []int
	for i := 0; i+len(q) <= len(t); i++ {
		match := true
		for j := range q {
			if unicode.ToLower(t[i+j]) != unicode.ToLower(q[j]) {
				match = false
				break
			}
		}
		if match {
			offsets = append(offsets, i)
			i += len(q) - 1
		}
	}
	return offsets
}

// renderFindStatus draws the prompt in place of the ordinary status bar.
func (m Model) renderFindStatus() string {
	// The query is cleaned for drawing (see editViewText): it may have been
	// seeded from a file's bytes or pasted, and the status bar is one line
	// the terminal must not act on. The stored query keeps what was typed,
	// so the search still runs over the same text the user sees.
	text, at := editViewText(m.find.query, m.find.caret)
	q := []rune(text)
	caret := at
	left := " " + m.theme.statusKey.Render(m.t(i18n.KeyFindLabel)+":") + " " +
		string(q[:caret]) + m.theme.cursor.Render(" ") + string(q[caret:])

	count := m.t(i18n.KeyFindEmpty)
	if m.find.query != "" {
		count = m.t(i18n.KeyFindNoMatches)
		if m.find.matches > 0 {
			count = m.t(i18n.KeyFindMatches, m.find.matches)
		}
	}
	right := " " + m.theme.statusKey.Render(count+"  "+m.t(i18n.KeyFindHint)) + " "

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncateANSI(left+"  "+right, m.width)
	}
	return left + strings.Repeat(" ", gap) + right
}
