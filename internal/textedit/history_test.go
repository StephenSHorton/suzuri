package textedit

import "testing"

func TestUndoRedoRoundTrip(t *testing.T) {
	h := NewHistory(10)
	s0 := Snapshot{Text: []rune("hi"), Cursor: 2, Sel: -1}
	h.Push(s0)
	s1 := Snapshot{Text: []rune("hi!"), Cursor: 3, Sel: -1}
	prev, ok := h.Undo(s1)
	if !ok {
		t.Fatal("expected undo")
	}
	if string(prev.Text) != "hi" || prev.Cursor != 2 {
		t.Fatalf("undo got %q cur=%d", string(prev.Text), prev.Cursor)
	}
	// After undo, redo restores s1.
	next, ok := h.Redo(prev)
	if !ok {
		t.Fatal("expected redo")
	}
	if string(next.Text) != "hi!" {
		t.Fatalf("redo got %q", string(next.Text))
	}
}

func TestPushClearsRedo(t *testing.T) {
	h := NewHistory(10)
	h.Push(Snapshot{Text: []rune("a"), Cursor: 1, Sel: -1})
	mid := Snapshot{Text: []rune("ab"), Cursor: 2, Sel: -1}
	_, _ = h.Undo(mid)
	h.Push(Snapshot{Text: []rune("ac"), Cursor: 2, Sel: -1})
	if h.CanRedo() {
		t.Fatal("push after undo must clear redo")
	}
}

func TestCoalesceIdenticalPush(t *testing.T) {
	h := NewHistory(10)
	s := Snapshot{Text: []rune("x"), Cursor: 1, Sel: -1}
	h.Push(s)
	h.Push(s)
	if len(h.past) != 1 {
		t.Fatalf("coalesce want 1 got %d", len(h.past))
	}
}
