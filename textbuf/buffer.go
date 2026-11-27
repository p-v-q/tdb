// Package textbuf provides a UI-agnostic multiline text buffer for use by a SQL editor.
//
// Design notes:
//   - Each line is stored as a []rune, so an edit is O(n) per line, where n is the line
//     length (fast enough for SQL editing).
//   - Every mutation produces a reversible Edit record, enabling unlimited undo/redo.
//   - It depends on no terminal library, which keeps it easy to unit test and reuse
//     (for example in a result grid or a command-line input box).
package textbuf

import (
	"strings"
	"unicode"
)

// Pos represents a position in the buffer. Row and Col are both 0-based; Col is
// measured in runes.
type Pos struct {
	Row int
	Col int
}

// Less reports whether p is strictly before q.
func (p Pos) Less(q Pos) bool {
	return p.Row < q.Row || (p.Row == q.Row && p.Col < q.Col)
}

type editKind int

const (
	editInsert editKind = iota
	editDelete
	editReplace
)

// Edit describes a single reversible buffer mutation.
type Edit struct {
	Kind    editKind
	At      Pos    // insertion start / deletion start
	Text    []rune // text inserted, deleted or replaced
	NewText []rune // replacement text for editReplace
	Before  Pos    // cursor before the operation
	After   Pos    // cursor after the operation
	Group   int64  // when non-zero, adjacent inserts in the same group merge into one undo step (for continuous typing)
}

// Buffer is a multiline text buffer.
type Buffer struct {
	lines [][]rune
	row   int
	col   int
	goal  int // goal column for vertical movement, avoiding "jitter" on short lines

	undoStack []Edit
	redoStack []Edit
}

// New creates a buffer containing the initial text, with the cursor at the start of
// the text.
func New(text string) *Buffer {
	b := &Buffer{}
	b.SetText(text)
	return b
}

// NewEmpty creates an empty buffer (containing at least one line).
func NewEmpty() *Buffer { return New("") }

// SetText replaces the entire buffer contents with text and clears the undo history.
func (b *Buffer) SetText(text string) {
	b.lines = splitLines(text)
	b.row, b.col, b.goal = 0, 0, 0
	b.undoStack, b.redoStack = nil, nil
}

// Lines returns the total number of lines (an empty buffer still returns 1).
func (b *Buffer) Lines() int { return len(b.lines) }

// Line returns the contents of line row (a read-only view; callers must not modify it).
func (b *Buffer) Line(row int) []rune {
	if row < 0 || row >= len(b.lines) {
		return nil
	}
	return b.lines[row]
}

// LineString returns the contents of line row as a string.
func (b *Buffer) LineString(row int) string {
	return string(b.Line(row))
}

// Text returns the entire buffer text.
func (b *Buffer) Text() string {
	var sb strings.Builder
	for i, ln := range b.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(string(ln))
	}
	return sb.String()
}

// Cursor returns the current cursor position.
func (b *Buffer) Cursor() Pos { return Pos{Row: b.row, Col: b.col} }

// SetCursor moves the cursor to p (clamped to the valid range).
func (b *Buffer) SetCursor(p Pos) {
	b.row = clamp(p.Row, 0, len(b.lines)-1)
	b.col = clamp(p.Col, 0, len(b.lines[b.row]))
	b.goal = b.col
}

// InsertString inserts text at the cursor and moves the cursor after the inserted text.
func (b *Buffer) InsertString(s string) { b.InsertStringGrouped(s, 0) }

// InsertStringGrouped is like InsertString, but when group is non-zero and the insert
// is adjacent to the previous one, it merges with that insert into the same undo unit
// (used to collapse continuous typing into one undo).
func (b *Buffer) InsertStringGrouped(s string, group int64) {
	if s == "" {
		return
	}
	b.apply(editInsert, Pos{b.row, b.col}, []rune(s), group)
}

// InsertRune inserts a single character at the cursor.
func (b *Buffer) InsertRune(r rune) { b.InsertString(string(r)) }

// DeleteBackward deletes the character before the cursor (joining with the previous
// line at the start of a line) and reports whether a deletion occurred.
func (b *Buffer) DeleteBackward() bool {
	if b.col > 0 {
		b.apply(editDelete, Pos{b.row, b.col - 1}, []rune{b.lines[b.row][b.col-1]}, 0)
		return true
	}
	if b.row > 0 {
		prev := b.lines[b.row-1]
		at := Pos{b.row - 1, len(prev)}
		b.apply(editDelete, at, []rune{'\n'}, 0)
		return true
	}
	return false
}

// DeleteForward deletes the character after the cursor (joining with the next line at
// the end of a line) and reports whether a deletion occurred.
func (b *Buffer) DeleteForward() bool {
	line := b.lines[b.row]
	if b.col < len(line) {
		b.apply(editDelete, Pos{b.row, b.col}, []rune{line[b.col]}, 0)
		return true
	}
	if b.row < len(b.lines)-1 {
		b.apply(editDelete, Pos{b.row, b.col}, []rune{'\n'}, 0)
		return true
	}
	return false
}

