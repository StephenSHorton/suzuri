//go:build windows || darwin

package ui

import (
	"os"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/aicontrol"
)

// aiSurface is the window-agnostic half of HTTP control (layout + tree ops).
type aiSurface struct {
	pages  *[]*page
	tabs   []*tab
	active *int
	split  func(splitDir)
	close  func(paneID int)
	newTab func()
	after  func()
}

func (s aiSurface) pageList() []*page {
	if s.pages == nil {
		return nil
	}
	return *s.pages
}

func (s aiSurface) setPages(p []*page) {
	if s.pages != nil {
		*s.pages = p
	}
}

func applyAIJob(s aiSurface, job aicontrol.Job) {
	if job.Reply == nil {
		return
	}
	switch job.Kind {
	case aicontrol.KindLayout:
		job.Reply <- aicontrol.JSONOK(layoutJSON(s))
	case aicontrol.KindPane:
		v, ok := paneJSON(s, job.Pane, job.Lines)
		if !ok {
			job.Reply <- aicontrol.JSONErr(404, "no such pane")
			return
		}
		job.Reply <- aicontrol.JSONOK(v)
	case aicontrol.KindCall:
		v, status, err := applyAICall(s, job.Tool, job.Args)
		if err != "" {
			job.Reply <- aicontrol.JSONErr(status, err)
			return
		}
		job.Reply <- aicontrol.JSONOK(v)
	default:
		job.Reply <- aicontrol.JSONErr(400, "bad job")
	}
}

func layoutJSON(s aiSurface) map[string]any {
	focus := 0
	activeTab := 0
	pages := s.pageList()
	if s.active != nil && *s.active >= 0 && *s.active < len(pages) && pages[*s.active] != nil {
		pg := pages[*s.active]
		activeTab = pg.id
		if t := pg.focused(); t != nil {
			focus = t.id
		}
	}
	var tabs []map[string]any
	for i, pg := range pages {
		if pg == nil {
			continue
		}
		var panes []map[string]any
		for _, t := range pg.leaves() {
			if t == nil {
				continue
			}
			if v, ok := paneJSON(s, t.id, 12); ok {
				panes = append(panes, v)
			}
		}
		tabs = append(tabs, map[string]any{
			"id":         pg.id,
			"title":      pg.title(),
			"active":     s.active != nil && i == *s.active,
			"focus_pane": pg.focusID,
			"tree":       treeJSON(pg.root),
			"panes":      panes,
		})
	}
	return map[string]any{
		"pid":        os.Getpid(),
		"focus_pane": focus,
		"active_tab": activeTab,
		"windows": []map[string]any{{
			"surface": 0,
			"tabs":    tabs,
		}},
	}
}

func paneJSON(s aiSurface, id, lines int) (map[string]any, bool) {
	t := findTab(s, id)
	if t == nil {
		return nil, false
	}
	if lines < 1 {
		lines = 12
	}
	var hist []string
	for _, hl := range t.sb.historyTail(lines) {
		hist = append(hist, hl.text)
	}
	live := trimLiveLines(snapshotLiveText(t.term))
	return map[string]any{
		"id":        t.id,
		"title":     t.displayTitle(),
		"kind":      "terminal",
		"cwd":       t.cwd,
		"cols":      t.lastCols,
		"rows":      t.lastRows,
		"guest_url": nil,
		"tail":      hist,
		"live":      live,
	}, true
}

func treeJSON(n *splitNode) map[string]any {
	if n == nil {
		return map[string]any{"pane": 0}
	}
	if n.isLeaf() && n.pane != nil {
		return map[string]any{"pane": n.pane.id}
	}
	axis := "right"
	if n.dir == splitHoriz {
		axis = "down"
	}
	return map[string]any{
		"axis":  axis,
		"ratio": n.ratio,
		"a":     treeJSON(n.a),
		"b":     treeJSON(n.b),
	}
}

