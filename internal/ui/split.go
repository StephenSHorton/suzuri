//go:build windows || darwin

package ui

import "strings"

// Split panes within a chrome tab (equal H/V splits; no sash drag in v1).
//
// Model:
//   page  — one strip tab; owns a binary tree of panes
//   pane  — *tab (shell session: ConPTY + VT + scrollback + input bar)
//
// Focus is a pane id (*tab.id). The Warp bar and keyboard target the focused leaf.

// splitDir is the axis of a branch node.
type splitDir int

const (
	// splitHoriz stacks children top (a) / bottom (b).
	splitHoriz splitDir = iota
	// splitVert places children left (a) / right (b).
	splitVert
)

// splitNode is a binary layout tree. Leaves hold a pane; branches hold two children.
type splitNode struct {
	pane  *tab // non-nil ⇒ leaf
	dir   splitDir
	ratio float64 // fraction of space for child a (0.05–0.95; default 0.5)
	a, b  *splitNode
}

func (n *splitNode) isLeaf() bool {
	return n != nil && n.pane != nil
}

func leafNode(t *tab) *splitNode {
	if t == nil {
		return nil
	}
	return &splitNode{pane: t, ratio: 0.5}
}

// page is one chrome-strip entry that may contain multiple split panes.
//
// Title model (pane vs tab):
//   - A pane (*tab) always has its own title. OSC sequences from tools like
//     Grok only ever update the pane (applyTitle) — never the page.
//   - A page (chrome strip tab) is a separate object even when it has a single
//     pane. Solo pages follow that pane's display title so the strip stays
//     useful; multi-pane pages freeze a sticky strip name at first split so
//     Grok/OSC thrash only shows on the per-pane mini title bars.
type page struct {
	// id is stable for chrome (matches first pane id at creation; never reused).
	id int
	// root of the split tree (never nil while page is alive).
	root *splitNode
	// focusID is the focused leaf pane id (*tab.id).
	focusID int
	// userTitle is a manual strip name for this page. When set, the chrome tab
	// label no longer follows panes (or stickyTitle).
	userTitle string
	// stickyTitle freezes the strip label when the page first becomes multi-pane
	// (only if userTitle is empty). Cleared when the page collapses back to one
	// pane so solo pages follow the remaining pane's OSC title again.
	stickyTitle string
}

func newPage(t *tab) *page {
	if t == nil {
		return nil
	}
	return &page{
		id:      t.id,
		root:    leafNode(t),
		focusID: t.id,
	}
}

func (p *page) focused() *tab {
	if p == nil || p.root == nil {
		return nil
	}
	if t := findPane(p.root, p.focusID); t != nil {
		return t
	}
	// Fallback: first leaf.
	leaves := collectLeaves(p.root)
	if len(leaves) == 0 {
		return nil
	}
	p.focusID = leaves[0].id
	return leaves[0]
}

func (p *page) setFocus(id int) bool {
	if p == nil {
		return false
	}
	if findPane(p.root, id) == nil {
		return false
	}
	p.focusID = id
	return true
}

func (p *page) leaves() []*tab {
	if p == nil {
		return nil
	}
	return collectLeaves(p.root)
}

func (p *page) leafCount() int {
	return len(p.leaves())
}

func (p *page) anyBusy() bool {
	for _, t := range p.leaves() {
		if t != nil && t.busy() {
			return true
		}
	}
	return false
}

func (p *page) anyAlt() bool {
	for _, t := range p.leaves() {
		if t != nil && t.altScreen() {
			return true
		}
	}
	return false
}

func (p *page) anyAlive() bool {
	for _, t := range p.leaves() {
		if t != nil && t.alive.Load() {
			return true
		}
	}
	return false
}

// title for the strip:
//  1. userTitle (manual "Rename tab") always wins
//  2. multi-pane: stickyTitle (frozen at first split; ignores Grok/OSC)
//  3. solo (or sticky empty): focused pane display title, then any leaf
func (p *page) title() string {
	if p == nil {
		return "shell"
	}
	if s := strings.TrimSpace(p.userTitle); s != "" {
		return s
	}
	if p.leafCount() > 1 {
		if s := strings.TrimSpace(p.stickyTitle); s != "" {
			return s
		}
	}
	if t := p.focused(); t != nil {
		if d := t.displayTitle(); d != "" {
			return d
		}
	}
	for _, t := range p.leaves() {
		if t != nil {
			if d := t.displayTitle(); d != "" {
				return d
			}
		}
	}
	return "shell"
}