// DeleteRange deletes the text in [start, end) and returns the deleted contents,
// leaving the cursor at start.
func (b *Buffer) DeleteRange(start, end Pos) string {
	start, end = normalize(b, start, end)
	if start == end {
		return ""
	}
	deleted := b.sliceRange(start, end)
	b.apply(editDelete, start, []rune(deleted), 0)
	return deleted
}

// Slice returns the text in [start, end) (normalizing the order automatically).
func (b *Buffer) Slice(start, end Pos) string {
	start, end = normalize(b, start, end)
	return b.sliceRange(start, end)
}

// ReplaceRange replaces [start, end) with text, leaving the cursor after the
// inserted text. The whole replacement is one undo step.
func (b *Buffer) ReplaceRange(start, end Pos, text string) {
	start, end = normalize(b, start, end)
	if start == end {
		b.InsertString(text)
		return
	}
	old := b.sliceRange(start, end)
	before := b.Cursor()
	b.deleteRaw(start, end)
	after := b.insertRaw(start, []rune(text))
	b.undoStack = append(b.undoStack, Edit{
		Kind:    editReplace,
		At:      start,
		Text:    []rune(old),
		NewText: []rune(text),
		Before:  before,
		After:   after,
	})
	b.redoStack = nil
	b.SetCursor(after)
}

// Undo reverts the last mutation and reports whether there was anything to undo.
func (b *Buffer) Undo() bool {
	if len(b.undoStack) == 0 {
		return false
	}
	e := b.undoStack[len(b.undoStack)-1]
	b.undoStack = b.undoStack[:len(b.undoStack)-1]

	switch e.Kind {
	case editInsert:
		end := posAfter(e.At, e.Text)
		b.deleteRaw(e.At, end)
	case editDelete:
		b.insertRaw(e.At, e.Text)
	case editReplace:
		end := posAfter(e.At, e.NewText)
		b.deleteRaw(e.At, end)
		b.insertRaw(e.At, e.Text)
	}
	b.SetCursor(e.Before)
	b.redoStack = append(b.redoStack, e)
	return true
}

// Redo reapplies the last undone mutation.
func (b *Buffer) Redo() bool {
	if len(b.redoStack) == 0 {
		return false
	}
	e := b.redoStack[len(b.redoStack)-1]
	b.redoStack = b.redoStack[:len(b.redoStack)-1]

	switch e.Kind {
	case editInsert:
		b.insertRaw(e.At, e.Text)
	case editDelete:
		end := posAfter(e.At, e.Text)
		b.deleteRaw(e.At, end)
	case editReplace:
		end := posAfter(e.At, e.Text)
		b.deleteRaw(e.At, end)
		b.insertRaw(e.At, e.NewText)
	}
	b.SetCursor(e.After)
	b.undoStack = append(b.undoStack, e)
	return true
}

// ---- cursor movement ----

// MoveLeft moves left by one cell; when word is true, it jumps by word.
func (b *Buffer) MoveLeft(word bool) {
	if b.col == 0 {
		if b.row == 0 {
			return
		}
		b.row--
		b.col = len(b.lines[b.row])
	} else if word {
		b.col = prevWordBoundary(b.lines[b.row], b.col)
	} else {
		b.col--
	}
	b.goal = b.col
}

// MoveRight moves right by one cell; when word is true, it jumps by word.
func (b *Buffer) MoveRight(word bool) {
	line := b.lines[b.row]
	if b.col >= len(line) {
		if b.row >= len(b.lines)-1 {
			return
		}
		b.row++
		b.col = 0
	} else if word {
		b.col = nextWordBoundary(line, b.col)
	} else {
		b.col++
	}
	b.goal = b.col
}

// MoveUp moves up one line, preserving the goal column.
func (b *Buffer) MoveUp() {
	if b.row == 0 {
		return
	}
	b.row--
	b.col = min(b.goal, len(b.lines[b.row]))
}

// MoveDown moves down one line, preserving the goal column.
func (b *Buffer) MoveDown() {
	if b.row >= len(b.lines)-1 {
		return
	}
	b.row++
	b.col = min(b.goal, len(b.lines[b.row]))
}

// MoveLineHome moves to the start of the line (pressing again switches to the first
// non-whitespace character).
func (b *Buffer) MoveLineHome() {
	line := b.lines[b.row]
	indent := indentWidth(line)
	target := 0
	if b.col == 0 {
		target = indent
	} else if b.col > indent {
		target = indent
	}
	b.col = target
	b.goal = b.col
}

// MoveLineEnd moves to the end of the line.
func (b *Buffer) MoveLineEnd() {
	b.col = len(b.lines[b.row])
	b.goal = b.col
}

// MoveBufferHome moves to the start of the buffer.
func (b *Buffer) MoveBufferHome() { b.SetCursor(Pos{0, 0}) }

// MoveBufferEnd moves to the end of the buffer.
func (b *Buffer) MoveBufferEnd() {
	b.SetCursor(Pos{len(b.lines) - 1, len(b.lines[len(b.lines)-1])})
}

// ---- internal implementation ----

