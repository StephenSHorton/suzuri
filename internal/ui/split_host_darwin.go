//go:build darwin

package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// maxPanesTotal caps concurrent PTY sessions across all tabs (shared with Windows).
const maxPanesTotal = 24

// activePage returns the chrome-active page (may be nil).
func (u *macUI) activePage() *page {
	if u == nil || u.active < 0 || u.active >= len(u.pages) {
		return nil
	}
	return u.pages[u.active]
}

// allPanes flattens every leaf session (for I/O, teardown, bridge).
func (u *macUI) allPanes() []*tab {
	if u == nil {
		return nil
	}
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
func (u *macUI) paneCount() int {
	return len(u.allPanes())
}

// pageByPaneID finds the page owning a pane id.
func (u *macUI) pageByPaneID(id int) (pageIdx int, p *page) {
	for i, pg := range u.pages {
		if findPane(pg.root, id) != nil {
			return i, pg
		}
	}
	return -1, nil
}

func findPaneAcrossPages(u *macUI, id int) *tab {
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

// shellRect returns the pixel region for shell panes (full height under chrome).
// Per-pane Warp bars live inside each leaf, matching Windows layout.
func (u *macUI) shellRect(clientW, clientH int32) (x, y, w, h int32) {
	const padX int32 = 4
	y = u.shellPadY()
	bot := clientH
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
func (u *macUI) computeActiveLayout() []paneGeom {
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
		clientH = int32(u.rows)*ch + u.chromePx
	}
	sx, sy, sw, sh := u.shellRect(clientW, clientH)
	res := layoutPage(p.root, sx, sy, sw, sh, cw, ch, p.focusID)
	u.lastPaneLayout = res.leaves
	u.lastSashes = res.sashes
	u.lastShell.x, u.lastShell.y, u.lastShell.w, u.lastShell.h = res.shellX, res.shellY, res.shellW, res.shellH
	return u.lastPaneLayout
}

// focusedGeom returns layout for the focused pane (recomputes if needed).
func (u *macUI) focusedGeom() *paneGeom {
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
func (u *macUI) paneGeomFor(id int) *paneGeom {
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

// splitActive creates a new pane from the focused session and splits the page.
func (u *macUI) splitActive(dir splitDir) {
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
	u.tabs = append(u.tabs, t)
	t.startWorkers(u)
	u.selecting = false
	focus.sel.clear()
	ebiten.SetWindowTitle("suzuri — " + t.displayTitle())
	u.syncChrome()
	u.applyClientSize(u.width, u.height)
	if dir == splitVert {
		u.toast("split right")
	} else {
		u.toast("split down")
	}
	log.Info("split pane", "dir", dir, "new", t.id, "page", pg.id, "leaves", pg.leafCount())
}

// closePaneUI closes a single pane. Last pane in page closes the page (tab).
func (u *macUI) closePaneUI(paneID int, interactive bool) {
	defer applog.Recover("closePaneUI", false)
	if u.tabByID(paneID) == nil && findPaneAcrossPages(u, paneID) == nil {
		return
	}
	pi, pg := u.pageByPaneID(paneID)
	if pg == nil {
		u.closeTabUI(paneID)
		return
	}
	if pg.leafCount() <= 1 {
		u.closePageAt(pi, interactive)
		return
	}
	closed, empty, _ := pg.removePane(paneID)
	if closed == nil {
		return
	}
	u.detachTabSession(closed)
	if empty {
		u.removePageAt(pi, false)
		return
	}
	u.syncChrome()
	u.applyClientSize(u.width, u.height)
	u.toast(fmt.Sprintf("%d panes", pg.leafCount()))
	u.publishBridgeSnapshot()
}

// closePageAt removes a chrome page and all its panes.
func (u *macUI) closePageAt(idx int, interactive bool) {
	if idx < 0 || idx >= len(u.pages) {
		return
	}
	if len(u.pages) == 1 {
		if interactive {
			log.Info("last tab close — confirm quit")
			r := u.chrome.UpdateChrome(chrome.OpenConfirmQuitMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			return
		}
		log.Info("last shell exited — quitting")
		u.persistWindowPlacement()
		u.quit = true
		return
	}
	u.removePageAt(idx, true)
}

// removePageAt detaches a page, closes all panes, adjusts active index.
func (u *macUI) removePageAt(idx int, toast bool) {
	defer applog.Recover("removePageAt", false)
	if idx < 0 || idx >= len(u.pages) {
		return
	}
	pg := u.pages[idx]
	leaves := pg.leaves()
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
		ebiten.SetWindowTitle("suzuri — " + at.title)
	}
	u.syncChrome()
	u.applyClientSize(u.width, u.height)
	if toast {
		msg := fmt.Sprintf("%d tabs", len(u.pages))
		if len(u.pages) == 1 {
			msg = "1 tab"
		}
		u.toast(msg)
	}
	u.publishBridgeSnapshot()
}

// detachTabSession removes t from the flat tabs list and closes the session.
func (u *macUI) detachTabSession(t *tab) {
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
func (u *macUI) focusPaneDir(dir int) {
	pg := u.activePage()
	if pg == nil {
		return
	}
	layouts := u.computeActiveLayout()
	if !pg.focusNeighbor(dir, layouts) {
		return
	}
	u.selecting = false
	if t := u.activeTab(); t != nil {
		ebiten.SetWindowTitle("suzuri — " + t.displayTitle())
	}
	u.syncChrome()
	u.computeActiveLayout()
}

// focusPaneByID sets focus to a pane on the active page (click-to-focus).
func (u *macUI) focusPaneByID(id int) bool {
	pg := u.activePage()
	if pg == nil || pg.focusID == id {
		return false
	}
	if !pg.setFocus(id) {
		return false
	}
	u.selecting = false
	if t := u.activeTab(); t != nil {
		ebiten.SetWindowTitle("suzuri — " + t.displayTitle())
	}
	u.syncChrome()
	u.computeActiveLayout()
	return true
}

// addPageWithTab appends a new chrome page owning a single pane.
func (u *macUI) addPageWithTab(t *tab) {
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

// sumActivePaneBarHeights totals per-pane bar heights for layout bookkeeping.
func (u *macUI) sumActivePaneBarHeights() int32 {
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		layouts = u.computeActiveLayout()
	}
	var sum int32
	for _, g := range layouts {
		sum += g.barH
	}
	return sum
}