// setUserTitle locks a custom strip name (empty clears → sticky/pane titles).
func (p *page) setUserTitle(name string) {
	if p == nil {
		return
	}
	p.userTitle = strings.TrimSpace(name)
}

// captureStickyTitle freezes the current strip label for multi-pane use.
// No-op when user already locked the tab name or sticky is already set.
func (p *page) captureStickyTitle() {
	if p == nil {
		return
	}
	if strings.TrimSpace(p.userTitle) != "" {
		return
	}
	if strings.TrimSpace(p.stickyTitle) != "" {
		return
	}
	// Prefer the current solo/focused display title before the split mutates focus.
	if t := p.focused(); t != nil {
		if d := t.displayTitle(); d != "" {
			p.stickyTitle = d
			return
		}
	}
	p.stickyTitle = p.title()
}

// clearStickyTitleIfSolo drops the freeze when only one pane remains so the
// strip can follow that pane's OSC title again.
func (p *page) clearStickyTitleIfSolo() {
	if p == nil {
		return
	}
	if p.leafCount() <= 1 {
		p.stickyTitle = ""
	}
}

func findPane(n *splitNode, id int) *tab {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.pane != nil && n.pane.id == id {
			return n.pane
		}
		return nil
	}
	if t := findPane(n.a, id); t != nil {
		return t
	}
	return findPane(n.b, id)
}

func collectLeaves(n *splitNode) []*tab {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.pane == nil {
			return nil
		}
		return []*tab{n.pane}
	}
	var out []*tab
	out = append(out, collectLeaves(n.a)...)
	out = append(out, collectLeaves(n.b)...)
	return out
}

// splitFocused replaces the focused leaf with a split of (old | new) in dir.
// newPane becomes focused. Returns false if focus missing.
//
// On the transition from 1 → 2+ panes, freezes the strip label (stickyTitle)
// so later Grok/OSC updates on individual panes do not thrash the chrome tab.
func (p *page) splitFocused(dir splitDir, newPane *tab) bool {
	if p == nil || p.root == nil || newPane == nil {
		return false
	}
	wasSolo := p.leafCount() <= 1
	if wasSolo {
		p.captureStickyTitle()
	}
	ok := false
	p.root = splitReplace(p.root, p.focusID, dir, newPane, &ok)
	if ok {
		p.focusID = newPane.id
	} else if wasSolo {
		// Split failed — don't leave a sticky from a no-op.
		p.stickyTitle = ""
	}
	return ok
}

// splitReplace walks the tree and replaces the leaf with id with a branch.
func splitReplace(n *splitNode, id int, dir splitDir, newPane *tab, ok *bool) *splitNode {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.pane == nil || n.pane.id != id {
			return n
		}
		*ok = true
		// Keep existing pane as a (top/left); new pane as b (bottom/right).
		return &splitNode{
			dir:   dir,
			ratio: 0.5,
			a:     leafNode(n.pane),
			b:     leafNode(newPane),
		}
	}
	n.a = splitReplace(n.a, id, dir, newPane, ok)
	if *ok {
		return n
	}
	n.b = splitReplace(n.b, id, dir, newPane, ok)
	return n
}

// removePane drops a leaf by id. Returns the closed pane, whether the page is
// now empty, and the new focus id (if any).
//
// When the page collapses back to a single pane, stickyTitle is cleared so the
// strip follows that pane again (Grok/OSC renames the only visible title).
//
// Focus after close (tmux-style tree neighbor): if the closed pane was focused,
// move focus to a leaf of its immediate split sibling — not the first leaf of
// the whole page. That matches "nearest pane" for the usual 2–3 pane layouts
// without needing live geometry at remove time.
func (p *page) removePane(id int) (closed *tab, empty bool, newFocus int) {
	if p == nil || p.root == nil {
		return nil, true, -1
	}
	// Capture sibling leaves before the tree mutates (only needed if focus moves).
	var siblingFocus []*tab
	needRefocus := p.focusID == id || findPane(p.root, p.focusID) == nil
	if needRefocus {
		if leaves, ok := siblingLeavesOf(p.root, id); ok && len(leaves) > 0 {
			siblingFocus = leaves
		}
	}
	var removed *tab
	p.root = removeLeaf(p.root, id, &removed)
	if removed == nil {
		return nil, false, p.focusID
	}
	if p.root == nil {
		p.stickyTitle = ""
		return removed, true, -1
	}
	// Collapse is handled inside removeLeaf; root may be a leaf or branch.
	leaves := collectLeaves(p.root)
	if len(leaves) == 0 {
		p.root = nil
		p.stickyTitle = ""
		return removed, true, -1
	}
	// If we closed the focused pane (or focus is gone), pick nearest remaining.
	if p.focusID == id || findPane(p.root, p.focusID) == nil {
		p.focusID = pickFocusAfterClose(siblingFocus, leaves)
	}
	p.clearStickyTitleIfSolo()
	return removed, false, p.focusID
}

