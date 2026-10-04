// Package iconstroke rasterizes the top-right strip icons from one Lucide-style
// 24×24 stroke set. Same stroke weight and coverage AA for every glyph.
package iconstroke

import (
	"image"
	"image/color"
	"math"
)

// Kind is one strip icon. Min/max/close/bell/coffee share this library.
type Kind int

const (
	Min Kind = iota
	Max
	Restore
	Close
	Bell
	Coffee
)

// Ink is one coverage-weighted pixel.
type Ink struct {
	X, Y int
	A    uint8
}

// StrokeForSize is the X's weight: at least 2px, about 1/5 of the box.
// Every icon uses this — do not thin the X.
func StrokeForSize(size int) int {
	if size < 6 {
		size = 6
	}
	t := (size + 2) / 5
	if t < 2 {
		t = 2
	}
	return t
}

// Raster draws kind centered at (cx,cy) in a size×size box.
func Raster(kind Kind, cx, cy, size, stroke int) []Ink {
	if size < 6 {
		size = 6
	}
	if stroke < 2 {
		stroke = StrokeForSize(size)
	}
	sc := float64(size) / 24
	mapX := func(x float64) float64 { return float64(cx) + (x-12)*sc }
	mapY := func(y float64) float64 { return float64(cy) + (y-12)*sc }
	st := int32(stroke)
	line := func(x0, y0, x1, y1 float64) []Ink {
		return strokeSeg(mapX(x0), mapY(y0), mapX(x1), mapY(y1), st)
	}
	rect := func(x, y, w, h float64) []Ink {
		return merge(
			line(x, y, x+w, y),
			line(x+w, y, x+w, y+h),
			line(x+w, y+h, x, y+h),
			line(x, y+h, x, y),
		)
	}
	arc := func(cx24, cy24, r, a0, a1 float64) []Ink {
		return strokeArc(mapX(cx24), mapY(cy24), r*sc, a0, a1, st)
	}
	switch kind {
	case Min:
		return line(5, 12, 19, 12)
	case Max:
		return rect(5, 5, 14, 14)
	case Restore:
		return merge(rect(8, 3, 13, 13), rect(3, 8, 13, 13))
	case Close:
		return merge(line(6, 6, 18, 18), line(18, 6, 6, 18))
	case Bell:
		// Lucide bell, lines + dome + clapper.
		return merge(
			arc(12, 9, 5.5, math.Pi, 0),
			line(6.5, 9, 5, 17),
			line(17.5, 9, 19, 17),
			line(5, 17, 19, 17),
			line(10.2, 19.5, 13.8, 19.5),
		)
	case Coffee:
		// Lucide coffee: steam, cup body, handle.
		return merge(
			line(8, 2, 8, 4.2),
			line(12, 2, 12, 4.2),
			line(16, 2, 16, 4.2),
			rect(4, 8, 13, 11),
			arc(17.5, 12, 3.2, -math.Pi/2, math.Pi/2),
		)
	default:
		return nil
	}
}

func strokeSeg(x0, y0, x1, y1 float64, stroke int32) []Ink {
	if stroke < 1 {
		stroke = 1
	}
	half := float64(stroke) / 2
	const fringe = 0.75
	pad := half + fringe + 1
	minX := int(math.Floor(math.Min(x0, x1) - pad))
	maxX := int(math.Ceil(math.Max(x0, x1) + pad))
	minY := int(math.Floor(math.Min(y0, y1) - pad))
	maxY := int(math.Ceil(math.Max(y0, y1) + pad))
	out := make([]Ink, 0, (maxX-minX+1)*(maxY-minY+1)/2)
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
			out = append(out, Ink{X: x, Y: y, A: uint8(cov * 255)})
		}
	}
	return out
}

func strokeArc(cx, cy, r, a0, a1 float64, stroke int32) []Ink {
	if r < 0.5 {
		return nil
	}
	if a1 < a0 {
		a1 += 2 * math.Pi
	}
	steps := int(math.Ceil(r * (a1 - a0) * 2))
	if steps < 8 {
		steps = 8
	}
	var parts [][]Ink
	prevX := cx + r*math.Cos(a0)
	prevY := cy + r*math.Sin(a0)
	for i := 1; i <= steps; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(steps)
		x := cx + r*math.Cos(a)
		y := cy + r*math.Sin(a)
		parts = append(parts, strokeSeg(prevX, prevY, x, y, stroke))
		prevX, prevY = x, y
	}
	return merge(parts...)
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

func merge(parts ...[]Ink) []Ink {
	seen := make(map[uint64]uint8, 64)
	for _, part := range parts {
		for _, p := range part {
			if p.A == 0 {
				continue
			}
			k := uint64(uint32(p.X))<<32 | uint64(uint32(p.Y))
			if p.A > seen[k] {
				seen[k] = p.A
			}
		}
	}
	out := make([]Ink, 0, len(seen))
	for k, a := range seen {
		out = append(out, Ink{X: int(int32(k >> 32)), Y: int(int32(k)), A: a})
	}
	return out
}

func mixCover(bg, fg, a uint8) uint8 {
	return uint8((int(bg)*(255-int(a)) + int(fg)*int(a)) / 255)
}

// Paint draws ink onto dst with fg over bg. Returns how many pixels were set.
func Paint(dst *image.RGBA, ink []Ink, fg, bg color.RGBA) int {
	if dst == nil {
		return 0
	}
	n := 0
	for _, p := range ink {
		if p.A == 0 {
			continue
		}
		pt := image.Pt(p.X, p.Y)
		if !pt.In(dst.Rect) {
			continue
		}
		dst.SetRGBA(p.X, p.Y, color.RGBA{
			R: mixCover(bg.R, fg.R, p.A),
			G: mixCover(bg.G, fg.G, p.A),
			B: mixCover(bg.B, fg.B, p.A),
			A: 255,
		})
		n++
	}
	return n
}
