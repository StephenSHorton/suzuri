//go:build windows

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// maxPanesTotal caps concurrent ConPTY sessions across all tabs.
const maxPanesTotal = 24

// activePage returns the chrome-active page (may be nil).
func (u *winUI) activePage() *page {
	if u == nil || u.active < 0 || u.active >= len(u.pages) {
		return nil
	}
	return u.pages[u.active]
}

// allPanes flattens every leaf session (for I/O, teardown, bridge).
func (u *winUI) allPanes() []*tab {
	if u == nil {
		return nil
	}
	// Prefer pages tree; fall back to legacy tabs slice during init.
	if len(u.pages) > 0 {
		var out []*tab
		for _, p := range u.pages {
			out = append(out, p.leaves()...)
		}
		return out
	}
	return u.tabs
}

// paneCount returns total live+dead leaf sessions.
func (u *winUI) paneCount() int {
	return len(u.allPanes())
}

// pageByPaneID finds the page owning a pane id.
func (u *winUI) pageByPaneID(id int) (pageIdx int, p *page) {
	for i, pg := range u.pages {
		if findPane(pg.root, id) != nil {
			return i, pg
		}
	}
	return -1, nil
}

func findPaneAcrossPages(u *winUI, id int) *tab {
	if u == nil {
		return nil
	}
	for _, pg := range u.pages {
		if t := findPane(pg.root, id); t != nil {
			return t
		}
	}
	return nil
}

// shellRect returns the pixel region for shell panes.
func (u *winUI) shellRect(clientW, clientH int32) (x, y, w, h int32) {
	const padX int32 = 4
	y = u.shellPadY()
	bot := u.shellBottomY(clientH)
	if bot < y+1 {
		bot = y + 1
	}
	x = padX
	w = clientW - padX
	if w < 1 {
		w = 1
	}
	h = bot - y
	if h < 1 {
		h = 1
	}
	return x, y, w, h
}

// computeActiveLayout lays out the active page into u.lastPaneLayout / lastSashes.
func (u *winUI) computeActiveLayout() []paneGeom {
	if u == nil {
		return nil
	}
	p := u.activePage()
	if p == nil || p.root == nil {
		u.lastPaneLayout = nil
		u.lastSashes = nil
		return nil
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	clientW, clientH := u.width, u.height
	if clientW < 1 {
		clientW = int32(u.cols) * cw
	}
	if clientH < 1 {
		clientH = int32(u.rows)*ch + u.chromePx + u.inputPx
	}
	sx, sy, sw, sh := u.shellRect(clientW, clientH)
	res := layoutPage(p.root, sx, sy, sw, sh, cw, ch, p.focusID)
	u.lastPaneLayout = res.leaves
	u.lastSashes = res.sashes
	u.lastShell.x, u.lastShell.y, u.lastShell.w, u.lastShell.h = res.shellX, res.shellY, res.shellW, res.shellH
	return u.lastPaneLayout
}

// focusedGeom returns layout for the focused pane (recomputes if needed).
func (u *winUI) focusedGeom() *paneGeom {
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		layouts = u.computeActiveLayout()
	}
	p := u.activePage()
	if p == nil {
		return nil
	}
	for i := range layouts {
		if layouts[i].pane != nil && layouts[i].pane.id == p.focusID {
			return &layouts[i]
		}
	}
	if len(layouts) > 0 {
		return &layouts[0]
	}
	return nil
}

// paneGeomFor returns layout entry for a pane id on the active page.
func (u *winUI) paneGeomFor(id int) *paneGeom {
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		layouts = u.computeActiveLayout()
	}
	for i := range layouts {
		if layouts[i].pane != nil && layouts[i].pane.id == id {
			return &layouts[i]
		}
	}
	return nil
}

