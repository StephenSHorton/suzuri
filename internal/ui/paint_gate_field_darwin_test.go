//go:build darwin

package ui

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// These drive renderFrame, the decision + framebuffer half of paintTo, with
// a real software painter. Only the texture upload is left out.

// fieldPane is a Claude Code pane: a foreground program, cursor hidden.
func fieldPane(id int, alt bool) *tab {
	term := vt10x.New(vt10x.WithSize(100, 30))
	p := &tab{id: id, term: term, sb: newScrollback(), alive: atomicBool(true), forceChild: true}
	if alt {
		term.Write([]byte("\x1b[?1049h"))
		p.wasAlt = true
	}
	term.Write([]byte("\x1b[?25l✻ Thinking…\r\n> \r\n── claude ──\r\n⏵⏵ bypass permissions on"))
	p.agentReported.Store(agentIdle)
	return p
}

// paintUI gives u a real painter and a framebuffer the size of the window.
func paintUI(u *macUI, w, h int) {
	u.painter = newSoftwarePainter("", 14)
	cw, ch := u.painter.metrics()
	u.metricW, u.metricH = int32(cw), int32(ch)
	u.width, u.height = int32(w), int32(h)
	u.fb = image.NewRGBA(image.Rect(0, 0, w, h))
	u.alive.Store(true)
	u.jobs = make(chan func(), 16)
}

// fieldUI is the reported window: one tab whose page is a 2x2 split of
// Claude Code panes (three alt-screen, one inline), maximized, glass on.
func fieldUI(t *testing.T) (*macUI, []*tab, time.Time) {
	t.Helper()
	panes := []*tab{fieldPane(1, true), fieldPane(2, true), fieldPane(3, false), fieldPane(4, true)}
	pg := newPage(panes[0])
	pg.splitFocused(splitVert, panes[1])
	pg.splitFocused(splitHoriz, panes[2])
	pg.focusID = panes[0].id
	pg.splitFocused(splitHoriz, panes[3])
	if pg.leafCount() != 4 {
		t.Fatalf("setup: %d leaves", pg.leafCount())
	}
	t0 := time.Unix(1_800_000_000, 0)
	u := &macUI{tabs: panes, pages: []*page{pg}, cols: 200, rows: 60, blinkStart: t0}
	u.cfg.Backdrop = config.BackdropGlass
	paintUI(u, 1728, 1084)
	u.syncChrome()
	return u, panes, t0
}

func gateFrame(u *macUI, now time.Time) (full bool, rows []rowSpan) {
	return u.renderFrame(u.activeTab(), int(u.width), int(u.height), now)
}

// spinTick is what Update does each tick: refreshBackgroundTabSpinner, with
// the dots frame forced stale so the tick lands on the test clock.
func spinTick(u *macUI) {
	u.stripDots = "stale"
	u.refreshBackgroundTabSpinner()
}

// feed queues PTY bytes for a pane and runs the drain job Update would run.
func feed(t *testing.T, u *macUI, p *tab, b string) {
	t.Helper()
	p.inMu.Lock()
	p.inBuf = append(p.inBuf, b...)
	p.inMu.Unlock()
	u.queueBytes(p.id)
	select {
	case fn := <-u.jobs:
		u.runJob(fn)
	default:
		t.Fatal("drain job not queued")
	}
}

