package textbuf

import "testing"

func TestNewAndText(t *testing.T) {
	b := New("select *\nfrom users")
	if b.Lines() != 2 {
		t.Fatalf("Lines() = %d, want 2", b.Lines())
	}
	if got := b.Text(); got != "select *\nfrom users" {
		t.Fatalf("Text() = %q", got)
	}
	if c := b.Cursor(); c != (Pos{0, 0}) {
		t.Fatalf("Cursor() = %v", c)
	}
}

func TestEmptyBufferHasOneLine(t *testing.T) {
	b := NewEmpty()
	if b.Lines() != 1 {
		t.Fatalf("Lines() = %d, want 1", b.Lines())
	}
	b.InsertString("x")
	if got := b.Text(); got != "x" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestInsertMultiline(t *testing.T) {
	b := New("ab")
	b.SetCursor(Pos{0, 1})
	b.InsertString("1\n22\n333")
	if got := b.Text(); got != "a1\n22\n333b" {
		t.Fatalf("Text() = %q", got)
	}
	if c := b.Cursor(); c != (Pos{2, 3}) {
		t.Fatalf("Cursor() = %v, want {2 3}", c)
	}
}

func TestDeleteBackwardJoinLines(t *testing.T) {
	b := New("abc\ndef")
	b.SetCursor(Pos{1, 0})
	if !b.DeleteBackward() {
		t.Fatal("DeleteBackward() = false")
	}
	if got := b.Text(); got != "abcdef" {
		t.Fatalf("Text() = %q", got)
	}
	if c := b.Cursor(); c != (Pos{0, 3}) {
		t.Fatalf("Cursor() = %v", c)
	}
}

func TestDeleteForwardJoinLines(t *testing.T) {
	b := New("abc\ndef")
	b.SetCursor(Pos{0, 3})
	b.DeleteForward()
	if got := b.Text(); got != "abcdef" {
		t.Fatalf("Text() = %q", got)
	}
	if c := b.Cursor(); c != (Pos{0, 3}) {
		t.Fatalf("Cursor() = %v", c)
	}
}

func TestUndoRedo(t *testing.T) {
	b := New("select 1")
	b.SetCursor(Pos{0, 8})
	b.InsertString("23")
	b.DeleteBackward()
	if got := b.Text(); got != "select 12" {
		t.Fatalf("after edit Text() = %q", got)
	}
	if !b.Undo() {
		t.Fatal("Undo() = false")
	}
	if got := b.Text(); got != "select 123" {
		t.Fatalf("after undo Text() = %q", got)
	}
	if !b.Undo() {
		t.Fatal("Undo() = false")
	}
	if got := b.Text(); got != "select 1" {
		t.Fatalf("after second undo Text() = %q", got)
	}
	if b.Undo() {
		t.Fatal("Undo() = true, want stack empty")
	}
	if !b.Redo() {
		t.Fatal("Redo() = false")
	}
	if got := b.Text(); got != "select 123" {
		t.Fatalf("after redo Text() = %q", got)
	}
	if !b.Redo() {
		t.Fatal("Redo() = false")
	}
	if got := b.Text(); got != "select 12" {
		t.Fatalf("after second redo Text() = %q", got)
	}
	if c := b.Cursor(); c != (Pos{0, 9}) {
		t.Fatalf("Cursor() = %v", c)
	}
}

func TestUndoMultilineInsert(t *testing.T) {
	b := New("hello")
	b.SetCursor(Pos{0, 5})
	b.InsertString("\nworld\n!")
	if got := b.Text(); got != "hello\nworld\n!" {
		t.Fatalf("Text() = %q", got)
	}
	b.Undo()
	if got := b.Text(); got != "hello" {
		t.Fatalf("after undo Text() = %q", got)
	}
	b.Redo()
	if got := b.Text(); got != "hello\nworld\n!" {
		t.Fatalf("after redo Text() = %q", got)
	}
}

func TestDeleteRange(t *testing.T) {
	b := New("abcdef")
	got := b.DeleteRange(Pos{0, 1}, Pos{0, 4})
	if got != "bcd" || b.Text() != "aef" {
		t.Fatalf("DeleteRange = %q, text = %q", got, b.Text())
	}
	b.Undo()
	if b.Text() != "abcdef" {
		t.Fatalf("after undo Text() = %q", b.Text())
	}
}

func TestDeleteRangeMultiline(t *testing.T) {
	b := New("one\ntwo\nthree")
	got := b.DeleteRange(Pos{0, 2}, Pos{2, 2})
	if got != "e\ntwo\nth" {
		t.Fatalf("DeleteRange = %q", got)
	}
	if b.Text() != "onree" {
		t.Fatalf("Text() = %q", b.Text())
	}
}

func TestWordMovement(t *testing.T) {
	b := New("select * from users u")
	b.SetCursor(Pos{0, 21}) // end of line
	b.MoveLeft(true)
	if b.Cursor().Col != 20 {
		t.Fatalf("col = %d, want 20", b.Cursor().Col)
	}
	b.MoveLeft(true)
	if b.Cursor().Col != 14 {
		t.Fatalf("col = %d, want 14 (start of users)", b.Cursor().Col)
	}
	b.MoveRight(true)
	if b.Cursor().Col != 20 {
		t.Fatalf("col = %d, want 20", b.Cursor().Col)
	}
}

func TestVerticalMovementKeepsGoalColumn(t *testing.T) {
	b := New("abcdefgh\nxy\nijklmnop")
	b.SetCursor(Pos{0, 6})
	b.MoveDown()
	if c := b.Cursor(); c != (Pos{1, 2}) {
		t.Fatalf("Cursor() = %v, want {1 2}", c)
	}
	b.MoveDown()
	if c := b.Cursor(); c != (Pos{2, 6}) {
		t.Fatalf("Cursor() = %v, want {2 6} (goal column preserved)", c)
	}
}

func TestReplaceRange(t *testing.T) {
	b := New("select * form users")
	b.ReplaceRange(Pos{0, 9}, Pos{0, 13}, "from")
	if b.Text() != "select * from users" {
		t.Fatalf("Text() = %q", b.Text())
	}
	if c := b.Cursor(); c != (Pos{0, 13}) {
		t.Fatalf("Cursor() = %v", c)
	}
}

func TestReplaceRangeIsOneUndoStep(t *testing.T) {
	b := New("select * form users")
	b.ReplaceRange(Pos{0, 9}, Pos{0, 13}, "from")
	if !b.Undo() {
		t.Fatal("Undo() = false")
	}
	if got := b.Text(); got != "select * form users" {
		t.Fatalf("one undo must restore the whole line, got %q", got)
	}
	if !b.Redo() {
		t.Fatal("Redo() = false")
	}
	if got := b.Text(); got != "select * from users" {
		t.Fatalf("after redo Text() = %q", got)
	}
}

func TestReplaceRangeMultilineUndo(t *testing.T) {
	b := New("one\ntwo\nthree")
	b.ReplaceRange(Pos{0, 0}, Pos{2, 5}, "SELECT\n  1")
	if got := b.Text(); got != "SELECT\n  1" {
		t.Fatalf("Text() = %q", got)
	}
	if !b.Undo() {
		t.Fatal("Undo() = false")
	}
	if got := b.Text(); got != "one\ntwo\nthree" {
		t.Fatalf("after undo Text() = %q", got)
	}
	if !b.Redo() {
		t.Fatal("Redo() = false")
	}
	if got := b.Text(); got != "SELECT\n  1" {
		t.Fatalf("after redo Text() = %q", got)
	}
}

func TestSlice(t *testing.T) {
	b := New("select * from users")
	if got := b.Slice(Pos{0, 7}, Pos{0, 11}); got != "* fr" {
		t.Fatalf("Slice = %q", got)
	}
	// Works even when the order is reversed.
	if got := b.Slice(Pos{0, 11}, Pos{0, 7}); got != "* fr" {
		t.Fatalf("Slice(reversed) = %q", got)
	}
	// Across lines.
	b2 := New("one\ntwo")
	if got := b2.Slice(Pos{0, 2}, Pos{1, 2}); got != "e\ntw" {
		t.Fatalf("Slice(multiline) = %q", got)
	}
}

func TestClampedCursor(t *testing.T) {
	b := New("ab\ncd")
	b.SetCursor(Pos{10, 10})
	if c := b.Cursor(); c != (Pos{1, 2}) {
		t.Fatalf("Cursor() = %v, want {1 2}", c)
	}
}
