//go:build windows || darwin

package ui

import "math"

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
	stroke := captionStrokePx(s)
	half := float64(s) / 2
	fx, fy := float64(cx), float64(cy)
	switch kind {
	case 0:
		return strokeSeg(fx-half, fy, fx+half, fy, stroke)
	case 1:
		if zoomed {
			off := half / 2
			back := strokeRect(fx-half+off, fy-half, fx+half, fy+half-off, stroke)
			front := strokeRect(fx-half, fy-half+off, fx+half-off, fy+half, stroke)
			return mergeInk(back, front)
		}
		return strokeRect(fx-half, fy-half, fx+half, fy+half, stroke)
	default:
		return mergeInk(
			strokeSeg(fx-half, fy-half, fx+half, fy+half, stroke),
			strokeSeg(fx+half, fy-half, fx-half, fy+half, stroke),
		)
	}
}

func strokeRect(l, t, r, b float64, stroke int32) []captionInk {
	return mergeInk(
		strokeSeg(l, t, r, t, stroke),
		strokeSeg(r, t, r, b, stroke),
		strokeSeg(r, b, l, b, stroke),
		strokeSeg(l, b, l, t, stroke),
	)
}

func strokeSeg(x0, y0, x1, y1 float64, stroke int32) []captionInk {
	if stroke < 1 {
		stroke = 1
	}
	half := float64(stroke) / 2
	const fringe = 0.75
	pad := half + fringe + 1
	minX := int32(math.Floor(math.Min(x0, x1) - pad))
	maxX := int32(math.Ceil(math.Max(x0, x1) + pad))
	minY := int32(math.Floor(math.Min(y0, y1) - pad))
	maxY := int32(math.Ceil(math.Max(y0, y1) + pad))
	out := make([]captionInk, 0, (maxX-minX+1)*(maxY-minY+1)/2)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			d := distToSeg(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1)
			cov := half + fringe - d
			if cov <= 0 {
				continue
			}
			if cov > 1 {
				cov = 1
			}
			out = append(out, captionInk{x: x, y: y, a: byte(cov * 255)})
		}
	}
	return out
}

func distToSeg(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	if l2 < 1e-9 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*dx + (py-y0)*dy) / l2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}

func mergeInk(parts ...[]captionInk) []captionInk {
	seen := make(map[uint64]byte, 64)
	for _, part := range parts {
		for _, p := range part {
			if p.a == 0 {
				continue
			}
			k := uint64(uint32(p.x))<<32 | uint64(uint32(p.y))
			if p.a > seen[k] {
				seen[k] = p.a
			}
		}
	}
	out := make([]captionInk, 0, len(seen))
	for k, a := range seen {
		out = append(out, captionInk{x: int32(k >> 32), y: int32(k), a: a})
	}
	return out
}

func mixCover(bg, fg, a byte) byte {
	return byte((int(bg)*(255-int(a)) + int(fg)*int(a)) / 255)
}
