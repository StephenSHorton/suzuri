//go:build windows || darwin

package ui

import "github.com/charmbracelet/log"

// How far ingestPTY got. Hosts use this to decide paint and bridge work
// without reimplementing the byte pipeline.
const (
	ptyNone = iota
	ptyQuiet
	ptyFrame
)

// ptyHooks carries the few host decisions the shared pipeline cannot see:
// focus (the user is looking at this pane), cell metrics for inline images,
// and fork/cwd side effects.
type ptyHooks struct {
	// Focused is true only when the window is focused and this pane is the
	// active one. Command-finished cards and DEC 1004 use that, not the
	// window bit alone.
	Focused  bool
	Visible  bool
	CellW    int
	CellH    int
	PaneCols int
	OnFork   func(forkPaneRequest)
	OnCwd    func(prev, next string)
	// OnLeaveAlt runs while an alt-screen app is being torn down, after the
	// VT modes are cleared and before the host reflows.
	OnLeaveAlt func()
	OnAlt      func()
}

// ptyResult is what the host still has to do after the shared pipeline.
type ptyResult struct {
	Action       int
	Dirty        bool
	More         bool
	TitleChanged bool
	BusyChanged  bool
	Title        string
}

// ingestPTY is the single PTY→VT path for both hosts. It owns protocol
// filtering, cwd and fork OSC, inline images, Kitty graphics, and the grid
// update. Window title, resize, and paint stay with the host.
func (t *tab) ingestPTY(data []byte, h ptyHooks) ptyResult {
	var res ptyResult
	if t == nil || t.term == nil || len(data) == 0 {
		return res
	}
	before := snapshotScreenText(t.term)
	var spans []osc8Span
	data, spans = t.pullPTY(data, h.Focused, h.Visible)
	if len(data) == 0 {
		if len(spans) > 0 {
			t.modes.observe(before, snapshotScreenText(t.term), spans)
			res.Dirty = true
		}
		res.Action = ptyQuiet
		res.More = t.inputWaiting()
		return res
	}
	data = t.echo.feed(data)
	if clean, path, ok := stripAndTakeCwd(data); ok {
		prev := t.cwd
		t.setCwd(path)
		data = clean
		if h.OnCwd != nil && path != "" {
			h.OnCwd(prev, t.cwd)
		}
	} else {
		data = clean
	}
	{
		clean, reqs := stripAndTakeFork(data)
		data = clean
		if h.OnFork != nil {
			for _, req := range reqs {
				h.OnFork(req)
			}
		}
	}
	{
		clean, paths, blobs := stripAndTakeImages(data)
		data = clean
		if len(paths) > 0 || len(blobs) > 0 {
			cw, ch := h.CellW, h.CellH
			if cw < 1 {
				cw = cellW
			}
			if ch < 1 {
				ch = cellH
			}
			cols := h.PaneCols
			if cols < 1 {
				cols = 80
			}
			t.ingestImages(paths, blobs, cw, ch, cols)
			res.Dirty = true
		}
	}
	if len(data) == 0 {
		res.Action = ptyQuiet
		res.More = t.inputWaiting()
		return res
	}
	t.handleHostQueries(data)
	if t.kittyGfx == nil {
		t.kittyGfx = newKittyGfx()
	}
	data = feedKittyAPCs(t.kittyGfx, data, func(b []byte) {
		t.writeVT(b)
	}, func() (col, row int) {
		c := t.term.Cursor()
		return c.X, c.Y
	})
	if len(data) > 0 {
		t.writeVT(data)
	}
	t.modes.observe(before, snapshotScreenText(t.term), spans)
	if t.sb != nil {
		t.sb.noteScreen(t.term)
		if t.sb.atBottom() {
			t.sb.stickBottom()
		}
	}
	if title := t.term.Title(); title != "" {
		prevBusy := t.busy()
		res.TitleChanged = t.applyTitle(title)
		res.BusyChanged = t.busy() != prevBusy
		res.Title = t.title
	}
	nowAlt := t.altScreen()
	if nowAlt != t.wasAlt {
		if t.wasAlt {
			resetHostAfterAltApp(t.term)
			t.modes.leaveApp()
			if t.kittyGfx != nil {
				t.kittyGfx.clear()
			}
			if h.OnLeaveAlt != nil {
				h.OnLeaveAlt()
			}
			t.markShellIdle()
		}
		t.wasAlt = nowAlt
		log.Info("alt screen", "tab", t.id, "on", nowAlt)
		if h.OnAlt != nil {
			h.OnAlt()
		}
	}
	t.maybeReleaseBarAwaiting()
	res.Action = ptyFrame
	res.Dirty = true
	res.More = t.inputWaiting()
	return res
}

func (t *tab) inputWaiting() bool {
	if t == nil {
		return false
	}
	t.inMu.Lock()
	defer t.inMu.Unlock()
	return len(t.inBuf) > 0
}

// paneWatched reports whether the user is looking at pane: the window has
// focus and pane is the active leaf.
func paneWatched(windowFocused bool, active, pane *tab) bool {
	return windowFocused && active != nil && pane != nil && active == pane
}

func paneMetrics(metricW, metricH, fallbackCols, paneCols int) (cw, ch, cols int) {
	cw, ch = metricW, metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	cols = fallbackCols
	if paneCols > 0 {
		cols = paneCols
	}
	return cw, ch, cols
}
