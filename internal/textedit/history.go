// Package textedit provides a small shared undo/redo stack for text surfaces
// (notes body, workspace compose, and future channel editors).
package textedit

// Snapshot is one restorable editor state (text + caret/selection).
type Snapshot struct {
	Text   []rune
	Cursor int
	Sel    int // selection anchor; -1 = none
}

// History is a linear undo/redo stack. Call Push before each mutating edit.
// Undo restores the previous snapshot and moves the current state onto redo.
type History struct {
	past   []Snapshot
	future []Snapshot
	limit  int
}

// NewHistory returns an empty history. limit caps undo depth (0 → 200).
func NewHistory(limit int) *History {
	if limit <= 0 {
		limit = 200
	}
	return &History{limit: limit}
}

// Clear drops all undo/redo frames (e.g. when switching documents).
func (h *History) Clear() {
	if h == nil {
		return
	}
	h.past = nil
	h.future = nil
}

// Push records before-state so the next mutation can be undone.
// Consecutive identical snapshots are coalesced.
func (h *History) Push(s Snapshot) {
	if h == nil {
		return
	}
	s = cloneSnapshot(s)
	if n := len(h.past); n > 0 && snapshotsEqual(h.past[n-1], s) {
		return
	}
	h.past = append(h.past, s)
	if len(h.past) > h.limit {
		// Drop oldest.
		copy(h.past, h.past[1:])
		h.past = h.past[:h.limit]
	}
	// New branch discards redo.
	h.future = nil
}

// CanUndo reports whether Undo would change state.
func (h *History) CanUndo() bool {
	return h != nil && len(h.past) > 0
}

// CanRedo reports whether Redo would change state.
func (h *History) CanRedo() bool {
	return h != nil && len(h.future) > 0
}

// Undo pops the last pre-edit state. current is pushed onto redo.
// ok is false when there is nothing to undo.
func (h *History) Undo(current Snapshot) (prev Snapshot, ok bool) {
	if h == nil || len(h.past) == 0 {
		return Snapshot{}, false
	}
	prev = h.past[len(h.past)-1]
	h.past = h.past[:len(h.past)-1]
	h.future = append(h.future, cloneSnapshot(current))
	return cloneSnapshot(prev), true
}

// Redo reapplies a previously undone state. current is pushed onto undo.
func (h *History) Redo(current Snapshot) (next Snapshot, ok bool) {
	if h == nil || len(h.future) == 0 {
		return Snapshot{}, false
	}
	next = h.future[len(h.future)-1]
	h.future = h.future[:len(h.future)-1]
	h.past = append(h.past, cloneSnapshot(current))
	return cloneSnapshot(next), true
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := Snapshot{Cursor: s.Cursor, Sel: s.Sel}
	if len(s.Text) > 0 {
		out.Text = append([]rune(nil), s.Text...)
	}
	return out
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Cursor != b.Cursor || a.Sel != b.Sel || len(a.Text) != len(b.Text) {
		return false
	}
	for i := range a.Text {
		if a.Text[i] != b.Text[i] {
			return false
		}
	}
	return true
}
