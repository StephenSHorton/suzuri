//go:build windows || darwin

package ui

import (
	"math"
	"testing"
)

const (
	dirLeft = iota
	dirRight
	dirUp
	dirDown
)

func geomsOf(p *page) []paneGeom {
	return layoutPageZoom(p.root, 0, 0, 400, 200, 10, 10, p.focusID, p.zoomTarget()).leaves
}

// pageVert3 is 0 | 1 | 2 (tree 0 | (1 | 2)).
func pageVert3() *page {
	p := newPage(&tab{id: 0, title: "t0"})
	p.splitFocused(splitVert, &tab{id: 1, title: "t1"})
	p.splitFocused(splitVert, &tab{id: 2, title: "t2"})
	return p
}

// pageGrid is a 2×2 grid: (0 / 2) | (1 / 3).
func pageGrid() *page {
	p := newPage(&tab{id: 0, title: "t0"})
	p.splitFocused(splitVert, &tab{id: 1, title: "t1"})
	p.setFocus(0)
	p.splitFocused(splitHoriz, &tab{id: 2, title: "t2"})
	p.setFocus(1)
	p.splitFocused(splitHoriz, &tab{id: 3, title: "t3"})
	return p
}

// pageLeftTall is 0 | (1 / 2).
func pageLeftTall() *page {
	p := newPage(&tab{id: 0, title: "t0"})
	p.splitFocused(splitVert, &tab{id: 1, title: "t1"})
	p.splitFocused(splitHoriz, &tab{id: 2, title: "t2"})
	return p
}

func ratios(n *splitNode) []float64 {
	if n == nil || n.isLeaf() {
		return nil
	}
	out := []float64{n.ratio}
	out = append(out, ratios(n.a)...)
	return append(out, ratios(n.b)...)
}

func TestSwapNeighbor(t *testing.T) {
	cases := []struct {
		name  string
		build func() *page
		focus int
		dirs  []int
		want  []int // leaf order afterwards
		moved []bool
	}{
		{"single pane", func() *page { return newPage(&tab{id: 0}) }, 0, []int{dirLeft, dirRight, dirUp, dirDown}, []int{0}, []bool{false, false, false, false}},
		{"two side by side right", func() *page {
			p := newPage(&tab{id: 0})
			p.splitFocused(splitVert, &tab{id: 1})
			return p
		}, 0, []int{dirRight}, []int{1, 0}, []bool{true}},
		{"two side by side: right edge is a no-op", func() *page {
			p := newPage(&tab{id: 0})
			p.splitFocused(splitVert, &tab{id: 1})
			return p
		}, 1, []int{dirRight, dirUp, dirDown}, []int{0, 1}, []bool{false, false, false}},
		{"two stacked down then up", func() *page {
			p := newPage(&tab{id: 0})
			p.splitFocused(splitHoriz, &tab{id: 1})
			return p
		}, 0, []int{dirDown, dirUp}, []int{0, 1}, []bool{true, true}},
		{"three across walks to the far end", pageVert3, 0, []int{dirRight, dirRight, dirRight}, []int{1, 2, 0}, []bool{true, true, false}},
		{"three across walks back", pageVert3, 2, []int{dirLeft, dirLeft, dirLeft}, []int{2, 0, 1}, []bool{true, true, false}},
		{"tall left into right column then down", pageLeftTall, 0, []int{dirRight, dirDown}, []int{1, 2, 0}, []bool{true, true}},
		{"right column bottom moves left", pageLeftTall, 2, []int{dirLeft}, []int{2, 1, 0}, []bool{true}},
		{"grid loop brings the pane home, others rotate", pageGrid, 0, []int{dirRight, dirDown, dirLeft, dirUp}, []int{0, 1, 3, 2}, []bool{true, true, true, true}},
		{"grid top-left edges", pageGrid, 0, []int{dirLeft, dirUp}, []int{0, 2, 1, 3}, []bool{false, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.build()
			p.setFocus(tc.focus)
			before := ratios(p.root)
			for i, d := range tc.dirs {
				got := p.swapNeighbor(d, geomsOf(p))
				if got != tc.moved[i] {
					t.Fatalf("step %d dir %d: moved=%v want %v (leaves %v)", i, d, got, tc.moved[i], paneIDs(p))
				}
				if p.focusID != tc.focus {
					t.Fatalf("step %d: focus moved to %d, want it to stay on %d", i, p.focusID, tc.focus)
				}
			}
			if !sameIDs(paneIDs(p), tc.want) {
				t.Fatalf("leaves %v want %v", paneIDs(p), tc.want)
			}
			after := ratios(p.root)
			if len(before) != len(after) {
				t.Fatalf("tree shape changed: %v → %v", before, after)
			}
			for i := range before {
				if math.Abs(before[i]-after[i]) > 1e-9 {
					t.Fatalf("ratios changed: %v → %v", before, after)
				}
			}
		})
	}
}

