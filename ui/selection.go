package ui

// selection.go holds the editor selection and the small queries built on it.

import "github.com/p-v-q/tdb/textbuf"

// ---- Selection state ----

// selectionState holds the selection anchor; the caret is the other end.
type selectionState struct {
	anchor *textbuf.Pos
}

func (s selectionState) active() bool { return s.anchor != nil }

// hasSelection reports whether a non-empty selection exists.
func (m Model) hasSelection() bool {
	_, _, ok := m.selectionRange()
	return ok
}

// selectionRange returns the normalized selection [start, end).
func (m Model) selectionRange() (textbuf.Pos, textbuf.Pos, bool) {
	if m.sel.anchor == nil {
		return textbuf.Pos{}, textbuf.Pos{}, false
	}
	a, b := *m.sel.anchor, m.buf.Cursor()
	if b.Less(a) {
		a, b = b, a
	}
	if a == b {
		return textbuf.Pos{}, textbuf.Pos{}, false
	}
	return a, b, true
}

// selectedText returns the selected text.
func (m Model) selectedText() string {
	start, end, ok := m.selectionRange()
	if !ok {
		return ""
	}
	return m.buf.Slice(start, end)
}

// clearSelection drops the selection.
func (m *Model) clearSelection() { m.sel.anchor = nil }

// beginSelection anchors a selection at the current caret.
func (m *Model) beginSelection() {
	p := m.buf.Cursor()
	m.sel.anchor = &p
}

// extendSelectionTo moves the far end of the selection to p (no-op when the
// caret is already there).
func (m *Model) extendSelectionTo(p textbuf.Pos) {
	if m.sel.anchor == nil {
		m.beginSelection()
	}
	m.buf.SetCursor(p)
}