// apply performs a mutation and records undo information.
func (b *Buffer) apply(kind editKind, at Pos, text []rune, group int64) {
	before := b.Cursor()

	// Merge consecutive inserts in the same group (for example, a burst of typing
	// produces a single undo unit).
	if group != 0 && kind == editInsert && len(b.undoStack) > 0 {
		last := &b.undoStack[len(b.undoStack)-1]
		if last.Kind == editInsert && last.Group == group && posAfter(last.At, last.Text) == at {
			after := b.insertRaw(at, text)
			last.Text = append(last.Text, text...)
			last.After = after
			b.redoStack = nil
			b.SetCursor(after)
			return
		}
	}

	var after Pos
	switch kind {
	case editInsert:
		after = b.insertRaw(at, text)
	case editDelete:
		b.deleteRaw(at, posAfter(at, text))
		after = at
	}
	b.undoStack = append(b.undoStack, Edit{Kind: kind, At: at, Text: text, Before: before, After: after, Group: group})
	b.redoStack = nil
	b.SetCursor(after)
}

// insertRaw inserts text at p without recording undo information and returns the
// resulting cursor position.
func (b *Buffer) insertRaw(p Pos, text []rune) Pos {
	p.Row = clamp(p.Row, 0, len(b.lines)-1)
	line := b.lines[p.Row]
	col := clamp(p.Col, 0, len(line))

	head := append([]rune{}, line[:col]...)
	tail := append([]rune{}, line[col:]...)

	// Split text on '\n'; the first part follows head and the last part precedes tail.
	parts := splitRunes(text)
	newLines := make([][]rune, 0, len(parts))
	for _, part := range parts {
		newLines = append(newLines, part)
	}
	newLines[0] = append(head, newLines[0]...)
	last := len(newLines) - 1
	newLines[last] = append(newLines[last], tail...)

	// Reassemble the lines.
	replaced := append([][]rune{}, b.lines[:p.Row]...)
	replaced = append(replaced, newLines...)
	replaced = append(replaced, b.lines[p.Row+1:]...)
	b.lines = replaced

	return Pos{p.Row + last, len(newLines[last]) - len(tail)}
}

// deleteRaw deletes the text in [start, end) without recording undo information.
func (b *Buffer) deleteRaw(start, end Pos) {
	start, end = normalize(b, start, end)
	if start == end {
		return
	}
	head := append([]rune{}, b.lines[start.Row][:start.Col]...)
	tail := append([]rune{}, b.lines[end.Row][end.Col:]...)
	merged := append(head, tail...)

	replaced := append([][]rune{}, b.lines[:start.Row]...)
	replaced = append(replaced, merged)
	replaced = append(replaced, b.lines[end.Row+1:]...)
	b.lines = replaced
}

// sliceRange returns the text in [start, end).
func (b *Buffer) sliceRange(start, end Pos) string {
	if start.Row == end.Row {
		return string(b.lines[start.Row][start.Col:end.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(b.lines[start.Row][start.Col:]))
	for r := start.Row + 1; r < end.Row; r++ {
		sb.WriteByte('\n')
		sb.WriteString(string(b.lines[r]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(b.lines[end.Row][:end.Col]))
	return sb.String()
}

func (b *Buffer) clampPos(p Pos) Pos {
	return Pos{clamp(p.Row, 0, len(b.lines)-1), 0}
}

func normalize(b *Buffer, a, c Pos) (Pos, Pos) {
	a.Row = clamp(a.Row, 0, len(b.lines)-1)
	c.Row = clamp(c.Row, 0, len(b.lines)-1)
	a.Col = clamp(a.Col, 0, len(b.lines[a.Row]))
	c.Col = clamp(c.Col, 0, len(b.lines[c.Row]))
	if c.Less(a) {
		return c, a
	}
	return a, c
}

func posAfter(p Pos, text []rune) Pos {
	parts := splitRunes(text)
	if len(parts) == 1 {
		return Pos{p.Row, p.Col + len(parts[0])}
	}
	return Pos{p.Row + len(parts) - 1, len(parts[len(parts)-1])}
}

func splitLines(text string) [][]rune {
	lines := splitRunes([]rune(strings.ReplaceAll(text, "\r\n", "\n")))
	if len(lines) == 0 {
		return [][]rune{{}}
	}
	return lines
}

// splitRunes splits on '\n' and always returns at least one part.
func splitRunes(r []rune) [][]rune {
	out := make([][]rune, 0, 4)
	cur := make([]rune, 0, len(r))
	for _, c := range r {
		if c == '\n' {
			out = append(out, cur)
			cur = make([]rune, 0, 8)
			continue
		}
		cur = append(cur, c)
	}
	return append(out, cur)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$'
}

func prevWordBoundary(line []rune, col int) int {
	i := col
	for i > 0 && !isWordRune(line[i-1]) {
		i--
	}
	for i > 0 && isWordRune(line[i-1]) {
		i--
	}
	return i
}

func nextWordBoundary(line []rune, col int) int {
	i := col
	for i < len(line) && isWordRune(line[i]) {
		i++
	}
	for i < len(line) && !isWordRune(line[i]) {
		i++
	}
	return i
}

func indentWidth(line []rune) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