// splitActive creates a new pane from the focused session's profile-ish opts
// and splits the active page in dir (horiz = down, vert = right).
func (u *winUI) splitActive(dir splitDir) {
	defer applog.Recover("splitActive", false)
	if u == nil {
		return
	}
	if u.paneCount() >= maxPanesTotal {
		u.toast("max panes")
		return
	}
	pg := u.activePage()
	focus := u.activeTab()
	if pg == nil || focus == nil {
		return
	}
	// Size for the new session: half the focused pane if known, else full.
	cols, rows := u.cols, u.rows
	if g := u.focusedGeom(); g != nil {
		if dir == splitVert {
			cols = g.cols / 2
		} else {
			rows = g.rows / 2
		}
		if cols < minPaneCols {
			cols = minPaneCols
		}
		if rows < minPaneRows {
			rows = minPaneRows
		}
	}
	opts := tabOpts{
		shell: focus.shell,
		cwd:   focus.cwd,
	}
	t, err := newTab(u.nextTabID, cols, rows, opts)
	if err != nil {
		log.Error("split pane failed", "err", err)
		u.toast("split failed")
		return
	}
	u.nextTabID++
	if !pg.splitFocused(dir, t) {
		t.close()
		u.toast("split failed")
		return
	}
	// Keep flat tabs list in sync for any legacy loops.
	u.tabs = append(u.tabs, t)
	t.startWorkers(u)
	u.selecting = false
	focus.sel.clear()
	setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
	u.syncChrome()
	// Prefer paint-only when a sibling is already on alt-screen (Grok) so we
	// don't ResizePseudoConsole under load. Full settle when safe / later idle.
	u.relayoutActivePaintOnly()
	if u.anyPaneConPtyBusy() {
		u.layoutDeferred = true
	} else {
		u.postLayoutSettle()
	}
	if dir == splitVert {
		u.toast("split right")
	} else {
		u.toast("split down")
	}
	if u.hwnd != 0 {
		u.requestPaint()
	}
	log.Info("split pane", "dir", dir, "new", t.id, "page", pg.id, "leaves", pg.leafCount())
}

// closePaneUI closes a single pane. Last pane in page closes the page (tab).
// Last page uses the same confirm-quit path as close tab when interactive.
// Safe to call more than once for the same id (Wait + Read both notify).
func (u *winUI) closePaneUI(paneID int, interactive bool) {
	defer applog.Recover("closePaneUI", false)
	// Already torn down (duplicate closed notification).
	if u.tabByID(paneID) == nil && findPaneAcrossPages(u, paneID) == nil {
		return
	}
	pi, pg := u.pageByPaneID(paneID)
	if pg == nil {
		// Fallback: treat as whole-tab close by tab id.
		u.closeTabUI(paneID)
		return
	}
	if pg.leafCount() <= 1 {
		// Closing the only pane = close the chrome tab.
		if interactive {
			u.closePageAt(pi, true)
		} else {
			u.closePageAt(pi, false)
		}
		return
	}
	closed, empty, _ := pg.removePane(paneID)
	if closed == nil {
		return
	}
	u.detachTabSession(closed)
	if empty {
		// Should not happen when leafCount > 1 pre-remove, but be safe.
		u.removePageAt(pi, false)
		return
	}
	u.syncChrome()
	u.postLayoutSettle()
	u.toast(fmt.Sprintf("%d panes", pg.leafCount()))
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
	u.publishBridgeSnapshot()
}