func applyAICall(s aiSurface, tool string, args map[string]any) (map[string]any, int, string) {
	switch tool {
	case "layout":
		return layoutJSON(s), 200, ""
	case "pane":
		id, ok := aicontrol.ArgUint(args, "pane_id")
		if !ok {
			return nil, 400, "pane_id required"
		}
		lines := 80
		if n, ok := aicontrol.ArgUint(args, "lines"); ok && n > 0 {
			lines = n
		}
		v, found := paneJSON(s, id, lines)
		if !found {
			return nil, 404, "no such pane"
		}
		return v, 200, ""
	case "focus":
		id, ok := aicontrol.ArgUint(args, "pane_id")
		if !ok {
			return nil, 400, "pane_id required"
		}
		if !focusPane(s, id) {
			return nil, 404, "no such pane"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "split":
		if id, ok := aicontrol.ArgUint(args, "pane_id"); ok {
			if !focusPane(s, id) {
				return nil, 404, "no such pane"
			}
		}
		axis := "right"
		if v, ok := aicontrol.ArgString(args, "axis"); ok && v != "" {
			axis = v
		}
		dir := splitVert
		switch strings.ToLower(strings.TrimSpace(axis)) {
		case "down", "below", "horizontal", "h":
			dir = splitHoriz
		case "right", "vertical", "v":
			dir = splitVert
		default:
			return nil, 400, "axis must be right|down"
		}
		if s.split == nil {
			return nil, 500, "split unavailable"
		}
		s.split(dir)
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "close":
		if id, ok := aicontrol.ArgUint(args, "pane_id"); ok {
			if !focusPane(s, id) {
				return nil, 404, "no such pane"
			}
		}
		id := focusedID(s)
		if id < 0 || s.close == nil {
			return nil, 400, "no pane"
		}
		s.close(id)
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "rename":
		id, ok := aicontrol.ArgUint(args, "pane_id")
		if !ok {
			return nil, 400, "pane_id required"
		}
		title, ok := aicontrol.ArgString(args, "title")
		if !ok {
			return nil, 400, "title required"
		}
		t := findTab(s, id)
		if t == nil {
			return nil, 404, "no such pane"
		}
		t.setUserTitle(title)
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "new_tab":
		if s.newTab == nil {
			return nil, 500, "new_tab unavailable"
		}
		s.newTab()
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "grow":
		return ratioOp(s, args, 0.08)
	case "shrink":
		return ratioOp(s, args, -0.08)
	case "equalize":
		id, err := paneOrFocus(s, args)
		if err != "" {
			return nil, 400, err
		}
		pg := pageOf(s, id)
		if pg == nil || !pg.equalizeAround(id) {
			return nil, 400, "no split"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "rotate":
		id, err := paneOrFocus(s, args)
		if err != "" {
			return nil, 400, err
		}
		pg := pageOf(s, id)
		if pg == nil || !pg.rotateAround(id) {
			return nil, 400, "no split"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "swap":
		id, err := paneOrFocus(s, args)
		if err != "" {
			return nil, 400, err
		}
		pg := pageOf(s, id)
		if pg == nil || !pg.swapAround(id) {
			return nil, 400, "no split"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "move":
		pane, ok := aicontrol.ArgUint(args, "pane_id")
		if !ok {
			return nil, 400, "pane_id required"
		}
		target, ok := aicontrol.ArgUint(args, "target_pane_id")
		if !ok {
			return nil, 400, "target_pane_id required"
		}
		edge, ok := aicontrol.ArgString(args, "edge")
		if !ok {
			return nil, 400, "edge must be left|right|top|bottom"
		}
		if !movePane(s, pane, target, edge) {
			return nil, 400, "move failed"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	case "move_to_tab":
		pane, ok := aicontrol.ArgUint(args, "pane_id")
		if !ok {
			return nil, 400, "pane_id required"
		}
		tabID, hasTab := aicontrol.ArgUint(args, "tab_id")
		if !movePaneToTab(s, pane, tabID, hasTab) {
			return nil, 400, "move_to_tab failed"
		}
		if s.after != nil {
			s.after()
		}
		return layoutJSON(s), 200, ""
	default:
		return nil, 404, "unknown tool " + tool
	}
}

func ratioOp(s aiSurface, args map[string]any, delta float64) (map[string]any, int, string) {
	id, err := paneOrFocus(s, args)
	if err != "" {
		return nil, 400, err
	}
	pg := pageOf(s, id)
	if pg == nil || !pg.adjustRatio(id, delta) {
		return nil, 400, "no split"
	}
	if s.after != nil {
		s.after()
	}
	return layoutJSON(s), 200, ""
}

func paneOrFocus(s aiSurface, args map[string]any) (int, string) {
	if id, ok := aicontrol.ArgUint(args, "pane_id"); ok {
		if findTab(s, id) == nil {
			return 0, "no such pane"
		}
		focusPane(s, id)
		return id, ""
	}
	id := focusedID(s)
	if id < 0 {
		return 0, "no pane"
	}
	return id, ""
}

func focusedID(s aiSurface) int {
	pages := s.pageList()
	if s.active == nil || *s.active < 0 || *s.active >= len(pages) {
		return -1
	}
	pg := pages[*s.active]
	if pg == nil {
		return -1
	}
	if t := pg.focused(); t != nil {
		return t.id
	}
	return -1
}

func findTab(s aiSurface, id int) *tab {
	for _, t := range s.tabs {
		if t != nil && t.id == id {
			return t
		}
	}
	for _, pg := range s.pageList() {
		if pg == nil {
			continue
		}
		if t := findPane(pg.root, id); t != nil {
			return t
		}
	}
	return nil
}

func pageOf(s aiSurface, paneID int) *page {
	for _, pg := range s.pageList() {
		if pg != nil && findPane(pg.root, paneID) != nil {
			return pg
		}
	}
	return nil
}

func pageIndexOf(s aiSurface, paneID int) int {
	for i, pg := range s.pageList() {
		if pg != nil && findPane(pg.root, paneID) != nil {
			return i
		}
	}
	return -1
}

func focusPane(s aiSurface, id int) bool {
	idx := pageIndexOf(s, id)
	if idx < 0 {
		return false
	}
	if s.active != nil {
		*s.active = idx
	}
	return s.pageList()[idx].setFocus(id)
}

func movePane(s aiSurface, paneID, targetID int, edge string) bool {
	if paneID == targetID {
		return false
	}
	src := pageOf(s, paneID)
	dst := pageOf(s, targetID)
	t := findTab(s, paneID)
	if src == nil || dst == nil || t == nil {
		return false
	}
	closed, empty, _ := src.removePane(paneID)
	if closed == nil {
		return false
	}
	if empty {
		dropPage(s, src)
	}
	if !dst.dockExisting(targetID, edge, t) {
		pages := append(s.pageList(), newPage(t))
		s.setPages(pages)
		if s.active != nil {
			*s.active = len(pages) - 1
		}
		return false
	}
	return true
}

func movePaneToTab(s aiSurface, paneID, tabID int, hasTab bool) bool {
	t := findTab(s, paneID)
	src := pageOf(s, paneID)
	if t == nil || src == nil {
		return false
	}
	if src.leafCount() <= 1 && !hasTab {
		return false // already its own tab
	}
	closed, empty, _ := src.removePane(paneID)
	if closed == nil {
		return false
	}
	if empty {
		dropPage(s, src)
	}
	if !hasTab {
		pages := append(s.pageList(), newPage(t))
		s.setPages(pages)
		if s.active != nil {
			*s.active = len(pages) - 1
		}
		return true
	}
	var dst *page
	for _, pg := range s.pageList() {
		if pg != nil && pg.id == tabID {
			dst = pg
			break
		}
	}
	if dst == nil {
		s.setPages(append(s.pageList(), newPage(t)))
		return false
	}
	focus := dst.focusID
	if !dst.dockExisting(focus, "right", t) {
		s.setPages(append(s.pageList(), newPage(t)))
		return false
	}
	return true
}

func dropPage(s aiSurface, pg *page) {
	cur := s.pageList()
	out := make([]*page, 0, len(cur))
	for _, p := range cur {
		if p != pg {
			out = append(out, p)
		}
	}
	s.setPages(out)
	if s.active != nil && *s.active >= len(out) {
		*s.active = len(out) - 1
	}
	if s.active != nil && *s.active < 0 {
		*s.active = 0
	}
}