func TestSwapNeighborKeepsRatiosAndTitle(t *testing.T) {
	p := pageVert3()
	p.root.ratio = 0.3
	p.root.b.ratio = 0.7
	p.stickyTitle = "work"
	p.setFocus(0)
	if !p.swapNeighbor(dirRight, geomsOf(p)) {
		t.Fatal("swap failed")
	}
	if p.root.ratio != 0.3 || p.root.b.ratio != 0.7 {
		t.Fatalf("ratios %v", ratios(p.root))
	}
	if p.stickyTitle != "work" {
		t.Fatalf("strip title changed to %q", p.stickyTitle)
	}
	// The pane takes the slot, not the slot's size: 1 now holds the 30% slot.
	g := geomsOf(p)
	if g[0].pane.id != 1 || g[1].pane.id != 0 {
		t.Fatalf("geometry order %d,%d", g[0].pane.id, g[1].pane.id)
	}
}

func TestSwapNeighborZoomedIsNoop(t *testing.T) {
	p := pageVert3()
	p.setFocus(0)
	p.toggleZoom()
	if p.swapNeighbor(dirRight, geomsOf(p)) {
		t.Fatal("zoomed page should not rearrange")
	}
	if !sameIDs(paneIDs(p), []int{0, 1, 2}) {
		t.Fatalf("leaves %v", paneIDs(p))
	}
}

func TestNeighborIDMissingPane(t *testing.T) {
	p := pageVert3()
	if id := neighborID(geomsOf(p), 42, dirRight); id != -1 {
		t.Fatalf("unknown pane got neighbour %d", id)
	}
}

func TestMovePaneBesideSameTab(t *testing.T) {
	cases := []struct {
		name         string
		pane, target int
		edge         string
		want         []int
	}{
		{"far right to left of first", 2, 0, "left", []int{2, 0, 1}},
		{"first to right of last", 0, 2, "right", []int{1, 2, 0}},
		{"middle under first", 1, 0, "bottom", []int{0, 1, 2}},
		{"up alias", 2, 1, "up", []int{0, 2, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := pageVert3()
			p.stickyTitle = "work"
			pages := []*page{p}
			out, dst, ok, why := movePaneBeside(pages, tc.pane, tc.target, tc.edge)
			if !ok {
				t.Fatalf("move failed: %s", why)
			}
			if len(out) != 1 || dst != p {
				t.Fatalf("pages=%d dst=%p", len(out), dst)
			}
			if !sameIDs(paneIDs(p), tc.want) {
				t.Fatalf("leaves %v want %v", paneIDs(p), tc.want)
			}
			if p.focusID != tc.pane {
				t.Fatalf("focus %d want %d", p.focusID, tc.pane)
			}
			if p.stickyTitle != "work" {
				t.Fatalf("strip title %q", p.stickyTitle)
			}
		})
	}
}

func TestMovePaneBesideTwoPanesKeepsTitle(t *testing.T) {
	// Removing one of two panes briefly leaves a solo page; the strip name
	// must survive that.
	p := newPage(&tab{id: 0, title: "a"})
	p.splitFocused(splitVert, &tab{id: 1, title: "b"})
	want := p.stickyTitle
	if want == "" {
		t.Fatal("setup: expected a frozen strip title")
	}
	_, _, ok, why := movePaneBeside([]*page{p}, 0, 1, "bottom")
	if !ok {
		t.Fatal(why)
	}
	if p.stickyTitle != want || p.root.dir != splitHoriz || !sameIDs(paneIDs(p), []int{1, 0}) {
		t.Fatalf("title=%q dir=%v leaves=%v", p.stickyTitle, p.root.dir, paneIDs(p))
	}
}

func TestMovePaneBesideRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pane, target int
		edge         string
	}{
		{"same pane", 1, 1, "left"},
		{"bad edge", 1, 0, "middle"},
		{"no such pane", 9, 0, "left"},
		{"no such target", 1, 9, "left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := pageVert3()
			pages := []*page{p}
			out, _, ok, _ := movePaneBeside(pages, tc.pane, tc.target, tc.edge)
			if ok {
				t.Fatal("want failure")
			}
			if len(out) != 1 || !sameIDs(paneIDs(p), []int{0, 1, 2}) {
				t.Fatalf("pages changed: %v", paneIDs(p))
			}
		})
	}
}

func TestMovePaneBesideAcrossTabsDropsEmptyTab(t *testing.T) {
	pages, a, b := twoTabs()
	out, dst, ok, why := movePaneBeside(pages, 2, 0, "top")
	if !ok {
		t.Fatal(why)
	}
	if len(out) != 1 || out[0] != a || dst != a || pageIndex(out, b) >= 0 {
		t.Fatalf("want only tab A left; got %d tabs", len(out))
	}
	if !sameIDs(paneIDs(a), []int{2, 0, 1}) || a.focusID != 2 {
		t.Fatalf("A=%v focus=%d", paneIDs(a), a.focusID)
	}
}

func TestAIMoveKeepsUserTab(t *testing.T) {
	pages, a, b := twoTabs()
	active := 1 // user is on B
	next := 10
	s := aiSurface{pages: &pages, active: &active, nextID: &next}
	if !movePane(s, 1, 2, "left") {
		t.Fatal("move failed")
	}
	if pages[active] != b {
		t.Fatalf("user pulled off tab B (active=%d)", active)
	}
	if !sameIDs(paneIDs(a), []int{0}) || !sameIDs(paneIDs(b), []int{1, 2}) {
		t.Fatalf("A=%v B=%v", paneIDs(a), paneIDs(b))
	}
	// Moving the user's only pane away takes them to where it went.
	active = 1
	if !movePane(s, 1, 0, "right") || !movePane(s, 2, 0, "bottom") {
		t.Fatal("move failed")
	}
	if len(pages) != 1 || active != 0 {
		t.Fatalf("pages=%d active=%d", len(pages), active)
	}
}

type fakeMoveHost struct {
	pages  []*page
	active int
	next   int
	toasts []string
	after  int
}

func (h *fakeMoveHost) pageState() (*[]*page, *int, *int) { return &h.pages, &h.active, &h.next }
func (h *fakeMoveHost) toast(msg string)                  { h.toasts = append(h.toasts, msg) }
func (h *fakeMoveHost) afterPageMove()                    { h.after++ }
func (h *fakeMoveHost) activeLayout() []paneGeom {
	return geomsOf(h.pages[h.active])
}

func TestHostMovePaneDir(t *testing.T) {
	p := pageVert3()
	p.setFocus(1)
	h := &fakeMoveHost{pages: []*page{p}}
	hostMovePaneDir(h, dirLeft)
	if !sameIDs(paneIDs(p), []int{1, 0, 2}) || h.after != 1 || p.focusID != 1 {
		t.Fatalf("leaves %v after=%d focus=%d", paneIDs(p), h.after, p.focusID)
	}
	hostMovePaneDir(h, dirLeft) // edge
	hostMovePaneDir(h, dirUp)   // nothing above
	if h.after != 1 || len(h.toasts) != 0 {
		t.Fatalf("edge should be silent no-op: after=%d toasts=%v", h.after, h.toasts)
	}
	p.toggleZoom()
	hostMovePaneDir(h, dirRight)
	if h.after != 1 || len(h.toasts) != 1 {
		t.Fatalf("zoomed: after=%d toasts=%v", h.after, h.toasts)
	}
	solo := &fakeMoveHost{pages: []*page{newPage(&tab{id: 5})}}
	hostMovePaneDir(solo, dirRight)
	if solo.after != 0 || len(solo.toasts) != 0 {
		t.Fatal("solo tab should be a silent no-op")
	}
}
