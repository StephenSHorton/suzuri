//go:build darwin

package ui

import (
	"image"
	"testing"
)

// stacked │ cells must share ink on the shared edge (no gap between rows).
func TestVerticalBarSeamlessAcrossRows(t *testing.T) {
	p := &softwarePainter{cellW: 10, cellH: 16, ascent: 12}
	dst := image.NewRGBA(image.Rect(0, 0, 10, 32))
	// Two stacked box-drawing verticals.
	p.drawGlyph(dst, 0, 0, '│', 255, 255, 255)
	p.drawGlyph(dst, 0, 16, '│', 255, 255, 255)

	cx := 5 // cell mid for width 10
	// Bottom pixel of first cell and top pixel of second must both be inked.
	if !pixelIs(dst, cx, 15, 255, 255, 255) {
		t.Fatalf("top cell │ missing ink at bottom edge (y=15)")
	}
	if !pixelIs(dst, cx, 16, 255, 255, 255) {
		t.Fatalf("bottom cell │ missing ink at top edge (y=16)")
	}
	// Center of both cells should be inked.
	if !pixelIs(dst, cx, 8, 255, 255, 255) {
		t.Fatalf("top cell │ missing center ink")
	}
	if !pixelIs(dst, cx, 24, 255, 255, 255) {
		t.Fatalf("bottom cell │ missing center ink")
	}
}

func TestHorizontalBarSeamlessAcrossCols(t *testing.T) {
	p := &softwarePainter{cellW: 10, cellH: 16, ascent: 12}
	dst := image.NewRGBA(image.Rect(0, 0, 20, 16))
	p.drawGlyph(dst, 0, 0, '─', 200, 200, 200)
	p.drawGlyph(dst, 10, 0, '─', 200, 200, 200)

	cy := 8
	if !pixelIs(dst, 9, cy, 200, 200, 200) {
		t.Fatalf("left ─ missing right edge")
	}
	if !pixelIs(dst, 10, cy, 200, 200, 200) {
		t.Fatalf("right ─ missing left edge")
	}
}

func TestASCIIPipeNotSoftwareDrawn(t *testing.T) {
	// ASCII | is not box-drawing; leave it to the font (no full-cell stroke).
	p := &softwarePainter{cellW: 10, cellH: 16, ascent: 12}
	dst := image.NewRGBA(image.Rect(0, 0, 10, 16))
	if p.drawCellGlyph(dst, 0, 0, '|', 255, 255, 255) {
		t.Fatal("ASCII | should not be software box-drawn")
	}
}

func pixelIs(dst *image.RGBA, x, y int, r, g, b byte) bool {
	if !image.Pt(x, y).In(dst.Bounds()) {
		return false
	}
	i := dst.PixOffset(x, y)
	return dst.Pix[i] == r && dst.Pix[i+1] == g && dst.Pix[i+2] == b && dst.Pix[i+3] == 255
}
