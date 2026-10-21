//go:build windows || darwin

package ui

// Dragging a pane by its mini title bar and dropping it on an edge of
// another pane in the same tab. Hosts feed mouse events in; this file owns
// the hit-test and drop-edge math so macOS and Windows behave the same.

// paneDragThreshold is how far (px) the pointer must travel after pressing a
// title bar before it counts as a drag. Shorter moves stay a plain click.
const paneDragThreshold = 6

// paneDrag is the title-bar drag state for one window.
type paneDrag struct {
	// armed: the button went down on paneID's title bar.
	armed  bool
	paneID int
	startX int32
	startY int32
	// active: the pointer travelled past the threshold; a drop target and
	// highlight are being tracked.
	active   bool
	targetID int
	edge     string
	// hx..hh is where the dragged pane would land (the drop preview).
	hx, hy, hw, hh int32
}

// engaged reports whether a title-bar press is in progress (drag or not).
func (d *paneDrag) engaged() bool { return d.armed }

func (d *paneDrag) arm(paneID int, x, y int32) {
	*d = paneDrag{armed: true, paneID: paneID, startX: x, startY: y, targetID: -1}
}

func (d *paneDrag) reset() { *d = paneDrag{targetID: -1} }

// move tracks the pointer over page p, laid out as layouts with cell size
// cw×ch. It returns true when the drop highlight changed and the host should
// repaint. A drop that would leave the layout as it is counts as no target.
func (d *paneDrag) move(p *page, layouts []paneGeom, cw, ch int32, x, y int32) bool {
	if !d.armed {
		return false
	}
	if !d.active {
		dx, dy := x-d.startX, y-d.startY
		if dx*dx+dy*dy < paneDragThreshold*paneDragThreshold {
			return false
		}
		d.active = true
	}
	id, edge, ok := paneDropAt(layouts, d.paneID, x, y)
	if !ok {
		id, edge = -1, ""
	}
	if id == d.targetID && edge == d.edge {
		return false
	}
	var hx, hy, hw, hh int32
	if id >= 0 {
		var landed bool
		hx, hy, hw, hh, landed = paneDropLanding(p, layouts, cw, ch, d.paneID, id, edge)
		if !landed {
			id, edge = -1, ""
			hx, hy, hw, hh = 0, 0, 0, 0
		}
	}
	if id == d.targetID && edge == d.edge {
		return false
	}
	d.targetID, d.edge = id, edge
	d.hx, d.hy, d.hw, d.hh = hx, hy, hw, hh
	return true
}

// release ends the press. ok is true when it was a drag that ended over a
// valid drop target; the caller then moves paneID beside targetID on edge.
// The state is cleared either way.
func (d *paneDrag) release() (paneID, targetID int, edge string, ok bool) {
	paneID, targetID, edge = d.paneID, d.targetID, d.edge
	ok = d.armed && d.active && targetID >= 0 && edge != ""
	d.reset()
	return paneID, targetID, edge, ok
}

// highlight is the rect to paint as the drop preview, if any.
func (d *paneDrag) highlight() (x, y, w, h int32, ok bool) {
	if !d.active || d.targetID < 0 || d.hw < 1 || d.hh < 1 {
		return 0, 0, 0, 0, false
	}
	return d.hx, d.hy, d.hw, d.hh, true
}

// hitPaneTitle returns the multi-pane mini title row under (px,py), if any.
func hitPaneTitle(layouts []paneGeom, px, py int32) *paneGeom {
	for i := range layouts {
		g := &layouts[i]
		if g.titleH < 1 || g.pane == nil {
			continue
		}
		if px >= g.x && px < g.x+g.w && py >= g.titleY && py < g.titleY+g.titleH {
			return g
		}
	}
	return nil
}

// paneOuter is the whole leaf: title, content and input bar.
func paneOuter(g paneGeom) (x, y, w, h int32) {
	oy, oh := g.outerY, g.outerH
	if oh < 1 {
		oy, oh = g.y, g.h
	}
	return g.x, oy, g.w, oh
}

// paneDropEdge picks the edge of g nearest (px,py). The pane is cut along
// its diagonals into four triangles, so the edge the pointer is closest to
// (relative to the pane's size) wins.
func paneDropEdge(g paneGeom, px, py int32) string {
	x, y, w, h := paneOuter(g)
	if w < 1 || h < 1 {
		return ""
	}
	fx := (float64(px-x)+0.5)/float64(w) - 0.5
	fy := (float64(py-y)+0.5)/float64(h) - 0.5
	ax, ay := fx, fy
	if ax < 0 {
		ax = -ax
	}
	if ay < 0 {
		ay = -ay
	}
	if ax >= ay {
		if fx < 0 {
			return "left"
		}
		return "right"
	}
	if fy < 0 {
		return "top"
	}
	return "bottom"
}

// paneDropAt finds the pane under (px,py) other than srcID and the edge to
// dock on. ok is false over the dragged pane itself, a sash or empty space.
func paneDropAt(layouts []paneGeom, srcID int, px, py int32) (targetID int, edge string, ok bool) {
	hi := hitPane(layouts, px, py)
	if hi < 0 || layouts[hi].pane == nil || layouts[hi].pane.id == srcID {
		return -1, "", false
	}
	edge = paneDropEdge(layouts[hi], px, py)
	if edge == "" {
		return -1, "", false
	}
	return layouts[hi].pane.id, edge, true
}

// paneDropLanding is where pane srcID ends up if it is dropped on edge of
// targetID in page p. It runs the real move on a copy of the tree and lays
// the copy out in the same shell, so the preview matches the result even when
// taking srcID out first collapses its split and grows the target. ok is
// false when the drop would leave the layout as it is.
func paneDropLanding(p *page, layouts []paneGeom, cw, ch int32, srcID, targetID int, edge string) (x, y, w, h int32, ok bool) {
	if p == nil || p.root == nil || len(layouts) == 0 {
		return 0, 0, 0, 0, false
	}
	root, changed := simulateDockInTab(p.root, srcID, targetID, edge)
	if !changed {
		return 0, 0, 0, 0, false
	}
	// The leaves tile the shell, so their outer rects bound it.
	sx, sy, sw, sh := paneOuter(layouts[0])
	ex, ey := sx+sw, sy+sh
	for _, g := range layouts[1:] {
		gx, gy, gw, gh := paneOuter(g)
		if gx < sx {
			sx = gx
		}
		if gy < sy {
			sy = gy
		}
		if gx+gw > ex {
			ex = gx + gw
		}
		if gy+gh > ey {
			ey = gy + gh
		}
	}
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	res := layoutPage(root, sx, sy, ex-sx, ey-sy, cw, ch, srcID)
	for _, g := range res.leaves {
		if g.pane != nil && g.pane.id == srcID {
			x, y, w, h = paneOuter(g)
			return x, y, w, h, w > 0 && h > 0
		}
	}
	return 0, 0, 0, 0, false
}

// hostDropPane docks paneID on edge of targetID (both in the active tab) and
// keeps focus on the moved pane.
func hostDropPane(h pageMoveHost, paneID, targetID int, edge string) {
	pp, active, _ := h.pageState()
	out, dst, ok, why := movePaneBeside(*pp, paneID, targetID, edge)
	if !ok {
		h.toast(why)
		return
	}
	*pp = out
	if i := pageIndex(out, dst); i >= 0 {
		*active = i
	}
	dst.setFocus(paneID)
	h.afterPageMove()
}
