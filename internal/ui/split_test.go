//go:build windows || darwin

package ui

import "testing"

func TestSplitFocusedAndRemove(t *testing.T) {
	a := &tab{id: 0, title: "a"}
	b := &tab{id: 1, title: "b"}
	c := &tab{id: 2, title: "c"}
	p := newPage(a)
	if p.leafCount() != 1 {
		t.Fatalf("want 1 leaf, got %d", p.leafCount())
	}
	if !p.splitFocused(splitVert, b) {
		t.Fatal("split vert failed")
	}
	if p.leafCount() != 2 {
		t.Fatalf("want 2 leaves, got %d", p.leafCount())
	}
	if p.focusID != 1 {
		t.Fatalf("focus want 1 got %d", p.focusID)
	}
	// Focus a, split down.
	p.setFocus(0)
	if !p.splitFocused(splitHoriz, c) {
		t.Fatal("split horiz failed")
	}
	if p.leafCount() != 3 {
		t.Fatalf("want 3 leaves, got %d", p.leafCount())
	}
	closed, empty, focus := p.removePane(2)
	if closed != c || empty {
		t.Fatalf("remove c: closed=%v empty=%v", closed, empty)
	}
	if p.leafCount() != 2 {
		t.Fatalf("want 2 after remove, got %d", p.leafCount())
	}
	_ = focus
	closed, empty, _ = p.removePane(0)
	if closed != a || empty {
		t.Fatalf("remove a: closed=%v empty=%v", closed, empty)
	}
	if p.leafCount() != 1 {
		t.Fatalf("want 1 leaf, got %d", p.leafCount())
	}
	closed, empty, _ = p.removePane(1)
	if closed != b || !empty {
		t.Fatalf("remove last: closed=%v empty=%v", closed, empty)
	}
}

func TestLayoutEqualSplit(t *testing.T) {
	a := &tab{id: 0}
	b := &tab{id: 1}
	p := newPage(a)
	p.splitFocused(splitVert, b)
	res := layoutPage(p.root, 0, 0, 200, 100, 10, 10, p.focusID)
	geoms := res.leaves
	if len(geoms) != 2 {
		t.Fatalf("geoms=%d", len(geoms))
	}
	if len(res.sashes) != 1 {
		t.Fatalf("want 1 sash, got %d", len(res.sashes))
	}
	// Equal split with shared gap: wA + gap + wB = 200
	if geoms[0].w+geoms[1].w+paneGapPx != 200 && geoms[0].w+geoms[1].w != 200 {
		t.Fatalf("widths %d+%d gap=%d", geoms[0].w, geoms[1].w, paneGapPx)
	}
	if geoms[0].cols < 1 || geoms[1].cols < 1 {
		t.Fatalf("cols %d %d", geoms[0].cols, geoms[1].cols)
	}
	// Multi-pane leaves reserve a title strip.
	if geoms[0].titleH != 10 || geoms[1].titleH != 10 {
		t.Fatalf("titleH want 10 got %d %d", geoms[0].titleH, geoms[1].titleH)
	}
	if geoms[0].y != geoms[0].titleY+geoms[0].titleH {
		t.Fatalf("VT origin should sit below title")
	}
	// Non-alt panes reserve an input bar at the leaf bottom.
	if geoms[0].barH < 1 || geoms[1].barH < 1 {
		t.Fatalf("expected per-pane input bars, barH=%d %d", geoms[0].barH, geoms[1].barH)
	}
	if geoms[0].barY+geoms[0].barH != geoms[0].outerY+geoms[0].outerH {
		t.Fatalf("bar should sit at outer bottom")
	}
}

func TestSashDragUpdatesRatio(t *testing.T) {
	a := &tab{id: 0}
	b := &tab{id: 1}
	p := newPage(a)
	p.splitFocused(splitVert, b)
	res := layoutPage(p.root, 0, 0, 200, 100, 10, 10, p.focusID)
	if len(res.sashes) != 1 {
		t.Fatalf("sashes=%d", len(res.sashes))
	}
	s := res.sashes[0]
	// Drag toward left → smaller left ratio.
	applySashDrag(s, 60, 50)
	if s.node.ratio >= 0.5 {
		t.Fatalf("ratio after left drag=%v want <0.5", s.node.ratio)
	}
	applySashDrag(s, 160, 50)
	if s.node.ratio <= 0.5 {
		t.Fatalf("ratio after right drag=%v want >0.5", s.node.ratio)
	}
	// Hit test with padding.
	if hitSash(res.sashes, s.x+s.w/2, s.y+s.h/2) != 0 {
		t.Fatal("center sash miss")
	}
	if hitSash(res.sashes, s.x-2, s.y+s.h/2) != 0 {
		t.Fatal("padded sash miss")
	}
}

func TestFocusNeighbor(t *testing.T) {
	a := &tab{id: 0}
	b := &tab{id: 1}
	p := newPage(a)
	p.splitFocused(splitVert, b) // a left, b right; focus b
	geoms := layoutPage(p.root, 0, 0, 200, 100, 10, 10, p.focusID).leaves
	// Focus b, move left → a
	if !p.focusNeighbor(0, geoms) {
		t.Fatal("focus left failed")
	}
	if p.focusID != 0 {
		t.Fatalf("want focus 0 got %d", p.focusID)
	}
	if !p.focusNeighbor(1, geoms) {
		t.Fatal("focus right failed")
	}
	if p.focusID != 1 {
		t.Fatalf("want focus 1 got %d", p.focusID)
	}
}

// hitPane returns a layout index; click-to-focus must use layouts[i].pane.id
// (not the index). Regression: macOS once passed the index into setFocus,
// which fails when pane ids are not 0,1,2…
func TestHitPaneClickFocusUsesPaneID(t *testing.T) {
	a := &tab{id: 10}
	b := &tab{id: 20}
	p := newPage(a)
	if !p.splitFocused(splitVert, b) {
		t.Fatal("split")
	}
	// After split, focus is on b (20).
	if p.focusID != 20 {
		t.Fatalf("focus after split=%d want 20", p.focusID)
	}
	geoms := layoutPage(p.root, 0, 0, 200, 100, 10, 10, p.focusID).leaves
	if len(geoms) != 2 {
		t.Fatalf("leaves=%d", len(geoms))
	}
	// Click the left pane (a, id 10).
	var left *paneGeom
	for i := range geoms {
		if geoms[i].pane != nil && geoms[i].pane.id == 10 {
			left = &geoms[i]
			break
		}
	}
	if left == nil {
		t.Fatal("left pane missing")
	}
	hi := hitPane(geoms, left.x+1, left.outerY+1)
	if hi < 0 {
		t.Fatal("hitPane miss")
	}
	// Index is not the pane id when ids are 10/20.
	if hi == 10 || hi == 20 {
		t.Fatalf("hitPane returned id-like index %d (test assumption broken)", hi)
	}
	if geoms[hi].pane == nil || geoms[hi].pane.id != 10 {
		t.Fatalf("hit index %d pane=%v", hi, geoms[hi].pane)
	}
	// Wrong: setFocus(layout index) must not steal focus to a missing id.
	if p.setFocus(hi) {
		t.Fatalf("setFocus(%d) should fail (no pane with that id)", hi)
	}
	if p.focusID != 20 {
		t.Fatalf("focus changed after bad setFocus: %d", p.focusID)
	}
	// Right: setFocus(pane.id).
	if !p.setFocus(geoms[hi].pane.id) {
		t.Fatal("setFocus(pane.id) failed")
	}
	if p.focusID != 10 {
		t.Fatalf("focus=%d want 10", p.focusID)
	}
}
