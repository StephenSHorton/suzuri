//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/internal/iconstroke"

// captionInk is one coverage-weighted pixel of a caption icon.
type captionInk struct {
	x, y int32
	a    byte
}

func captionButtonWidth(dpi int32) int32 {
	if dpi < 96 {
		dpi = 96
	}
	w := int32(captionButtonDIP) * dpi / 96
	if w < 32 {
		w = 32
	}
	return w
}

func winCaptionButtonsDPI(clientW, stripH, dpi int32) [3]pixRect {
	var out [3]pixRect
	if clientW < 1 || stripH < 1 {
		return out
	}
	bw := captionButtonWidth(dpi)
	if bw*3 > clientW {
		bw = clientW / 3
		if bw < 1 {
			bw = 1
		}
	}
	right := clientW
	for i := 2; i >= 0; i-- {
		left := right - bw
		if left < 0 {
			left = 0
		}
		out[i] = pixRect{L: left, T: 0, R: right, B: stripH}
		right = left
	}
	return out
}

// captionIconSize is the glyph box inside a button. Scales with strip height.
func captionIconSize(btnW, btnH int32) int32 {
	s := btnH * 2 / 5
	if s < 10 {
		s = 10
	}
	if s > btnH-6 {
		s = btnH - 6
	}
	if btnW > 8 && s > btnW-8 {
		s = btnW - 8
	}
	if s < 6 {
		s = 6
	}
	return s
}

// captionStrokePx is the X's weight: at least 2px, ~1/5 of the icon.
// Minimize and maximize use this same stroke — do not thin the X.
func captionStrokePx(iconS int32) int32 {
	t := (iconS + 2) / 5
	if t < 2 {
		t = 2
	}
	return t
}

func captionGlyphInk(kind int, zoomed bool, cx, cy, s int32) []captionInk {
	k := iconstroke.Close
	switch kind {
	case 0:
		k = iconstroke.Min
	case 1:
		k = iconstroke.Max
		if zoomed {
			k = iconstroke.Restore
		}
	}
	return inkFrom(iconstroke.Raster(k, int(cx), int(cy), int(s), int(captionStrokePx(s))))
}

func stripIconInk(kind iconstroke.Kind, cx, cy, s int32) []captionInk {
	return inkFrom(iconstroke.Raster(kind, int(cx), int(cy), int(s), int(captionStrokePx(s))))
}

func inkFrom(src []iconstroke.Ink) []captionInk {
	out := make([]captionInk, len(src))
	for i, p := range src {
		out[i] = captionInk{x: int32(p.X), y: int32(p.Y), a: p.A}
	}
	return out
}

func mixCover(bg, fg, a byte) byte {
	return byte((int(bg)*(255-int(a)) + int(fg)*int(a)) / 255)
}
