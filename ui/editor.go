package ui

// editor.go contains editor state helpers: caret movement, scrolling,
// completion, token caching and selection primitives. Key dispatch lives in
// editor_keys.go; user-facing file and SQL commands live in editor_commands.go.

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/complete"
	"github.com/p-v-q/tdb/sql"
	"github.com/p-v-q/tdb/textbuf"
)

// scrollBy scrolls the editor view by rows; it deliberately does not force the
// caret into view, so the user can browse freely.
func (m *Model) scrollBy(delta int) {
	m.firstRow = clamp(m.firstRow+delta, 0, max(0, m.buf.Lines()-1))
}

// moveVertical moves n rows, one at a time.
func (m *Model) moveVertical(n int) {
	step := 1
	if n < 0 {
		step = -1
		n = -n
	}
	for i := 0; i < n; i++ {
		if step > 0 {
			m.buf.MoveDown()
		} else {
			m.buf.MoveUp()
		}
	}
	m.ensureCursorVisible()
}

// insertNewline inserts a newline, copying the indentation of the current line.
func (m *Model) insertNewline() {
	line := m.buf.Line(m.buf.Cursor().Row)
	indent := 0
	for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	m.buf.InsertString("\n" + string(line[:indent]))
}

// afterEdit refreshes completion and scrolling after an edit. When
// autoComplete is true it tries to open the completion popup.
func (m *Model) afterEdit(autoComplete bool) {
	m.refreshCompletion(autoComplete)
	m.ensureCursorVisible()
}

// refreshCompletion refreshes the popup for the caret context. When force is
// true an empty prefix still shows candidates (Ctrl+Space after FROM, say).
func (m *Model) refreshCompletion(force bool) {
	ctx := sql.AnalyzeContext(m.buf.Text(), toSQLPos(m.buf.Cursor()), m.cfg.Dialect)
	if !force && ctx.Prefix == "" && ctx.Qualifier == "" {
		m.popup.close()
		return
	}
	req := complete.Request{Context: ctx, Catalog: m.cat, Dialect: m.cfg.Dialect}
	m.popup.set(m.cfg.Engine.Complete(req), ctx.PrefixStart)
}

// acceptCompletion inserts the selected candidate.
func (m Model) acceptCompletion() (Model, tea.Cmd) {
	c, ok := m.popup.current()
	if !ok {
		m.popup.close()
		return m, nil
	}
	insert := c.Insert
	if insert == "" {
		insert = c.Label
	}
	// The candidate names an object of the catalog, and the editor draws its
	// buffer verbatim: an identifier carrying an escape sequence would reach
	// the terminal as soon as it is inserted (see sanitize.go). Every control
	// character is dropped but the line break — a snippet template is
	// multi-line SQL, and '\n' is a control character too, so removing it
	// would glue the template into one line and move the caret TailOffset
	// places.
	insert = stripControlKeepLines(insert)
	start := textbuf.Pos{Row: m.popup.prefixStart.Row, Col: m.popup.prefixStart.Col}
	m.buf.ReplaceRange(start, m.buf.Cursor(), insert)
	// The caret lands at the end of the inserted text, or at the placeholder a
	// snippet asked for (TailOffset runes before the end).
	m.buf.SetCursor(posAfterOffset(start, insert, len([]rune(insert))-c.TailOffset))
	m.popup.close()
	m.ensureCursorVisible()
	return m, nil
}

// saveFile writes the active tab's buffer to the file it was opened with.

// updateContext refreshes the context information shown in the status bar after
// the caret or the text changed.
func (m *Model) updateContext() {
	m.lastCtx = sql.AnalyzeContext(m.buf.Text(), toSQLPos(m.buf.Cursor()), m.cfg.Dialect)
}

// ensureCursorVisible adjusts the scroll offsets so the caret stays visible.
func (m *Model) ensureCursorVisible() {
	c := m.buf.Cursor()
	if c.Row < m.firstRow {
		m.firstRow = c.Row
	}
	if c.Row >= m.firstRow+m.editorHeight() {
		m.firstRow = c.Row - m.editorHeight() + 1
	}
	if c.Col < m.scrollX {
		m.scrollX = c.Col
	}
	if c.Col >= m.scrollX+m.editorWidth() {
		m.scrollX = c.Col - m.editorWidth() + 1
	}
	m.firstRow = max(m.firstRow, 0)
	m.scrollX = max(m.scrollX, 0)
}

