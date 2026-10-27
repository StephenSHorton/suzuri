//go:build darwin

package ui

import (
	"image"
	"sort"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// Draw used to re-raster the whole window and re-upload it as a texture on
// every vsync, even with an idle pane on screen (~100% CPU on a maximized
// glass window). Frames now repaint fully only when something visual changed
// or an animation is running. Small clocks (strip and pane-title spinners,
// the caret pulse, an indeterminate progress bar) repaint and upload just
// their rows over the last frame; otherwise Draw re-blits the last texture.
// The heartbeat bounds staleness for any path that forgot to call
// requestPaint. A full frame costs 25-58ms on a dense, maximized glass
// window, so even 4/s is a visible CPU floor.
const (
	paintHeartbeat = 2 * time.Second
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

// runJob runs one job posted to the UI thread. Jobs request a full frame,
// except a PTY drain for a pane that is not on screen (drainAndParse sets
// jobOffscreen); that one can only change the strip.
func (u *macUI) runJob(fn func()) {
	u.jobOffscreen = false
	if fn != nil {
		fn()
	}
	if !u.jobOffscreen {
		u.requestPaint()
	}
	u.jobOffscreen = false
}

// caretStep is the pulse bucket for elapsed time since blinkStart.
func caretStep(elapsed time.Duration) int {
	step := cursorBlinkPeriod / caretSteps
	if elapsed < 0 || step <= 0 {
		return 0
	}
	return int(elapsed / step)
}

// caretShown reports whether a pulsing caret is on screen.
func (u *macUI) caretShown() bool {
	return u.barCaretShown() || u.shellCaretShown()
}

// barCaretShown: the focused pane has a Warp bar. Only that bar draws a live
// caret, so a bar on another leaf does not pulse.
func (u *macUI) barCaretShown() bool {
	if u.activeTab() == nil {
		return false
	}
	if g := u.focusedGeom(); g != nil && len(u.lastPaneLayout) > 0 {
		return g.barH > 0
	}
	return u.inputBarPixelHeight() > 0
}

// shellCaretShown: the focused pane shows a visible, non-steady shell cursor.
func (u *macUI) shellCaretShown() bool {
	t := u.activeTab()
	if t == nil {
		return false
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

// animating is true while something on screen moves on its own clock and
// can only be redrawn with a full frame.
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
		if t != nil && t.sb != nil && absFloat(t.sb.visual-float64(t.sb.offset)) > 0.01 {
			return true
		}
	}
	return false
}

// progressSliding: a visible pane shows the indeterminate OSC 9;4;3 bar.
// Claude Code sets it for as long as it works.
func (u *macUI) progressSliding() bool {
	for _, t := range u.visiblePanes() {
		if t != nil && t.modes.progress.kind == 3 {
			return true
		}
	}
	return false
}

// visiblePanes is the leaves painted this frame (active page, or active tab).
// A zoomed page paints only its zoomed leaf.
func (u *macUI) visiblePanes() []*tab {
	if p := u.activePage(); p != nil {
		if p.zoomed() {
			if t := findPane(p.root, p.zoomTarget()); t != nil {
				return []*tab{t}
			}
		}
		return p.leaves()
	}
	if t := u.activeTab(); t != nil {
		return []*tab{t}
	}
	return nil
}

// paneOnScreen reports whether t is painted on the visible page.
func (u *macUI) paneOnScreen(t *tab) bool {
	for _, v := range u.visiblePanes() {
		if v == t {
			return true
		}
	}
	return false
}

// frameNeed is how much of the window Draw must repaint this vsync: skip,
// full, or a set of row-scoped parts painted over the last frame.
type frameNeed int

const (
	frameSkip     frameNeed = 0      // re-present the last texture
	frameCaret    frameNeed = 1 << 0 // the caret pulse stepped (Warp bar, shell cursor cell)
	frameChrome   frameNeed = 1 << 1 // strip changed, or a pane title mark spun
	frameProgress frameNeed = 1 << 2 // an indeterminate progress bar slid
	frameFull     frameNeed = 1 << 3
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
	need := frameSkip
	if u.chromeDirty {
		need |= frameChrome
	}
	if u.caretShown() && caretStep(now.Sub(u.blinkStart)) != u.paintCaretStep {
		need |= frameCaret
	}
	if u.progressSliding() && !progressPhase(now).Equal(u.paintProgAt) {
		need |= frameProgress
	}
	return need
}

// frameDue reports whether Draw must paint anything this vsync.
func (u *macUI) frameDue(now time.Time) bool {
	return u.frameNeed(now) != frameSkip
}

// notePainted records a full frame. Row-scoped frames do not count, so the
// heartbeat still refreshes the shell while a spinner turns.
func (u *macUI) notePainted(now time.Time) {
	u.paintPending = false
	u.lastPaintAt = now
	u.paintCaretStep = caretStep(now.Sub(u.blinkStart))
	u.paintProgAt = progressPhase(now)
}

// rowSpan is a band of framebuffer rows [y0, y1) to upload.
type rowSpan struct{ y0, y1 int }

// mergeRows clamps spans to [0, h), sorts them and joins any that touch, so
// each band uploads once.
func mergeRows(rows []rowSpan, h int) []rowSpan {
	out := make([]rowSpan, 0, len(rows))
	for _, r := range rows {
		r.y0, r.y1 = max(r.y0, 0), min(r.y1, h)
		if r.y1 > r.y0 {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].y0 < out[j].y0 })
	n := 0
	for _, r := range out {
		if n > 0 && r.y0 <= out[n-1].y1 {
			out[n-1].y1 = max(out[n-1].y1, r.y1)
			continue
		}
		out[n] = r
		n++
	}
	return out[:n]
}

// uploadRows copies full-width row bands of u.fb into the texture. Whole
// rows are contiguous in Pix, so no staging copy is needed.
func (u *macUI) uploadRows(rows []rowSpan) {
	w, stride := u.fb.Bounds().Dx(), u.fb.Stride
	for _, r := range mergeRows(rows, u.fb.Bounds().Dy()) {
		band := image.Rect(0, r.y0, w, r.y1)
		u.tex.SubImage(band).(*ebiten.Image).WritePixels(u.fb.Pix[r.y0*stride : r.y1*stride])
	}
}

// paintPartial paints the row-scoped parts of need over the last frame in
// u.fb. ok is false when any part cannot be scoped; the caller then paints a
// full frame, which also covers whatever was already redrawn here.
func (u *macUI) paintPartial(tab *tab, need frameNeed, now time.Time) (rows []rowSpan, ok bool) {
	if u.painter == nil || u.fb == nil {
		return nil, false
	}
	if need&frameChrome != 0 {
		r, ok := u.paintChromeFB()
		if !ok {
			return nil, false
		}
		rows = append(rows, r...)
	}
	if need&frameProgress != 0 {
		r, ok := u.paintProgressFB(now)
		if !ok {
			return nil, false
		}
		rows = append(rows, r...)
		u.paintProgAt = progressPhase(now)
	}
	if need&frameCaret != 0 {
		r, ok := u.paintCaretFB(tab, now)
		if !ok {
			return nil, false
		}
		rows = append(rows, r...)
		u.paintCaretStep = caretStep(now.Sub(u.blinkStart))
	}
	return rows, true
}

// paintChromeFB repaints the tab strip and, on a split page, the pane title
// bands. A busy pane spins both every 80ms (refreshBackgroundTabSpinner);
// a full frame for each tick costs most of a core on a dense, maximized
// window. Title bands are opaque fills, so they redraw cleanly in place.
func (u *macUI) paintChromeFB() ([]rowSpan, bool) {
	h := u.fb.Bounds().Dy()
	u.ensureChromeCells()
	cw, ch := int(u.metricW), int(u.metricH)
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	stripH := u.painter.paintChromeStrip(u.fb, u.chromeCells, int(u.titleStripHeight())-ch)
	if stripH < 1 || stripH > h {
		return nil, false
	}
	u.paintMacLights(u.fb)
	u.paintBrandMark(u.fb)
	u.paintCaffeineCup(u.fb)
	rows := []rowSpan{{0, stripH}}
	if layouts := u.lastPaneLayout; len(layouts) > 1 {
		u.painter.paintPaneTitles(u.fb, layouts, cw, ch)
		// A hairline may touch a title band's edge; its fills are idempotent.
		u.painter.paintPaneSashes(u.fb, u.lastSashes, u.lastShell)
		for _, g := range layouts {
			if g.pane != nil && g.titleH > 0 {
				rows = append(rows, rowSpan{int(g.titleY), int(g.titleY + g.titleH)})
			}
		}
	}
	return rows, true
}

// progUnder is one progress bar from the last full frame, with the pixels
// it was drawn over.
type progUnder struct {
	pane      *tab
	x, y, w   int
	kind, pct int
	under     savedRect
}

// paintProgressBars draws each visible pane's OSC 9;4 bar on top of the
// frame, saving what is under it so paintProgressFB can slide it alone.
func (u *macUI) paintProgressBars(layouts []paneGeom, padY int, now time.Time) {
	bar := func(t *tab, x, y, w int) {
		if t == nil || t.modes.progress.kind == 0 || w < 2 {
			return
		}
		k := t.modes.progress
		u.progUnders = append(u.progUnders, progUnder{
			pane: t, x: x, y: y, w: w, kind: k.kind, pct: k.pct,
			under: saveRect(u.fb, image.Rect(x, y, x+w, y+progressBarH)),
		})
		u.painter.paintProgress(u.fb, x, y, w, k.kind, k.pct, now)
	}
	if len(layouts) > 1 {
		for _, g := range layouts {
			bar(g.pane, int(g.x), int(g.y), int(g.w))
		}
		return
	}
	if len(layouts) == 1 {
		// Single pane: paintFrame's shell geometry (padX 4, 8px scroll gutter).
		bar(layouts[0].pane, 4, padY, u.fb.Bounds().Dx()-8-4)
	}
}

// paintProgressFB slides the indeterminate bars over their saved pixels.
func (u *macUI) paintProgressFB(now time.Time) ([]rowSpan, bool) {
	for _, t := range u.visiblePanes() {
		if t != nil && t.modes.progress.kind == 3 && !u.hasProgUnder(t) {
			return nil, false
		}
	}
	var rows []rowSpan
	for _, pu := range u.progUnders {
		k := pu.pane.modes.progress
		if k.kind != pu.kind || k.pct != pu.pct {
			return nil, false
		}
		if k.kind != 3 {
			continue
		}
		pu.under.restore(u.fb)
		u.painter.paintProgress(u.fb, pu.x, pu.y, pu.w, k.kind, k.pct, now)
		rows = append(rows, rowSpan{pu.y, pu.y + progressBarH})
	}
	return rows, true
}

func (u *macUI) hasProgUnder(t *tab) bool {
	for _, pu := range u.progUnders {
		if pu.pane == t {
			return true
		}
	}
	return false
}

// keepCursorUnder drops the saved shell caret cell when something painted
// over it after the grid; restoring it would erase that layer.
func (u *macUI) keepCursorUnder(layouts []paneGeom, focus *tab) {
	cu := &u.painter.curUnder
	if !cu.ok {
		return
	}
	drop := u.chrome.OverlayOpen() || u.modalImage != nil
	if focus != nil && focus.kittyGfx != nil && len(focus.kittyGfx.snapshotPlacements()) > 0 {
		drop = true
	}
	r := cu.under.r
	for _, pu := range u.progUnders {
		if pu.under.r.Overlaps(r) {
			drop = true
		}
	}
	if z := u.zoomBadge; z.w > 0 && image.Rect(int(z.x), int(z.y), int(z.x+z.w), int(z.y+z.h)).Overlaps(r) {
		drop = true
	}
	if len(layouts) > 1 && focus != nil && focus.sb != nil && !focus.altScreen() {
		for _, g := range layouts {
			if g.pane == focus && len(focus.sb.visibleImages(focus.term, g.rows)) > 0 {
				drop = true
			}
		}
	}
	if drop {
		*cu = cursorUnder{}
	}
}

// paintCaretFB steps the caret pulse: Warp bars, and the focused pane's
// shell cursor cell when it blinks.
func (u *macUI) paintCaretFB(tab *tab, now time.Time) ([]rowSpan, bool) {
	var rows []rowSpan
	if u.barCaretShown() {
		w, h := u.fb.Bounds().Dx(), u.fb.Bounds().Dy()
		sticky := u.inputOnlyDirty
		u.inputOnlyDirty = true
		r, ok := u.paintInputOnlyFB(tab, w, h)
		u.inputOnlyDirty = sticky
		if !ok {
			return nil, false
		}
		rows = append(rows, r...)
	}
	if u.shellCaretShown() {
		cu := u.painter.curUnder
		if !cu.ok {
			return nil, false
		}
		cu.under.restore(u.fb)
		u.painter.paintCursorCell(u.fb, cu.px, cu.py, cu.cell, cu.style, u.caretAlphaAt(now.Sub(u.blinkStart)), false, cu.glass)
		rows = append(rows, rowSpan{cu.under.r.Min.Y, cu.under.r.Max.Y})
	}
	return rows, true
}

// savedRect is a copy of framebuffer pixels under a layer that repaints alone.
type savedRect struct {
	r   image.Rectangle
	pix []byte
}

func saveRect(src *image.RGBA, r image.Rectangle) savedRect {
	r = r.Intersect(src.Bounds())
	s := savedRect{r: r, pix: make([]byte, r.Dx()*r.Dy()*4)}
	n := r.Dx() * 4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		i := src.PixOffset(r.Min.X, y)
		copy(s.pix[(y-r.Min.Y)*n:], src.Pix[i:i+n])
	}
	return s
}

func (s savedRect) restore(dst *image.RGBA) {
	if !s.r.In(dst.Bounds()) {
		return
	}
	n := s.r.Dx() * 4
	for y := s.r.Min.Y; y < s.r.Max.Y; y++ {
		i := dst.PixOffset(s.r.Min.X, y)
		copy(dst.Pix[i:i+n], s.pix[(y-s.r.Min.Y)*n:])
	}
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
