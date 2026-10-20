//go:build windows || darwin

package ui

import "testing"

func paneIDs(p *page) []int {
	var ids []int
	for _, t := range p.leaves() {
		ids = append(ids, t.id)
	}
	return ids
}

func sameIDs(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// twoTabs builds tab A = [0 | 1] and tab B = [2].
func twoTabs() (pages []*page, a, b *page) {
	a = newPage(&tab{id: 0, title: "a0"})
	a.splitFocused(splitVert, &tab{id: 1, title: "a1"})
	b = newPage(&tab{id: 2, title: "b2"})
	return []*page{a, b}, a, b
}

func TestMovePaneToExistingTab(t *testing.T) {
	pages, a, b := twoTabs()
	res, ok, why := movePaneToPage(pages, 1, b, -1, 99)
	if !ok {
		t.Fatalf("move failed: %s", why)
	}
	if len(res.pages) != 2 || res.pages[res.active] != b || res.focus != 1 {
		t.Fatalf("want 2 tabs, B active, focus 1; got %d tabs active=%d focus=%d", len(res.pages), res.active, res.focus)
	}
	if !sameIDs(paneIDs(a), []int{0}) || !sameIDs(paneIDs(b), []int{2, 1}) {
		t.Fatalf("A=%v B=%v", paneIDs(a), paneIDs(b))
	}
	if b.focusID != 1 || a.focusID != 0 {
		t.Fatalf("focus A=%d B=%d", a.focusID, b.focusID)
	}
	if b.stickyTitle == "" {
		t.Fatal("B became multi-pane; strip title should freeze")
	}
	if a.stickyTitle != "" {
		t.Fatal("A is solo again; sticky title should clear")
	}
}

func TestMoveLastPaneRemovesSourceTab(t *testing.T) {
	pages, a, b := twoTabs()
	res, ok, why := movePaneToPage(pages, 2, a, -1, 99)
	if !ok {
		t.Fatalf("move failed: %s", why)
	}
	if len(res.pages) != 1 || res.pages[0] != a || res.active != 0 {
		t.Fatalf("want only A left and active; got %d tabs active=%d", len(res.pages), res.active)
	}
	if !sameIDs(paneIDs(a), []int{0, 1, 2}) {
		t.Fatalf("A=%v", paneIDs(a))
	}
	if b.root != nil {
		t.Fatal("emptied source should hold no panes")
	}
}

func TestMovePaneToNewTabUsesFreshID(t *testing.T) {
	pages, a, _ := twoTabs()
	// Pane 0 shares its id with tab A; the new tab must not reuse it.
	res, ok, why := movePaneToPage(pages, 0, nil, -1, 7)
	if !ok {
		t.Fatalf("move failed: %s", why)
	}
	if len(res.pages) != 3 || res.active != 2 {
		t.Fatalf("want new tab at end; got %d tabs active=%d", len(res.pages), res.active)
	}
	np := res.pages[2]
	if np.id != 7 || np.id == a.id {
		t.Fatalf("new tab id %d collides or is wrong (A=%d)", np.id, a.id)
	}
	if !sameIDs(paneIDs(np), []int{0}) || !sameIDs(paneIDs(a), []int{1}) {
		t.Fatalf("new=%v A=%v", paneIDs(np), paneIDs(a))
	}
}

func TestMovePaneToNewTabAtStart(t *testing.T) {
	pages, _, _ := twoTabs()
	res, ok, _ := movePaneToPage(pages, 1, nil, 0, 9)
	if !ok || res.active != 0 || res.pages[0].id != 9 {
		t.Fatalf("want new tab first and active; ok=%v active=%d", ok, res.active)
	}
}

func TestMoveRefusesNoOps(t *testing.T) {
	pages, a, b := twoTabs()
	if _, ok, _ := movePaneToPage(pages, 2, nil, -1, 9); ok {
		t.Fatal("sole pane to a new tab is a no-op")
	}
	if _, ok, _ := movePaneToPage(pages, 0, a, -1, 9); ok {
		t.Fatal("moving into its own tab is a no-op")
	}
	if _, ok, _ := movePaneToPage(pages, 42, b, -1, 9); ok {
		t.Fatal("unknown pane")
	}
	if !sameIDs(paneIDs(a), []int{0, 1}) || !sameIDs(paneIDs(b), []int{2}) {
		t.Fatal("refused moves must not change tabs")
	}
}

func TestMoveIntoZoomedTabShowsSplit(t *testing.T) {
	pages, a, b := twoTabs()
	if !a.toggleZoom() {
		t.Fatal("zoom")
	}
	if _, ok, _ := movePaneToPage(pages, 2, a, -1, 9); !ok {
		t.Fatal("move failed")
	}
	if a.zoomed() {
		t.Fatal("receiving tab should leave zoom so the moved pane is visible")
	}
	_ = b
}

func TestMergeKeepsLayoutAndFocus(t *testing.T) {
	// A = [0], B = [1 / 2] (stacked), C = [3].
	a := newPage(&tab{id: 0, title: "a"})
	b := newPage(&tab{id: 1, title: "b1"})
	b.splitFocused(splitHoriz, &tab{id: 2, title: "b2"})
	b.setFocus(2)
	c := newPage(&tab{id: 3, title: "c"})
	pages := []*page{a, b, c}

	res, ok, why := mergePageInto(pages, b, a)
	if !ok {
		t.Fatalf("merge failed: %s", why)
	}
	if len(res.pages) != 2 || res.pages[0] != a || res.pages[1] != c || res.active != 0 {
		t.Fatalf("want [A C] with A active; got %d active=%d", len(res.pages), res.active)
	}
	if !sameIDs(paneIDs(a), []int{0, 1, 2}) {
		t.Fatalf("A=%v", paneIDs(a))
	}
	// B's stack is kept as one block on the right.
	if a.root.dir != splitVert || a.root.b == nil || a.root.b.isLeaf() || a.root.b.dir != splitHoriz {
		t.Fatal("merged tab's split layout should be grafted intact")
	}
	if a.focusID != 2 || res.focus != 2 {
		t.Fatalf("focus should stay on B's focused pane; got %d", a.focusID)
	}
	if a.root.ratio < 0.3 || a.root.ratio > 0.36 {
		t.Fatalf("1 vs 2 panes should split about 1/3, got %v", a.root.ratio)
	}
	if b.root != nil {
		t.Fatal("merged-away tab should hold no panes")
	}
}

func TestMergeActiveIndexAfterEarlierTabRemoved(t *testing.T) {
	a := newPage(&tab{id: 0})
	b := newPage(&tab{id: 1})
	c := newPage(&tab{id: 2})
	pages := []*page{a, b, c}
	res, ok, _ := mergePageInto(pages, a, c)
	if !ok || res.pages[res.active] != c || res.active != 1 {
		t.Fatalf("dest index should shift left after removing A; active=%d", res.active)
	}
}

func TestMergeRefusesSameTab(t *testing.T) {
	pages, a, _ := twoTabs()
	if _, ok, _ := mergePageInto(pages, a, a); ok {
		t.Fatal("merge into self")
	}
}

func TestAdjacentMoveTarget(t *testing.T) {
	pages, a, b := twoTabs()
	if dst, _ := adjacentMoveTarget(pages, a, 1); dst != b {
		t.Fatal("next of A is B")
	}
	if dst, at := adjacentMoveTarget(pages, a, -1); dst != nil || at != 0 {
		t.Fatal("prev of first tab is a new tab at the start")
	}
	if dst, at := adjacentMoveTarget(pages, b, 1); dst != nil || at != 2 {
		t.Fatal("next of last tab is a new tab at the end")
	}
}

func TestAIMoveToTabKeepsUsersTab(t *testing.T) {
	// User on A=[0]; agent moves pane 2 from B=[1|2] into C=[3].
	a := newPage(&tab{id: 0})
	b := newPage(&tab{id: 1})
	b.splitFocused(splitVert, &tab{id: 2})
	c := newPage(&tab{id: 3})
	pages := []*page{a, b, c}
	active, next := 0, 10
	s := aiSurface{pages: &pages, active: &active, nextID: &next}
	if !movePaneToTab(s, 2, c.id, true) {
		t.Fatal("move failed")
	}
	if pages[active] != a {
		t.Fatalf("agent move should leave the user on A; active=%d", active)
	}
	if c.focusID != 2 || !sameIDs(paneIDs(c), []int{3, 2}) {
		t.Fatalf("C=%v focus=%d", paneIDs(c), c.focusID)
	}
	// Extract to a new tab: fresh id, counter advanced.
	if !movePaneToTab(s, 3, 0, false) {
		t.Fatal("extract failed")
	}
	if pages[len(pages)-1].id != 10 || next != 11 {
		t.Fatalf("new tab id=%d next=%d", pages[len(pages)-1].id, next)
	}
}
