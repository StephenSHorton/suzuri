//go:build darwin

package ui

import (
	"image"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

// inlineProgramUI is a window showing one main-screen pane owned by a
// foreground program (Claude Code rendering inline after leaving alt).
func inlineProgramUI(t *testing.T, ptyBytes string) (*macUI, time.Time) {
	t.Helper()
	term := vt10x.New(vt10x.WithSize(107, 32))
	pane := &tab{id: 1, term: term, sb: newScrollback(), alive: atomicBool(true), forceChild: true}
	term.Write([]byte(ptyBytes))
	if pane.altScreen() || !pane.programOwnsKeys() {
		t.Fatalf("setup: alt=%v owns=%v, want main-screen program pane", pane.altScreen(), pane.programOwnsKeys())
	}
	t0 := time.Unix(1_800_000_000, 0)
	u := &macUI{tabs: []*tab{pane}, cols: 107, rows: 32, width: 1000, height: 700, blinkStart: t0}
	return u, t0
}

const vsync = time.Second / 60

func TestIdleInlinePaneDoesNotRepaintEveryFrame(t *testing.T) {
	// Status line + footer, then Claude hides the cursor and goes quiet.
	u, t0 := inlineProgramUI(t, "\x1b[?25l> \r\n── chat-2 ──\r\n⏵⏵ bypass permissions on (shift+tab to cycle)")
	u.notePainted(t0)
	frames := 0
	for i := 1; i < int(paintHeartbeat/vsync); i++ {
		if u.frameDue(t0.Add(time.Duration(i) * vsync)) {
			frames++
		}
	}
	if frames != 0 {
		t.Fatalf("idle pane repainted %d of %d vsyncs, want 0 before the heartbeat", frames, int(paintHeartbeat/vsync)-1)
	}
	if !u.frameDue(t0.Add(paintHeartbeat)) {
		t.Fatal("heartbeat frame not due")
	}
}

func TestPaintRequestsWakeIdlePane(t *testing.T) {
	u, t0 := inlineProgramUI(t, "\x1b[?25l")
	now := t0.Add(vsync)
	for _, c := range []struct {
		name string
		mark func()
	}{
		{"shell", u.markShellDirty},
		{"chrome", u.markChromeDirty},
		{"input", u.markInputDirty},
	} {
		u.notePainted(t0)
		u.chromeDirty = false
		if u.frameDue(now) {
			t.Fatalf("%s: due before mark", c.name)
		}
		c.mark()
		if !u.frameDue(now) {
			t.Fatalf("%s: mark did not request a frame", c.name)
		}
	}
	// overlayDirty with no overlay open is never cleared (ensureOverlayCells
	// returns early); it must not pin the gate open.
	u.notePainted(t0)
	u.chromeDirty = false
	u.overlayDirty = true
	if u.frameDue(now) {
		t.Fatal("stale overlayDirty forced a frame with no overlay open")
	}
}

func TestVisibleCaretRepaintsAtPulseRate(t *testing.T) {
	u, t0 := inlineProgramUI(t, "\x1b[?25h> ")
	u.notePainted(t0)
	frames := 0
	n := int(cursorBlinkPeriod / vsync)
	for i := 1; i <= n; i++ {
		now := t0.Add(time.Duration(i) * vsync)
		if u.frameDue(now) {
			frames++
			u.notePainted(now)
		}
	}
	if frames > caretSteps+1 || frames < caretSteps/2 {
		t.Fatalf("caret pulse painted %d frames per %v, want ~%d", frames, cursorBlinkPeriod, caretSteps)
	}
}

func TestCaretAlphaHoldsWithinStep(t *testing.T) {
	u := &macUI{}
	step := cursorBlinkPeriod / caretSteps
	a, b := u.caretAlphaAt(step/10), u.caretAlphaAt(step*9/10)
	if a != b {
		t.Fatalf("caret alpha moved inside one step: %v vs %v", a, b)
	}
	if c := u.caretAlphaAt(cursorBlinkPeriod / 4); c == a {
		t.Fatalf("caret alpha did not pulse: %v at 0 and at quarter period", c)
	}
}

func TestStripSpinnerRepaintsOnlyTheStrip(t *testing.T) {
	// A pane stuck "working" ticks the strip spinner every 80ms
	// (refreshBackgroundTabSpinner sets chromeDirty). That must not cost a
	// full-window frame each time.
	u, t0 := inlineProgramUI(t, "\x1b[?25l")
	u.notePainted(t0)
	u.chromeDirty = true
	if got := u.frameNeed(t0.Add(vsync)); got != frameChrome {
		t.Fatalf("spinner tick on a single pane: frameNeed=%d, want frameChrome (%d)", got, frameChrome)
	}
	// Strip frames do not reset the heartbeat, so the shell still refreshes.
	if got := u.frameNeed(t0.Add(paintHeartbeat)); got != frameFull {
		t.Fatalf("heartbeat during spinner: frameNeed=%d, want frameFull", got)
	}
	// Split pages also spin pane-title marks inside the shell.
	pg := newPage(u.tabs[0])
	pg.splitFocused(splitVert, &tab{id: 2, sb: newScrollback()})
	u.pages = []*page{pg}
	if got := u.frameNeed(t0.Add(vsync)); got != frameFull {
		t.Fatalf("spinner tick on a split page: frameNeed=%d, want frameFull", got)
	}
}

func TestPaintChromeStripReturnsStripHeight(t *testing.T) {
	p := newSoftwarePainter("", 14)
	_, ch := p.metrics()
	fb := image.NewRGBA(image.Rect(0, 0, 200, 120))
	row := make([]cellPix, 10)
	got := p.paintChromeStrip(fb, [][]cellPix{row, row}, 6)
	if got != 2*ch+6 {
		t.Fatalf("strip height %d, want %d", got, 2*ch+6)
	}
	// Nothing below the strip is touched (only those rows are uploaded).
	if a := fb.Pix[fb.PixOffset(0, got)+3]; a != 0 {
		t.Fatalf("pixel below strip painted (alpha %d)", a)
	}
}