// pickFocusAfterClose prefers a still-present sibling leaf, else first remaining.
func pickFocusAfterClose(sibling, remaining []*tab) int {
	for _, t := range sibling {
		if t == nil {
			continue
		}
		for _, r := range remaining {
			if r != nil && r.id == t.id {
				return t.id
			}
		}
	}
	if len(remaining) > 0 && remaining[0] != nil {
		return remaining[0].id
	}
	return -1
}

// siblingLeavesOf finds the leaf with id and returns the leaves of its immediate
// split sibling subtree. ok is false when id is not found (or is the only leaf).
// This is the tree-neighbor used when closing a pane (like tmux kill-pane focus).
func siblingLeavesOf(n *splitNode, id int) (leaves []*tab, ok bool) {
	if n == nil || n.isLeaf() {
		return nil, false
	}
	if n.a != nil && n.a.isLeaf() && n.a.pane != nil && n.a.pane.id == id {
		return collectLeaves(n.b), true
	}
	if n.b != nil && n.b.isLeaf() && n.b.pane != nil && n.b.pane.id == id {
		return collectLeaves(n.a), true
	}
	if leaves, ok = siblingLeavesOf(n.a, id); ok {
		return leaves, true
	}
	return siblingLeavesOf(n.b, id)
}

// removeLeaf returns the updated subtree (nil if empty) and sets *removed.
func removeLeaf(n *splitNode, id int, removed **tab) *splitNode {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.pane != nil && n.pane.id == id {
			*removed = n.pane
			return nil
		}
		return n
	}
	n.a = removeLeaf(n.a, id, removed)
	n.b = removeLeaf(n.b, id, removed)
	// Collapse if one side is gone.
	if n.a == nil && n.b == nil {
		return nil
	}
	if n.a == nil {
		return n.b
	}
	if n.b == nil {
		return n.a
	}
	return n
}

