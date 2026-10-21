//go:build windows || darwin

package ui

import "testing"

func TestPaneDropEdge(t *testing.T) {
	// Outer rect 100,50 200×100 (centre 200,100).
	g := paneGeom{pane: &tab{id: 1}, x: 100, w: 200, outerY: 50, outerH: 100, y: 60, h: 80}
	cases := []struct {
		x, y int32
		want string
	}{
		{110, 100, "left"},
		{290, 100, "right"},
		{200, 55, "top"},
		{200, 145, "bottom"},
		// Near a corner, the relatively closer edge wins.
		{105, 60, "left"},    // 2.5% from left, 10% from top
		{150, 52, "top"},     // 25% from left, 2% from top
		{295, 148, "bottom"}, // 2.2% from right, 1.5% from bottom
		// Wide pane: the middle third of the width is still top/bottom.
		{170, 70, "top"},
	}
	for _, tc := range cases {
		if got := paneDropEdge(g, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q want %q", tc.x, tc.y, got, tc.want)
		}
	}
	if paneDropEdge(paneGeom{}, 0, 0) != "" {
		t.Error("empty rect should have no edge")
	}
}

func TestPaneDropRect(t *testing.T) {
	g := paneGeom{x: 10, w: 101, outerY: 20, outerH: 51}
	for _, tc := range []struct {
		edge       string
		x, y, w, h int32
	}{
		{"left", 10, 20, 50, 51},
		{"right", 61, 20, 50, 51},
		{"top", 10, 20, 101, 25},
		{"bottom", 10, 46, 101, 25},
		{"nope", 0, 0, 0, 0},
	} {
		x, y, w, h := paneDropRect(g, tc.edge)
		if x != tc.x || y != tc.y || w != tc.w || h != tc.h {
			t.Errorf("%s: got %d,%d %dx%d want %d,%d %dx%d", tc.edge, x, y, w, h, tc.x, tc.y, tc.w, tc.h)
		}
	}
}

func TestHitPaneTitle(t *testing.T) {
	p := pageVert3()
	layouts := geomsOf(p)
	g := layouts[1]
	if hit := hitPaneTitle(layouts, g.x+5, g.titleY+1); hit == nil || hit.pane.id != g.pane.id {
		t.Fatalf("title bar of pane %d not hit", g.pane.id)
	}
	if hitPaneTitle(layouts, g.x+5, g.y+g.titleH+2) != nil {
		t.Fatal("content area counted as title bar")
	}
	solo := newPage(&tab{id: 7})
	sl := geomsOf(solo)
	if hitPaneTitle(sl, sl[0].x+1, sl[0].outerY+1) != nil {
		t.Fatal("a solo pane has no title bar to drag")
	}
}

func TestPaneDropAt(t *testing.T) {
	p := pageVert3()
	layouts := geomsOf(p)
	src := layouts[0]
	if _, _, ok := paneDropAt(layouts, src.pane.id, src.x+src.w/2, src.outerY+src.outerH/2); ok {
		t.Fatal("dropping on itself should be refused")
	}
	dst := layouts[2]
	id, edge, ok := paneDropAt(layouts, src.pane.id, dst.x+dst.w-2, dst.outerY+dst.outerH/2)
	if !ok || id != dst.pane.id || edge != "right" {
		t.Fatalf("got %d %q %v", id, edge, ok)
	}
	if _, _, ok := paneDropAt(layouts, src.pane.id, -5, -5); ok {
		t.Fatal("outside every pane should be refused")
	}
}

func TestPaneDragClickIsNotADrag(t *testing.T) {
	p := pageVert3()
	layouts := geomsOf(p)
	var d paneDrag
	d.reset()
	d.arm(0, 10, 5)
	if !d.engaged() {
		t.Fatal("arm should engage")
	}
	// Jitter inside the threshold, even over another pane's half.
	if d.move(layouts, 10+paneDragThreshold-2, 5+2) {
		t.Fatal("small move should not start a drag")
	}
	if _, _, _, ok := d.release(); ok {
		t.Fatal("a click must not move the pane")
	}
	if d.engaged() || d.active {
		t.Fatal("release should clear state")
	}
}

func TestPaneDragDropFlow(t *testing.T) {
	p := pageVert3()
	layouts := geomsOf(p)
	src, dst := layouts[0], layouts[2]
	var d paneDrag
	d.arm(src.pane.id, src.x+4, src.titleY+1)
	// Over its own pane: dragging, but nothing to drop on.
	if d.move(layouts, src.x+40, src.outerY+40) {
		t.Fatal("no target over the source pane; nothing to repaint")
	}
	if !d.active {
		t.Fatal("past threshold should be a drag")
	}
	if _, _, _, _, ok := d.highlight(layouts); ok {
		t.Fatal("no highlight over the source pane")
	}
	// Over the bottom of pane 2.
	if !d.move(layouts, dst.x+dst.w/2, dst.outerY+dst.outerH-3) {
		t.Fatal("new target should ask for a repaint")
	}
	if d.move(layouts, dst.x+dst.w/2+1, dst.outerY+dst.outerH-3) {
		t.Fatal("same target/edge should not repaint again")
	}
	hx, hy, hw, hh, ok := d.highlight(layouts)
	wx, wy, ww, wh := paneDropRect(dst, "bottom")
	if !ok || hx != wx || hy != wy || hw != ww || hh != wh {
		t.Fatalf("highlight %d,%d %dx%d want %d,%d %dx%d", hx, hy, hw, hh, wx, wy, ww, wh)
	}
	id, target, edge, ok := d.release()
	if !ok || id != src.pane.id || target != dst.pane.id || edge != "bottom" {
		t.Fatalf("release %d→%d %q %v", id, target, edge, ok)
	}
}

func TestPaneDragReleaseOffTargetCancels(t *testing.T) {
	p := pageVert3()
	layouts := geomsOf(p)
	dst := layouts[1]
	var d paneDrag
	d.arm(0, 5, 5)
	d.move(layouts, dst.x+3, dst.outerY+dst.outerH/2)
	d.move(layouts, 5000, 5000) // off every pane
	if _, _, _, ok := d.release(); ok {
		t.Fatal("drop outside the panes should not move anything")
	}
}

func TestHostDropPane(t *testing.T) {
	p := pageVert3()
	p.setFocus(1)
	h := &fakeMoveHost{pages: []*page{p}}
	hostDropPane(h, 2, 0, "top")
	if h.after != 1 || p.focusID != 2 || !sameIDs(paneIDs(p), []int{2, 0, 1}) {
		t.Fatalf("after=%d focus=%d leaves=%v", h.after, p.focusID, paneIDs(p))
	}
	hostDropPane(h, 2, 2, "left")
	if h.after != 1 || len(h.toasts) != 1 {
		t.Fatalf("drop on self: after=%d toasts=%v", h.after, h.toasts)
	}
}