// inRows reports whether every row band lies inside one of allowed.
func inRows(rows, allowed []rowSpan) bool {
	for _, r := range rows {
		ok := false
		for _, a := range allowed {
			if r.y0 >= a.y0 && r.y1 <= a.y1 {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func titleRows(u *macUI) []rowSpan {
	out := []rowSpan{{0, int(u.titleStripHeight()) + int(u.metricH)}}
	for _, g := range u.lastPaneLayout {
		out = append(out, rowSpan{int(g.titleY), int(g.titleY + g.titleH)})
	}
	return out
}

func TestSplitPageSpinnerPaintsOnlyTitleRows(t *testing.T) {
	u, panes, t0 := fieldUI(t)
	panes[2].agentReported.Store(agentWorking)
	if full, _ := gateFrame(u, t0); !full {
		t.Fatal("first frame not full")
	}
	ticks, fulls := 0, 0
	n := int(paintHeartbeat/vsync) - 1
	for i := 1; i <= n; i++ {
		now := t0.Add(time.Duration(i) * vsync)
		if i%5 == 0 { // DotsFrame turns every 80ms
			spinTick(u)
			ticks++
		}
		full, rows := gateFrame(u, now)
		if full {
			fulls++
			continue
		}
		if !inRows(rows, titleRows(u)) {
			t.Fatalf("vsync %d uploaded %v, want only strip/title rows %v", i, rows, titleRows(u))
		}
	}
	if ticks == 0 || fulls != 0 {
		t.Fatalf("%d spinner ticks on a split page cost %d full frames, want 0", ticks, fulls)
	}
}

func TestTitleRepaintMatchesFullFrame(t *testing.T) {
	// A blocked pane shows a static mark, so a full frame at the same
	// instant must equal the strip/title repaint pixel for pixel.
	u, panes, t0 := fieldUI(t)
	gateFrame(u, t0)
	before := append([]byte(nil), u.fb.Pix...)
	panes[1].agentReported.Store(agentBlocked)
	u.syncChrome()
	u.chromeDirty = true
	now := t0.Add(vsync)
	if full, rows := gateFrame(u, now); full || len(rows) == 0 {
		t.Fatalf("title change: full=%v rows=%v, want a row-scoped frame", full, rows)
	}
	got := append([]byte(nil), u.fb.Pix...)
	if bytes.Equal(before, got) {
		t.Fatal("title repaint changed no pixels")
	}
	u.requestPaint()
	gateFrame(u, now)
	if !bytes.Equal(got, u.fb.Pix) {
		t.Fatal("title-row repaint differs from a full frame")
	}
}

func TestIdleFieldPageFullFramesPerSecond(t *testing.T) {
	u, _, t0 := fieldUI(t)
	const span = 10 * time.Second
	fulls := 0
	for i := 0; i < int(span/vsync); i++ {
		now := t0.Add(time.Duration(i) * vsync)
		spinTick(u)
		if full, _ := gateFrame(u, now); full {
			fulls++
		}
	}
	perSec := float64(fulls) / span.Seconds()
	t.Logf("idle 4-pane page: %d full frames in %v (%.2f/s)", fulls, span, perSec)
	if perSec > 0.6 {
		t.Fatalf("idle page painted %.2f full frames/s, want <= 0.6", perSec)
	}
}

func TestWorkingPaneProgressSlidesWithoutFullFrames(t *testing.T) {
	u, panes, t0 := fieldUI(t)
	// Claude Code while working: OSC 9;4;3 indeterminate progress.
	panes[1].agentReported.Store(agentWorking)
	feed(t, u, panes[1], "\x1b]9;4;3;\x07")
	if panes[1].modes.progress.kind != 3 {
		t.Fatalf("setup: progress kind %d, want 3", panes[1].modes.progress.kind)
	}
	gateFrame(u, t0)
	fulls, slides := 0, 0
	for i := 1; i < int(time.Second/vsync); i++ {
		now := t0.Add(time.Duration(i) * vsync)
		if i%5 == 0 {
			spinTick(u)
		}
		need := u.frameNeed(now)
		full, rows := gateFrame(u, now)
		switch {
		case full:
			fulls++
		case need&frameProgress != 0:
			slides++
			band := rowSpan{u.progUnders[0].y, u.progUnders[0].y + progressBarH}
			if !inRows(rows, append(titleRows(u), band)) {
				t.Fatalf("progress step uploaded %v, want the bar band %v", rows, band)
			}
		}
	}
	t.Logf("working pane + progress: %d full, %d progress frames in 1s", fulls, slides)
	if fulls != 0 {
		t.Fatalf("indeterminate progress cost %d full frames/s, want 0", fulls)
	}
	if slides < 10 || slides > 13 {
		t.Fatalf("progress stepped %d times/s, want ~12", slides)
	}
}

func TestProgressSlideMatchesFullFrame(t *testing.T) {
	u, panes, t0 := fieldUI(t)
	feed(t, u, panes[3], "\x1b]9;4;3;\x07")
	gateFrame(u, t0)
	now := t0.Add(progressStep * 3)
	if need := u.frameNeed(now); need != frameProgress {
		t.Fatalf("frameNeed=%d, want frameProgress", need)
	}
	before := append([]byte(nil), u.fb.Pix...)
	gateFrame(u, now)
	got := append([]byte(nil), u.fb.Pix...)
	if bytes.Equal(before, got) {
		t.Fatal("progress slide changed no pixels")
	}
	u.requestPaint()
	gateFrame(u, now)
	if !bytes.Equal(got, u.fb.Pix) {
		t.Fatal("progress slide differs from a full frame")
	}
}

func TestOffPagePTYDrainPaintsOnlyStrip(t *testing.T) {
	u, panes, t0 := fieldUI(t)
	bg := fieldPane(9, true)
	u.tabs = append(u.tabs, bg)
	u.pages = append(u.pages, newPage(bg))
	u.syncChrome()
	gateFrame(u, t0)

	feed(t, u, bg, "\x1b]0;⠂ Claude Code\x07\x1b[H✻ Working…")
	if u.paintPending {
		t.Fatal("PTY drain for a pane on another page requested a full frame")
	}
	if need := u.frameNeed(t0.Add(vsync)); need&frameFull != 0 {
		t.Fatalf("off-page drain: frameNeed=%d, want strip-only", need)
	}
	// A pane on the visible page still repaints.
	feed(t, u, panes[0], "\x1b[H✻ Working…")
	if !u.paintPending {
		t.Fatal("PTY drain for a visible pane did not request a frame")
	}
}

func TestChromeMarkDoesNotPinCaretToFullFrames(t *testing.T) {
	// A shell pane with the Warp bar. Any markChromeDirty used to leave
	// overlayDirty set with no card open, so every caret step after it fell
	// through the bar-only path into a full frame.
	term := vt10x.New(vt10x.WithSize(100, 30))
	pane := &tab{id: 1, term: term, sb: newScrollback(), alive: atomicBool(true)}
	t0 := time.Unix(1_800_000_000, 0)
	u := &macUI{tabs: []*tab{pane}, pages: []*page{newPage(pane)}, cols: 100, rows: 30, blinkStart: t0}
	paintUI(u, 1000, 700)
	if !u.barCaretShown() {
		t.Fatal("setup: no Warp bar")
	}
	u.markChromeDirty()
	gateFrame(u, t0)
	u.markInputDirty()
	if !u.inputOnlyDirty {
		t.Fatal("markInputDirty after a chrome frame did not take the bar-only path")
	}
	fulls, steps := 0, 0
	for i := 1; i < int(cursorBlinkPeriod/vsync); i++ {
		now := t0.Add(time.Duration(i) * vsync)
		need := u.frameNeed(now)
		full, _ := gateFrame(u, now)
		if full {
			fulls++
		} else if need != frameSkip {
			steps++
		}
	}
	if fulls != 0 || steps < caretSteps/2 {
		t.Fatalf("caret pulse after markChromeDirty: %d full, %d bar-only frames; want 0 full", fulls, steps)
	}
}

func TestBlinkingShellCursorStepsOnlyItsCell(t *testing.T) {
	// vim-style alt-screen program with a visible, blinking block cursor.
	term := vt10x.New(vt10x.WithSize(100, 30))
	pane := &tab{id: 1, term: term, sb: newScrollback(), alive: atomicBool(true), forceChild: true}
	t0 := time.Unix(1_800_000_000, 0)
	u := &macUI{tabs: []*tab{pane}, cols: 100, rows: 30, blinkStart: t0}
	u.cfg.Backdrop = config.BackdropGlass
	paintUI(u, 1000, 700)
	feed(t, u, pane, "\x1b[?1049h\x1b[?25h\x1b[1 qhello\r\nwor")
	if !u.shellCaretShown() {
		t.Fatal("setup: no blinking shell cursor")
	}
	gateFrame(u, t0)
	first := append([]byte(nil), u.fb.Pix...)
	ch := int(u.metricH)
	fulls, steps := 0, 0
	var last time.Time
	for i := 1; i < int(cursorBlinkPeriod/vsync); i++ {
		now := t0.Add(time.Duration(i) * vsync)
		full, rows := gateFrame(u, now)
		switch {
		case full:
			fulls++
		case len(rows) > 0:
			steps++
			last = now
			if len(rows) != 1 || rows[0].y1-rows[0].y0 != ch {
				t.Fatalf("caret step uploaded %v, want one %dpx cell row", rows, ch)
			}
		}
	}
	if fulls != 0 || steps < caretSteps-2 {
		t.Fatalf("blinking cursor: %d full, %d cell frames per blink; want 0 full, ~%d cell", fulls, steps, caretSteps)
	}
	got := append([]byte(nil), u.fb.Pix...)
	if bytes.Equal(first, got) {
		t.Fatal("cursor steps changed no pixels")
	}
	u.requestPaint()
	gateFrame(u, last)
	if !bytes.Equal(got, u.fb.Pix) {
		t.Fatal("cursor-cell repaint differs from a full frame")
	}
}

func TestSinglePaneIdleFullFramesPerSecond(t *testing.T) {
	u, t0 := inlineProgramUI(t, "\x1b[?25l> \r\n── chat-2 ──\r\n⏵⏵ bypass permissions on (shift+tab to cycle)")
	paintUI(u, 1728, 1084)
	const span = 10 * time.Second
	fulls := 0
	for i := 0; i < int(span/vsync); i++ {
		if full, _ := gateFrame(u, t0.Add(time.Duration(i)*vsync)); full {
			fulls++
		}
	}
	perSec := float64(fulls) / span.Seconds()
	t.Logf("single idle pane: %d full frames in %v (%.2f/s)", fulls, span, perSec)
	if perSec > 0.6 {
		t.Fatalf("idle pane painted %.2f full frames/s, want <= 0.6", perSec)
	}
}

func TestMergeRows(t *testing.T) {
	got := mergeRows([]rowSpan{{30, 40}, {-5, 10}, {8, 12}, {40, 41}, {90, 120}, {5, 5}}, 100)
	want := []rowSpan{{0, 12}, {30, 41}, {90, 100}}
	if len(got) != len(want) {
		t.Fatalf("mergeRows=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeRows=%v, want %v", got, want)
		}
	}
}

func TestPaneGoingIdleRepaintsItsTitle(t *testing.T) {
	// Busy can end on a clock (no PTY event). The next spinner tick must
	// still repaint the title rows, or the mark sits there until the heartbeat.
	u, panes, t0 := fieldUI(t)
	panes[2].agentReported.Store(agentWorking)
	gateFrame(u, t0)
	spinTick(u)
	gateFrame(u, t0.Add(vsync))
	panes[2].agentReported.Store(agentIdle)
	spinTick(u)
	if need := u.frameNeed(t0.Add(2 * vsync)); need != frameChrome {
		t.Fatalf("tick after the last busy pane went idle: frameNeed=%d, want frameChrome", need)
	}
}

func TestBarOnUnfocusedLeafDoesNotPulse(t *testing.T) {
	// Only the focused leaf's bar draws a caret. A shell neighbor's bar must
	// not wake the gate while the focused Claude pane hides its cursor.
	u, panes, t0 := fieldUI(t)
	panes[2].forceChild = false // the inline leaf is back at a shell prompt
	gateFrame(u, t0)
	if u.inputBarPixelHeight() < 1 {
		t.Fatal("setup: no Warp bar on the shell leaf")
	}
	for i := 1; i < int(cursorBlinkPeriod/vsync); i++ {
		if need := u.frameNeed(t0.Add(time.Duration(i) * vsync)); need != frameSkip {
			t.Fatalf("vsync %d: frameNeed=%d with only an unfocused bar, want skip", i, need)
		}
	}
}
