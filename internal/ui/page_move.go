//go:build windows || darwin

package ui

import "fmt"

// Moving panes between chrome tabs (pages).
//
// These helpers only rearrange the page list and split trees. They never
// close a session: a moved pane keeps its PTY, scrollback and running program.
// Hosts call them, then sync chrome and re-layout.

// pageMove is the result of a move or merge.
type pageMove struct {
	pages  []*page
	active int
	// focus is the pane that should have keyboard focus afterwards.
	focus int
}

// pageIndex returns the index of pg in pages, or -1.
func pageIndex(pages []*page, pg *page) int {
	for i, p := range pages {
		if p == pg {
			return i
		}
	}
	return -1
}

// pageWithID finds a page by its strip id.
func pageWithID(pages []*page, id int) *page {
	for _, p := range pages {
		if p != nil && p.id == id {
			return p
		}
	}
	return nil
}

// pageHolding returns the page that owns pane id.
func pageHolding(pages []*page, paneID int) *page {
	for _, p := range pages {
		if p != nil && findPane(p.root, paneID) != nil {
			return p
		}
	}
	return nil
}

// withoutPage returns pages minus pg (a new slice; pg's sessions are untouched).
func withoutPage(pages []*page, pg *page) []*page {
	out := make([]*page, 0, len(pages))
	for _, p := range pages {
		if p != pg {
			out = append(out, p)
		}
	}
	return out
}

// movePaneToPage moves pane paneID out of its page.
//
//   - dst != nil: the pane is docked on the right of dst's focused pane.
//   - dst == nil: the pane gets a new tab, inserted at insertAt (clamped;
//     -1 = end). newPageID is the id for that tab; it must not be in use
//     (pane ids and page ids share the host counter, so pass nextTabID).
//
// If the pane was the last one in its tab, that tab goes away. The tab that
// receives the pane becomes active. ok is false (and pages are unchanged)
// when there is nothing to do; reason says why.
func movePaneToPage(pages []*page, paneID int, dst *page, insertAt int, newPageID int) (res pageMove, ok bool, reason string) {
	src := pageHolding(pages, paneID)
	if src == nil {
		return pageMove{}, false, "no such pane"
	}
	if dst != nil && pageIndex(pages, dst) < 0 {
		return pageMove{}, false, "no such tab"
	}
	if dst == src {
		return pageMove{}, false, "already in that tab"
	}
	if dst == nil && src.leafCount() <= 1 {
		return pageMove{}, false, "already its own tab"
	}
	t := findPane(src.root, paneID)
	if dst != nil {
		anchor := dst.focused()
		if anchor == nil {
			return pageMove{}, false, "no such tab"
		}
		closed, empty, _ := src.removePane(paneID)
		if closed == nil {
			return pageMove{}, false, "no such pane"
		}
		if !dst.dockExisting(anchor.id, "right", t) {
			// Cannot happen while anchor is a leaf of dst; keep the pane alive
			// in its own tab rather than losing it.
			np := newPage(t)
			np.id = newPageID
			out := pages
			if empty {
				out = withoutPage(out, src)
			}
			out = append(out, np)
			return pageMove{pages: out, active: len(out) - 1, focus: t.id}, true, ""
		}
		out := pages
		if empty {
			out = withoutPage(pages, src)
		}
		return pageMove{pages: out, active: pageIndex(out, dst), focus: t.id}, true, ""
	}

	closed, _, _ := src.removePane(paneID)
	if closed == nil {
		return pageMove{}, false, "no such pane"
	}
	np := newPage(t)
	np.id = newPageID
	if insertAt < 0 || insertAt > len(pages) {
		insertAt = len(pages)
	}
	out := make([]*page, 0, len(pages)+1)
	out = append(out, pages[:insertAt]...)
	out = append(out, np)
	out = append(out, pages[insertAt:]...)
	return pageMove{pages: out, active: insertAt, focus: t.id}, true, ""
}

// mergePageInto moves every pane of src into dst, keeping src's split layout
// intact as one block on the right of dst's panes. src's tab goes away; dst
// becomes active and focus stays on the pane src had focused. src's custom
// tab name is dropped (dst keeps its own).
func mergePageInto(pages []*page, src, dst *page) (res pageMove, ok bool, reason string) {
	if src == nil || dst == nil || pageIndex(pages, src) < 0 || pageIndex(pages, dst) < 0 {
		return pageMove{}, false, "no such tab"
	}
	if src == dst {
		return pageMove{}, false, "same tab"
	}
	if src.root == nil || dst.root == nil {
		return pageMove{}, false, "empty tab"
	}
	focus := src.focused()
	if focus == nil {
		return pageMove{}, false, "empty tab"
	}
	if dst.leafCount() <= 1 {
		dst.captureStickyTitle()
	}
	dst.clearZoom()
	// Size the two blocks by pane count so panes end up roughly equal width.
	nd, ns := float64(dst.leafCount()), float64(src.leafCount())
	ratio := nd / (nd + ns)
	if ratio < sashRatioMin {
		ratio = sashRatioMin
	}
	if ratio > sashRatioMax {
		ratio = sashRatioMax
	}
	dst.root = &splitNode{dir: splitVert, ratio: ratio, a: dst.root, b: src.root}
	dst.focusID = focus.id
	// src no longer owns any pane; drop it without touching sessions.
	src.root = nil
	out := withoutPage(pages, src)
	return pageMove{pages: out, active: pageIndex(out, dst), focus: focus.id}, true, ""
}

