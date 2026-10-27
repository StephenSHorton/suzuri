//go:build darwin

package ui

import (
	"image"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// Draw used to re-raster the whole window and re-upload it as a texture on
// every vsync, even with an idle pane on screen (~100% CPU on a maximized
// glass window). Frames now repaint only when something visual changed, an
// animation is running, or the caret pulse steps; otherwise Draw re-blits the
// last texture. The heartbeat bounds staleness for any path that forgot to
// call requestPaint.
const (
	paintHeartbeat = 250 * time.Millisecond
	// caretSteps quantizes the caret pulse so an idle caret repaints ~10×/s
	// instead of every frame.
	caretSteps = 12
)

// requestPaint asks Draw for a full frame on the next vsync.
func (u *macUI) requestPaint() {
	if u != nil {
		u.paintPending = true
	}
}

// caretStep is the pulse bucket for elapsed time since blinkStart.
func caretStep(elapsed time.Duration) int {
	step := cursorBlinkPeriod / caretSteps
	if elapsed < 0 || step <= 0 {
		return 0
	}
	return int(elapsed / step)
}

// caretShown reports whether a pulsing caret is on screen (Warp bar, or a
// visible non-steady shell cursor in the focused pane).
func (u *macUI) caretShown() bool {
	t := u.activeTab()
	if t == nil {
		return false
	}
	if u.inputBarPixelHeight() > 0 {
		return true
	}
	rows := u.rows
	if g := u.focusedGeom(); g != nil && g.rows > 0 {
		rows = g.rows
	}
	if _, _, vis := t.gridCursor(rows, true); !vis {
		return false
	}
	_, steady := t.modes.shellCursor(int(u.cfg.Cursor))
	return !steady
}

// animating is true while something on screen moves on its own clock.
func (u *macUI) animating(now time.Time) bool {
	if u.chrome.OverlayOpen() || u.chrome.SettingsOpen || u.modalImage != nil {
		return true
	}
	if u.matrixIntroActive() || u.shellAmbientOn() {
		return true
	}
	// Center mark fades in after the intro curtain (see watermarkFade).
	if !u.matrixIntroSpawnEnd.IsZero() && now.Before(u.matrixIntroSpawnEnd.Add(2*time.Second)) {
		return true
	}
	for _, t := range u.visiblePanes() {
		if t == nil {
			continue
		}
		if t.sb != nil && absFloat(t.sb.visual-float64(t.sb.offset)) > 0.01 {
			return true
		}
		// Indeterminate progress bar slides with wall time (paintProgress).
		if t.modes.progress.kind == 3 {
			return true
		}
	}
	return false
}

// visiblePanes is the leaves painted this frame (active page, or active tab).
func (u *macUI) visiblePanes() []*tab {
	if p := u.activePage(); p != nil {
		return p.leaves()
	}
	if t := u.activeTab(); t != nil {
		return []*tab{t}
	}
	return nil
}

// frameNeed is how much of the window Draw must repaint this vsync.
type frameNeed int

const (
	frameSkip   frameNeed = iota // re-present the last texture
	frameCaret                   // only the caret pulse stepped
	frameChrome                  // only the tab strip changed (spinner, caffeine label)
	frameFull
)

// frameNeed decides this vsync's paint. It reads no ebiten state so it can be
// tested without a window.
func (u *macUI) frameNeed(now time.Time) frameNeed {
	if u.paintPending || u.lastPaintAt.IsZero() || now.Sub(u.lastPaintAt) >= paintHeartbeat {
		return frameFull
	}
	if u.animating(now) {
		return frameFull
	}
	caret := u.caretShown() && caretStep(now.Sub(u.blinkStart)) != u.paintCaretStep
	if u.chromeDirty {
		// Split pages also spin busy marks in pane titles, inside the shell.
		if caret || len(u.visiblePanes()) > 1 {
			return frameFull
		}
		return frameChrome
	}
	if caret {
		return frameCaret
	}
	return frameSkip
}

// frameDue reports whether Draw must paint anything this vsync.
func (u *macUI) frameDue(now time.Time) bool {
	return u.frameNeed(now) != frameSkip
}

// notePainted records a full frame. Strip- and bar-only frames do not count,
// so the heartbeat still refreshes the shell while a spinner turns.
func (u *macUI) notePainted(now time.Time) {
	u.paintPending = false
	u.lastPaintAt = now
	u.paintCaretStep = caretStep(now.Sub(u.blinkStart))
}

// tryPaintChromeOnly repaints just the tab strip over the last frame and
// uploads only those rows. A busy pane's spinner ticks every 80ms; a full
// frame for each one costs most of a core on a dense, maximized window.
func (u *macUI) tryPaintChromeOnly(screen *ebiten.Image, w, h int) bool {
	if u.painter == nil || u.fb == nil || u.tex == nil ||
		u.fb.Bounds().Dx() != w || u.fb.Bounds().Dy() != h {
		return false
	}
	u.ensureChromeCells()
	ch := int(u.metricH)
	if ch < 1 {
		ch = cellH
	}
	stripH := u.painter.paintChromeStrip(u.fb, u.chromeCells, int(u.titleStripHeight())-ch)
	if stripH < 1 || stripH > h {
		return false
	}
	u.paintMacLights(u.fb)
	u.paintBrandMark(u.fb)
	u.paintCaffeineCup(u.fb)
	strip := image.Rect(0, 0, w, stripH)
	u.tex.SubImage(strip).(*ebiten.Image).WritePixels(u.fb.Pix[:stripH*u.fb.Stride])
	presentFB(screen, u.tex)
	return true
}

// noteInputForPaint requests a frame on any pointer, key, wheel or focus
// activity. Hover tints, drags, title lights and held-key repeat all paint
// from Update state that has no dirty flag of its own.
func (u *macUI) noteInputForPaint() {
	x, y := ebiten.CursorPosition()
	focused := ebiten.IsFocused()
	wx, wy := ebiten.Wheel()
	active := x != u.paintMouseX || y != u.paintMouseY || focused != u.paintFocused ||
		wx != 0 || wy != 0 ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonMiddle) ||
		inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft) ||
		inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonRight)
	if !active {
		u.paintKeys = inpututil.AppendPressedKeys(u.paintKeys[:0])
		active = len(u.paintKeys) > 0
	}
	if !active {
		u.paintKeys = inpututil.AppendJustReleasedKeys(u.paintKeys[:0])
		active = len(u.paintKeys) > 0
	}
	if !active {
		u.paintChars = ebiten.AppendInputChars(u.paintChars[:0])
		active = len(u.paintChars) > 0
	}
	u.paintMouseX, u.paintMouseY, u.paintFocused = x, y, focused
	if active {
		u.requestPaint()
	}
}
