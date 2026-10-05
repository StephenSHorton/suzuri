//go:build windows || darwin

package ui

import "testing"

func TestCaptionIconSizeOpticalBump(t *testing.T) {
	// 27px strip at 96 DPI used to be 10px (2/5). Want ~20–30% larger.
	old := int32(27 * 2 / 5)
	got := captionIconSize(46, 27)
	if got < old*6/5 || got > old*7/5 {
		t.Fatalf("icon %d vs old %d — want 20–30%% larger", got, old)
	}
	if got < 11 {
		t.Fatal("100% DPI floor")
	}
	bell := captionIconSize(40, 27)
	closeS := captionIconSize(46, 27)
	if bell != closeS {
		t.Fatalf("bell/close must share the strip size (bell=%d close=%d)", bell, closeS)
	}
}

func TestCaptionStrokeMatchesX(t *testing.T) {
	for _, s := range []int32{10, 12, 16, 20, 24} {
		st := captionStrokePx(s)
		if st < 2 {
			t.Fatalf("icon %d stroke %d thins the X", s, st)
		}
		for _, kind := range []int{0, 1, 2} {
			ink := captionGlyphInk(kind, false, 40, 40, s)
			if !inkHasCoreAndFringe(ink) {
				t.Fatalf("kind %d icon %d missing core or AA fringe (%d px)", kind, s, len(ink))
			}
		}
	}
}

func TestCaptionGlyphsHaveNoShadowCopy(t *testing.T) {
	ink := captionGlyphInk(2, false, 50, 50, 16)
	// Old crossRects stamped a second diagonal 1px along the stroke (t/2).
	// A shadow copy would be the same shape translated by (1,1).
	if inkShiftedOverlap(ink, 1, 1) > 85 {
		t.Fatalf("close looks like an offset shadow copy (%d%% overlap)", inkShiftedOverlap(ink, 1, 1))
	}
	min := captionGlyphInk(0, false, 50, 50, 16)
	if inkShiftedOverlap(min, 1, 1) > 85 {
		t.Fatalf("min looks like an offset shadow copy (%d%% overlap)", inkShiftedOverlap(min, 1, 1))
	}
}

func TestFrameChromeStyleKeepsSnapChrome(t *testing.T) {
	const overlapped = styleCaption | styleSysmenu | styleThickframe | styleMinimizeBox | styleMaximizeBox
	got := frameChromeStyle(overlapped)
	if got&styleCaptionBtns != 0 {
		t.Fatalf("DWM button bits %#x", got)
	}
	if got&styleCaption == 0 || got&styleThickframe == 0 {
		t.Fatal("shadow/resize need caption+thickframe")
	}
}

func TestPresentCaptionHitKeepsSnapMax(t *testing.T) {
	if presentCaptionHit(hitMaxButton) != hitMaxButton {
		t.Fatal("maximize must stay HTMAXBUTTON for Snap Layouts")
	}
	if presentCaptionHit(hitMinButton) != hitClient {
		t.Fatal("min must be HTCLIENT so DWM does not own the button rect")
	}
	if presentCaptionHit(hitClose) != hitClient {
		t.Fatal("close must be HTCLIENT so DWM does not own the button rect")
	}
	if presentCaptionHit(hitCaption) != hitCaption {
		t.Fatal("drag strip")
	}
	if presentCaptionHit(hitTop) != hitTop {
		t.Fatal("resize")
	}
}

func TestPackCaptionInkBGR24(t *testing.T) {
	ink := []captionInk{{x: 2, y: 1, a: 255}, {x: 3, y: 1, a: 0}}
	pix := packCaptionInkBGR24(ink, 2, 1, 2, 1, 10, 20, 30, 1, 2, 3)
	if len(pix) < 6 {
		t.Fatalf("len %d", len(pix))
	}
	if pix[0] != 30 || pix[1] != 20 || pix[2] != 10 {
		t.Fatalf("solid fg BGR %d %d %d", pix[0], pix[1], pix[2])
	}
	if pix[3] != 3 || pix[4] != 2 || pix[5] != 1 {
		t.Fatalf("bg BGR %d %d %d", pix[3], pix[4], pix[5])
	}
}

func TestInkBoundsTightOnCoverage(t *testing.T) {
	if _, _, _, _, ok := inkBounds(nil); ok {
		t.Fatal("empty ink")
	}
	if _, _, _, _, ok := inkBounds([]captionInk{{x: 3, y: 4, a: 0}}); ok {
		t.Fatal("zero-alpha only")
	}
	ink := []captionInk{
		{x: 10, y: 20, a: 0},
		{x: 12, y: 18, a: 255},
		{x: 15, y: 22, a: 80},
		{x: 11, y: 19, a: 1},
	}
	minX, minY, maxX, maxY, ok := inkBounds(ink)
	if !ok || minX != 11 || minY != 18 || maxX != 15 || maxY != 22 {
		t.Fatalf("bounds %d,%d %d,%d ok=%v", minX, minY, maxX, maxY, ok)
	}
}

func TestCaptionButtonWidthScalesWithDPI(t *testing.T) {
	if captionButtonWidth(96) != captionButtonDIP {
		t.Fatalf("96dpi width %d want %d", captionButtonWidth(96), captionButtonDIP)
	}
	if captionButtonWidth(144) != captionButtonDIP*144/96 {
		t.Fatalf("150%% width %d", captionButtonWidth(144))
	}
	if captionButtonWidth(192) != captionButtonDIP*2 {
		t.Fatalf("200%% width %d", captionButtonWidth(192))
	}
	btns := winCaptionButtonsDPI(800, 40, 192)
	if btns[2].R-btns[2].L != captionButtonWidth(192) {
		t.Fatalf("close width %d", btns[2].R-btns[2].L)
	}
	if winCaptionButtons(800, 27)[0].R-winCaptionButtons(800, 27)[0].L != captionButtonDIP {
		t.Fatal("96dpi helper must stay 46px for hit tests")
	}
}

func inkHasCoreAndFringe(ink []captionInk) bool {
	if len(ink) < 8 {
		return false
	}
	core, fringe := 0, 0
	for _, p := range ink {
		if p.a >= 200 {
			core++
		}
		if p.a > 0 && p.a < 180 {
			fringe++
		}
	}
	return core >= 4 && fringe >= 4
}

func inkShiftedOverlap(ink []captionInk, dx, dy int32) int {
	type xy struct{ x, y int32 }
	a := make(map[xy]struct{}, len(ink))
	b := make(map[xy]struct{}, len(ink))
	for _, p := range ink {
		if p.a < 80 {
			continue
		}
		a[xy{p.x, p.y}] = struct{}{}
		b[xy{p.x + dx, p.y + dy}] = struct{}{}
	}
	if len(a) == 0 {
		return 0
	}
	hit := 0
	for k := range a {
		if _, ok := b[k]; ok {
			hit++
		}
	}
	return hit * 100 / len(a)
}