// adjacentMoveTarget picks where "move pane to previous/next tab" sends the
// focused pane of page src: the neighbour tab if there is one, else a new tab
// at that end of the strip (dst nil, insertAt set).
func adjacentMoveTarget(pages []*page, src *page, delta int) (dst *page, insertAt int) {
	i := pageIndex(pages, src)
	j := i + delta
	if i >= 0 && j >= 0 && j < len(pages) {
		return pages[j], -1
	}
	if delta < 0 {
		return nil, 0
	}
	return nil, len(pages)
}

// pageMoveHost is the little a window host provides so the move commands
// behave the same on macOS and Windows.
type pageMoveHost interface {
	// pageState exposes the host's page list, active index and id counter.
	pageState() (pages *[]*page, active *int, nextID *int)
	toast(msg string)
	// afterPageMove re-syncs chrome, window title, layout and PTY sizes.
	afterPageMove()
}

func applyPageMove(h pageMoveHost, res pageMove, newID int) {
	pages, active, nextID := h.pageState()
	*pages = res.pages
	*active = res.active
	if pg := res.pages[res.active]; pg != nil {
		pg.setFocus(res.focus)
	}
	if pageWithID(res.pages, newID) != nil {
		*nextID = newID + 1
	}
	h.afterPageMove()
}

func activeMoveSource(h pageMoveHost) (pages []*page, src *page, focus *tab) {
	pp, active, _ := h.pageState()
	pages = *pp
	if *active < 0 || *active >= len(pages) {
		return pages, nil, nil
	}
	src = pages[*active]
	if src == nil {
		return pages, nil, nil
	}
	return pages, src, src.focused()
}

// hostMovePaneToTab moves the focused pane into the tab with strip id tabID.
func hostMovePaneToTab(h pageMoveHost, tabID int) {
	pages, _, focus := activeMoveSource(h)
	dst := pageWithID(pages, tabID)
	if focus == nil || dst == nil {
		h.toast("no such tab")
		return
	}
	hostMovePane(h, pages, focus, dst, -1)
}

// hostMovePaneToNewTab breaks the focused pane out into a new tab after the
// current one.
func hostMovePaneToNewTab(h pageMoveHost) {
	pages, src, focus := activeMoveSource(h)
	if focus == nil {
		return
	}
	hostMovePane(h, pages, focus, nil, pageIndex(pages, src)+1)
}

// hostMovePaneAdjacent carries the focused pane one tab left (delta -1) or
// right (+1), making a new tab at that end of the strip if needed.
func hostMovePaneAdjacent(h pageMoveHost, delta int) {
	pages, src, focus := activeMoveSource(h)
	if focus == nil {
		return
	}
	dst, at := adjacentMoveTarget(pages, src, delta)
	if dst == nil && src.leafCount() <= 1 {
		if delta < 0 {
			h.toast("already first tab")
		} else {
			h.toast("already last tab")
		}
		return
	}
	hostMovePane(h, pages, focus, dst, at)
}

func hostMovePane(h pageMoveHost, pages []*page, focus *tab, dst *page, insertAt int) {
	_, _, nextID := h.pageState()
	newID := *nextID
	res, ok, why := movePaneToPage(pages, focus.id, dst, insertAt, newID)
	if !ok {
		h.toast(why)
		return
	}
	applyPageMove(h, res, newID)
	if dst == nil {
		h.toast("moved to new tab")
	} else {
		h.toast(fmt.Sprintf("moved to tab %d", res.active+1))
	}
}

// hostMergeTabInto moves every pane of the active tab into the tab tabID.
func hostMergeTabInto(h pageMoveHost, tabID int) {
	pages, src, _ := activeMoveSource(h)
	dst := pageWithID(pages, tabID)
	if src == nil || dst == nil {
		h.toast("no such tab")
		return
	}
	n := src.leafCount()
	res, ok, why := mergePageInto(pages, src, dst)
	if !ok {
		h.toast(why)
		return
	}
	applyPageMove(h, res, -1)
	noun := "panes"
	if n == 1 {
		noun = "pane"
	}
	h.toast(fmt.Sprintf("merged %d %s into tab %d", n, noun, res.active+1))
}
