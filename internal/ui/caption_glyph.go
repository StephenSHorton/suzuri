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

// inkBounds is the inclusive pixel rect of non-zero coverage. Used to blit
// one DIB instead of SetPixel-per-dot (each SetPixel is a GDI syscall).
func inkBounds(ink []captionInk) (minX, minY, maxX, maxY int32, ok bool) {
	ok = false
	for _, p := range ink {
		if p.a == 0 {
			continue
		}
		if !ok {
			minX, minY, maxX, maxY = p.x, p.y, p.x, p.y
			ok = true
			continue
		}
		if p.x < minX {
			minX = p.x
		}
		if p.y < minY {
			minY = p.y
		}
		if p.x > maxX {
			maxX = p.x
		}
		if p.y > maxY {
			maxY = p.y
		}
	}
	return minX, minY, maxX, maxY, ok
}

func mixCover(bg, fg, a byte) byte {
	return byte((int(bg)*(255-int(a)) + int(fg)*int(a)) / 255)
}

// packCaptionInkBGR24 is a top-down 24-bit DIB (3 bytes/pixel, DWORD padded).
// Caption stamps must stay 24-bit: a 32-bit blit onto the window backbuffer
// realizes dest alpha and turns glass holes into opaque black.
func packCaptionInkBGR24(ink []captionInk, minX, minY, w, h int32, cr, cg, cb, br, bg, bb byte) []byte {
	if w < 1 || h < 1 {
		return nil
	}
	rowBytes := (int(w)*3 + 3) & ^3
	pix := make([]byte, rowBytes*int(h))
	for y := 0; y < int(h); y++ {
		off := y * rowBytes
		for x := 0; x < int(w); x++ {
			pix[off+x*3+0] = bb
			pix[off+x*3+1] = bg
			pix[off+x*3+2] = br
		}
	}
	for _, p := range ink {
		if p.a == 0 {
			continue
		}
		x := p.x - minX
		y := p.y - minY
		if x < 0 || y < 0 || x >= w || y >= h {
			continue
		}
		off := int(y)*rowBytes + int(x)*3
		pix[off+0] = mixCover(bb, cb, p.a)
		pix[off+1] = mixCover(bg, cg, p.a)
		pix[off+2] = mixCover(br, cr, p.a)
	}
	return pix
}