// focusNeighbor moves focus in a cardinal direction among leaves of this page.
// dir: 0=left, 1=right, 2=up, 3=down. Returns true if focus changed.
func (p *page) focusNeighbor(dir int, layouts []paneGeom) bool {
	if p == nil || len(layouts) < 2 {
		return false
	}
	var cur *paneGeom
	for i := range layouts {
		if layouts[i].pane != nil && layouts[i].pane.id == p.focusID {
			cur = &layouts[i]
			break
		}
	}
	if cur == nil {
		return false
	}
	cx, cy := paneCenter(*cur)

	bestID := -1
	bestDist := int32(1 << 30)
	for i := range layouts {
		pl := &layouts[i]
		if pl.pane == nil || pl.pane.id == p.focusID {
			continue
		}
		ox, oy := paneCenter(*pl)
		var ok bool
		var dist int32
		switch dir {
		case 0: // left
			ok = ox < cx
			dist = (cx - ox) + abs32(cy-oy)/2
		case 1: // right
			ok = ox > cx
			dist = (ox - cx) + abs32(cy-oy)/2
		case 2: // up
			ok = oy < cy
			dist = (cy - oy) + abs32(cx-ox)/2
		case 3: // down
			ok = oy > cy
			dist = (oy - cy) + abs32(cx-ox)/2
		}
		if !ok {
			continue
		}
		if dist < bestDist {
			bestDist = dist
			bestID = pl.pane.id
		}
	}
	if bestID < 0 {
		return false
	}
	return p.setFocus(bestID)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// paneGeom is a laid-out leaf rectangle in client pixels + cell size.
//
//	x,y,w,h     — VT grid pixel rect (content)
//	titleY/H    — mini tab title strip above content (0 when solo pane)
//	barY/H      — per-pane Warp input bar at leaf bottom (0 on alt-screen)
//	outerY/H    — full leaf including title + bar (hit-test / focus ring)
type paneGeom struct {
	pane       *tab
	x, y, w, h int32
	cols, rows int
	focused    bool
	titleY     int32
	titleH     int32
	barY       int32
	barH       int32
	barCols    int // wrap width for this pane's input bar
	outerY     int32
	outerH     int32
}

const (
	minPaneCols = 20
	minPaneRows = 5
	// Shared sash thickness between sibling panes (single border, not double).
	paneGapPx = 3
	// Hit-test pad beyond the sash so drag is easy (total hit ≈ gap+2*pad).
	sashHitPad = 4
	// Drag ratio clamps so a pane never collapses fully.
	sashRatioMin = 0.12
	sashRatioMax = 0.88
)

// sashGeom is a shared divider between two sibling panes (drag target).
type sashGeom struct {
	node                   *splitNode
	dir                    splitDir
	x, y, w, h             int32 // visual sash strip
	parentX, parentY       int32
	parentW, parentH       int32 // parent rect for ratio math
}

// layoutResult is leaves + shared sashes for one page tree.
type layoutResult struct {
	leaves []paneGeom
	sashes []sashGeom
	// Outer shell rect used for the shared perimeter border.
	shellX, shellY, shellW, shellH int32
}

// layoutPage assigns pixel rects and cell sizes to every leaf under root.
// shell is the full region under the chrome strip (bars live inside each leaf).
// Multi-leaf trees get a mini title row; each non-alt leaf gets its own input bar.
// Sibling panes share a single sash (no double borders).
func layoutPage(root *splitNode, shellX, shellY, shellW, shellH int32, cw, ch int32, focusID int) layoutResult {
	res := layoutResult{shellX: shellX, shellY: shellY, shellW: shellW, shellH: shellH}
	if root == nil || shellW < 1 || shellH < 1 || cw < 1 || ch < 1 {
		return res
	}
	titleH := int32(0)
	if len(collectLeaves(root)) > 1 {
		titleH = ch // one cell row for pane tab title
	}
	layoutNode(root, shellX, shellY, shellW, shellH, cw, ch, focusID, titleH, &res)
	return res
}

func layoutNode(n *splitNode, x, y, w, h int32, cw, ch int32, focusID int, titleH int32, res *layoutResult) {
	if n == nil || res == nil {
		return
	}
	if n.isLeaf() {
		if n.pane == nil {
			return
		}
		// Leaf stack: [title?] [VT content] [input bar?]
		th := titleH
		if th > h/3 {
			th = 0
		}
		barH := paneInputBarPixelHeight(n.pane, w, cw, ch)
		// Keep a usable VT: never let title+bar eat the whole leaf.
		if th+barH+ch > h {
			if barH > 0 && th+ch*2 > h {
				th = 0
			}
			if th+barH+ch > h {
				hair, topPad, botPad := inputBarVPads(ch)
				minBar := hair + topPad + ch + botPad
				if n.pane.altScreen() {
					minBar = 0
				}
				if th+minBar+ch <= h {
					barH = minBar
				} else {
					barH = 0
				}
			}
		}
		if th+barH >= h {
			th, barH = 0, 0
		}
		contentY := y + th
		contentH := h - th - barH
		if contentH < 1 {
			contentH = 1
		}
		barY := y + h - barH
		if barH < 1 {
			barY = y + h
		}
		barCols := 0
		if barH > 0 {
			barCols = paneInputContentCols(w, cw)
		}
		cols := int(w / cw)
		rows := int(contentH / ch)
		if cols < 1 {
			cols = 1
		}
		if rows < 1 {
			rows = 1
		}
		if cols > maxTermCols {
			cols = maxTermCols
		}
		if rows > maxTermRows {
			rows = maxTermRows
		}
		if cols < minPaneCols && w >= cw*int32(minPaneCols) {
			cols = minPaneCols
		}
		if rows < minPaneRows && contentH >= ch*int32(minPaneRows) {
			rows = minPaneRows
		}
		res.leaves = append(res.leaves, paneGeom{
			pane:    n.pane,
			x:       x,
			y:       contentY,
			w:       w,
			h:       contentH,
			cols:    cols,
			rows:    rows,
			focused: n.pane.id == focusID,
			titleY:  y,
			titleH:  th,
			barY:    barY,
			barH:    barH,
			barCols: barCols,
			outerY:  y,
			outerH:  h,
		})
		return
	}
	ratio := n.ratio
	if ratio < sashRatioMin {
		ratio = sashRatioMin
	}
	if ratio > sashRatioMax {
		ratio = sashRatioMax
	}
	// Persist clamped ratio so drag stays consistent.
	n.ratio = ratio
	gap := int32(paneGapPx)
	if n.dir == splitVert {
		// left | right
		if w < gap+2 {
			gap = 0
		}
		avail := w - gap
		if avail < 2 {
			avail = w
			gap = 0
		}
		wA := int32(float64(avail) * ratio)
		if wA < 1 {
			wA = 1
		}
		if wA > avail-1 {
			wA = avail - 1
		}
		wB := avail - wA
		if gap > 0 {
			res.sashes = append(res.sashes, sashGeom{
				node: n, dir: splitVert,
				x: x + wA, y: y, w: gap, h: h,
				parentX: x, parentY: y, parentW: w, parentH: h,
			})
		}
		layoutNode(n.a, x, y, wA, h, cw, ch, focusID, titleH, res)
		layoutNode(n.b, x+wA+gap, y, wB, h, cw, ch, focusID, titleH, res)
		return
	}
	// top / bottom
	if h < gap+2 {
		gap = 0
	}
	avail := h - gap
	if avail < 2 {
		avail = h
		gap = 0
	}
	hA := int32(float64(avail) * ratio)
	if hA < 1 {
		hA = 1
	}
	if hA > avail-1 {
		hA = avail - 1
	}
	hB := avail - hA
	if gap > 0 {
		res.sashes = append(res.sashes, sashGeom{
			node: n, dir: splitHoriz,
			x: x, y: y + hA, w: w, h: gap,
			parentX: x, parentY: y, parentW: w, parentH: h,
		})
	}
	layoutNode(n.a, x, y, w, hA, cw, ch, focusID, titleH, res)
	layoutNode(n.b, x, y+hA+gap, w, hB, cw, ch, focusID, titleH, res)
}

// hitSash returns the sash under (px,py) with hit padding, or -1.
func hitSash(sashes []sashGeom, px, py int32) int {
	pad := int32(sashHitPad)
	for i := range sashes {
		s := &sashes[i]
		if s.dir == splitVert {
			// Expand hit on X only.
			if px >= s.x-pad && px < s.x+s.w+pad && py >= s.y && py < s.y+s.h {
				return i
			}
		} else {
			if py >= s.y-pad && py < s.y+s.h+pad && px >= s.x && px < s.x+s.w {
				return i
			}
		}
	}
	return -1
}

// applySashDrag updates node.ratio from a pointer position inside the parent rect.
func applySashDrag(s sashGeom, px, py int32) {
	if s.node == nil {
		return
	}
	var r float64
	if s.dir == splitVert {
		// Fraction of parent width left of the sash center.
		if s.parentW < 2 {
			return
		}
		// Place sash so left pane ends at px (center sash on pointer).
		left := px - s.parentX - paneGapPx/2
		r = float64(left) / float64(s.parentW-paneGapPx)
	} else {
		if s.parentH < 2 {
			return
		}
		top := py - s.parentY - paneGapPx/2
		r = float64(top) / float64(s.parentH-paneGapPx)
	}
	if r < sashRatioMin {
		r = sashRatioMin
	}
	if r > sashRatioMax {
		r = sashRatioMax
	}
	s.node.ratio = r
}

// hitPane returns the layout entry containing (px,py), or -1.
// Hits the full leaf including the title strip.
func hitPane(layouts []paneGeom, px, py int32) int {
	for i := range layouts {
		pl := &layouts[i]
		oy, oh := pl.outerY, pl.outerH
		if oh < 1 {
			oy, oh = pl.y, pl.h
		}
		if px >= pl.x && px < pl.x+pl.w && py >= oy && py < oy+oh {
			return i
		}
	}
	return -1
}

// focusNeighbor uses outer leaf centers so title bars count as part of the pane.
func paneCenter(pl paneGeom) (cx, cy int32) {
	oy, oh := pl.outerY, pl.outerH
	if oh < 1 {
		oy, oh = pl.y, pl.h
	}
	return pl.x + pl.w/2, oy + oh/2
}