// lineKinds returns the token kind of every character, cached per text.
func (m *Model) lineKinds() [][]sql.TokenKind {
	text := m.buf.Text()
	if m.lexCache.text == text && m.lexCache.kinds != nil {
		return m.lexCache.kinds
	}
	spans := sql.Spans(sql.Lex(text, m.cfg.Dialect))
	kinds := make([][]sql.TokenKind, m.buf.Lines())
	for row := range kinds {
		lineLen := len(m.buf.Line(row))
		k := make([]sql.TokenKind, 0, lineLen)
		if row < len(spans) {
			for _, sp := range spans[row] {
				for range sp.Text {
					k = append(k, sp.Kind)
				}
			}
		}
		for len(k) < lineLen {
			k = append(k, sql.KindWhitespace)
		}
		kinds[row] = k
	}
	m.lexCache.text, m.lexCache.kinds = text, kinds
	return kinds
}

// ---- Selection helpers (keyboard side) ----

// startSelectionIfNeeded anchors a selection at the caret if none exists.
func (m *Model) startSelectionIfNeeded() {
	if !m.sel.active() {
		m.beginSelection()
	}
}

// moveCaret performs one caret movement, dropping the selection unless Shift
// is held.
func (m *Model) moveCaret(move func()) {
	m.clearSelection()
	move()
}

// moveVertically moves one row; extend keeps (and grows) the selection.
func (m *Model) moveVertically(delta int, extend bool) {
	if extend {
		m.startSelectionIfNeeded()
	} else {
		m.clearSelection()
	}
	if delta > 0 {
		m.buf.MoveDown()
	} else {
		m.buf.MoveUp()
	}
}

// deleteSelection deletes the selection and reports whether it deleted
// anything.
func (m *Model) deleteSelection() bool {
	start, end, ok := m.selectionRange()
	if !ok {
		return false
	}
	m.buf.DeleteRange(start, end)
	m.clearSelection()
	return true
}

// normalizePaste normalizes pasted line endings: Windows text arrives with
// "\r\n", a terminal copy may carry a lone "\r", and both become the single
// break that starts a buffer line — dropping them instead would lose a line
// of the pasted text, keeping them would leave a control character the frame
// carries (and the editor draws one buffer row per line, not per '\r').
func normalizePaste(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// sanitizePastedText cleans text on its way into the editor buffer through a
// paste.
//
// The buffer is drawn verbatim (see sanitize.go), which is right for a
// keystroke: what the user typed is what the user meant. A paste is not a
// keystroke — it is text from outside the program, copied from a web page, a
// shared file or another terminal, and SQL from there may carry an escape
// sequence the terminal executes the moment the buffer is drawn: OSC 52
// rewrites the clipboard, ESC[2J clears the screen, a bare CSI moves the
// cursor. Every other input of the program cleans its pastes (cell editor,
// object filter, find prompt, export path, tab rename, vault fields); this is
// the same rule for the one multiline input.
//
// Two control characters survive because they are the structure of the SQL
// itself: '\n' separates the statements' lines and '\t' indents them — drop
// them and a pasted statement arrives glued into one line or flattened.
// Carriage returns are normalized to '\n' first (normalizePaste above) rather
// than dropped, so a CRLF paste does not lose a line. Everything else —
// letters, digits, quotes, semicolons, CJK, emoji — passes through unchanged.
func sanitizePastedText(s string) string {
	s = normalizePaste(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

func toSQLPos(p textbuf.Pos) sql.Pos { return sql.Pos{Row: p.Row, Col: p.Col} }

// stripControlKeepLines cleans a text for the editor buffer: like
// stripControl it drops the control characters a catalog identifier may
// carry, but it keeps the line break. A snippet template is multi-line SQL
// and '\n' is a control character as well — dropping it would paste the
// template on one line and shift the caret by as many runes.
func stripControlKeepLines(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '$' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
}