// closePageAt removes a chrome page and all its panes.
// interactive last-page → confirm quit; non-interactive last-page → quit.
func (u *winUI) closePageAt(idx int, interactive bool) {
	if idx < 0 || idx >= len(u.pages) {
		return
	}
	if len(u.pages) == 1 {
		if interactive {
			log.Info("last tab close — confirm quit")
			r := u.chrome.UpdateChrome(chrome.OpenConfirmQuitMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			if u.hwnd != 0 {
				win.InvalidateRect(u.hwnd, nil, false)
			}
			return
		}
		log.Info("last shell exited — quitting")
		applog.Sync()
		u.persistWindowPlacement(true)
		if u.hwnd != 0 {
			win.DestroyWindow(u.hwnd)
		}
		return
	}
	u.removePageAt(idx, true)
}

// removePageAt detaches a page, closes all panes, adjusts active index.
func (u *winUI) removePageAt(idx int, toast bool) {
	defer applog.Recover("removePageAt", false)
	if idx < 0 || idx >= len(u.pages) {
		return
	}
	pg := u.pages[idx]
	leaves := pg.leaves()
	// Detach page first so paint never sees it.
	u.pages = append(u.pages[:idx], u.pages[idx+1:]...)
	if u.active >= len(u.pages) {
		u.active = len(u.pages) - 1
	} else if u.active > idx {
		u.active--
	}
	if u.active < 0 {
		u.active = 0
	}
	for _, t := range leaves {
		u.detachTabSession(t)
	}
	if at := u.activeTab(); at != nil {
		at.sel.clear()
		setWindowTitle(u.hwnd, "suzuri — "+at.title)
	}
	u.syncChrome()
	u.postLayoutSettle()
	if toast {
		msg := fmt.Sprintf("%d tabs", len(u.pages))
		if len(u.pages) == 1 {
			msg = "1 tab"
		}
		u.toast(msg)
	}
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
	u.publishBridgeSnapshot()
	applog.Sync()
}

// detachTabSession removes t from the flat tabs list and closes the session.
func (u *winUI) detachTabSession(t *tab) {
	if t == nil {
		return
	}
	for i, x := range u.tabs {
		if x == t || (x != nil && x.id == t.id) {
			u.tabs = append(u.tabs[:i], u.tabs[i+1:]...)
			break
		}
	}
	func() {
		defer applog.Recover("tab.close", false)
		t.close()
	}()
}

// focusPaneDir moves focus among panes on the active page (0L 1R 2U 3D).
func (u *winUI) focusPaneDir(dir int) {
	pg := u.activePage()
	if pg == nil {
		return
	}
	prev := u.activeTab()
	prevAlt := prev != nil && prev.altScreen()
	layouts := u.computeActiveLayout()
	if !pg.focusNeighbor(dir, layouts) {
		return
	}
	u.selecting = false
	if t := u.activeTab(); t != nil {
		setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
	}
	u.syncChrome()
	// Only reflow when Warp bar height would change (alt-screen focus).
	// Never ResizePseudoConsole under dual Grok — paint-only if unsafe.
	next := u.activeTab()
	nextAlt := next != nil && next.altScreen()
	if prevAlt != nextAlt {
		if u.anyPaneConPtyBusy() {
			u.layoutDeferred = true
			u.relayoutActivePaintOnly()
		} else {
			u.postLayoutSettle()
		}
	} else {
		u.computeActiveLayout()
	}
	if u.hwnd != 0 {
		u.requestPaint()
	}
}

// focusPaneByID sets focus to a pane on the active page (click-to-focus).
// Avoids layout settle unless alt-screen bar visibility changes.
func (u *winUI) focusPaneByID(id int) bool {
	pg := u.activePage()
	if pg == nil || pg.focusID == id {
		return false
	}
	prev := u.activeTab()
	prevAlt := prev != nil && prev.altScreen()
	if !pg.setFocus(id) {
		return false
	}
	u.selecting = false
	if t := u.activeTab(); t != nil {
		setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
	}
	u.syncChrome()
	next := u.activeTab()
	nextAlt := next != nil && next.altScreen()
	if prevAlt != nextAlt {
		if u.anyPaneConPtyBusy() {
			u.layoutDeferred = true
			u.relayoutActivePaintOnly()
		} else {
			u.postLayoutSettle()
		}
	} else {
		u.computeActiveLayout()
	}
	if u.hwnd != 0 {
		u.requestPaint()
	}
	return true
}

// newTabFromOpts is shared by newTabUI (also used after profile resolution).
func (u *winUI) addPageWithTab(t *tab) {
	if t == nil {
		return
	}
	u.tabs = append(u.tabs, t)
	u.pages = append(u.pages, newPage(t))
	u.active = len(u.pages) - 1
}

// splitOptsFromProfile builds launch opts (same as new tab).
func splitOptsFromProfile(cfg config.Config, profileName string) tabOpts {
	if profileName == "" {
		profileName = cfg.ActiveProfile
	}
	opts := tabOpts{}
	if p := config.FindProfile(cfg, profileName); p != nil {
		opts.shell = p.Shell
		opts.cwd = p.Cwd
		if p.Name != "" && !strings.EqualFold(p.Name, "Default") {
			opts.title = p.Name
		}
	}
	return opts
}
