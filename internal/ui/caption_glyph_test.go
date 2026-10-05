//go:build windows || darwin

package ui

import "testing"

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
