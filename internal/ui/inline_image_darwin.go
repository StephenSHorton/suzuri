//go:build darwin

package ui

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/charmbracelet/log"
	xdraw "golang.org/x/image/draw"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// tryOpenImageModalAt opens the image lightbox if the click is on a shell
// image block or near Grok's path / "[Open Image]" line (Windows parity).
func (u *macUI) tryOpenImageModalAt(px, py int) bool {
	t := u.activeTab()
	if t == nil {
		return false
	}
	_, y, viewRows := u.pixelToCellInPane(int32(px), int32(py), t)
	if y < 0 {
		y = 0
	}
	if viewRows < 1 {
		viewRows = u.rows
	}

	// Primary shell: click inside a visible image block.
	if !t.altScreen() {
		vis := t.sb.visibleImages(t.term, viewRows)
		for _, v := range vis {
			if v.img == nil {
				continue
			}
			if y >= v.viewRow && y < v.viewRow+v.span {
				u.modalImage = v.img
				log.Info("image modal open", "path", v.img.path, "src", "scrollback")
				return true
			}
		}
		return false
	}

	// Alt-screen (Grok): resolve path near the clicked row.
	hits := collectScreenImageHits(t.term)
	if len(hits) == 0 {
		return false
	}
	bestRef := ""
	bestDist := 1 << 30
	for _, h := range hits {
		d := h.row - y
		if d < 0 {
			d = -d
		}
		// Prefer hits on this row or within a few lines (path wrap / Open Image).
		if d <= 3 && d < bestDist {
			bestDist = d
			bestRef = h.ref
		}
	}
	if bestRef == "" {
		for _, h := range hits {
			d := h.row - y
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				bestRef = h.ref
			}
		}
	}
	if bestRef == "" || bestDist > 8 {
		return false
	}
	abs := resolveImagePath(t.cwd, bestRef)
	if abs == "" {
		log.Debug("image modal resolve failed", "ref", bestRef)
		return false
	}
	im, err := loadImageFile(abs)
	if err != nil {
		log.Debug("image modal load failed", "path", abs, "err", err)
		return false
	}
	u.modalImage = im
	log.Info("image modal open", "path", abs, "src", "alt-screen click")
	return true
}

// paintImageModal draws a dim full-window lightbox for modalImage into dst.
func (u *macUI) paintImageModal(dst *image.RGBA) {
	if u == nil || dst == nil || u.modalImage == nil || u.modalImage.img == nil {
		return
	}
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	if w < 40 || h < 40 {
		return
	}
	// Dim backdrop (~75% black over current frame).
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = dst.Pix[i+0] / 4
			dst.Pix[i+1] = dst.Pix[i+1] / 4
			dst.Pix[i+2] = dst.Pix[i+2] / 4
			dst.Pix[i+3] = 255
		}
	}
	const margin = 24
	maxW := w - 2*margin
	maxH := h - 2*margin - 28 // room for hint
	if maxW < 20 || maxH < 20 {
		return
	}
	src := u.modalImage.img
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw < 1 || sh < 1 {
		return
	}
	dw, dh := fitPreferNative(sw, sh, maxW, maxH)
	if dw < 1 || dh < 1 {
		return
	}
	x := margin + (maxW-dw)/2
	y := margin + (maxH-dh)/2
	// Scaled blit
	scaled := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, sb, draw.Over, nil)
	draw.Draw(dst, image.Rect(x, y, x+dw, y+dh), scaled, image.Point{}, draw.Over)
	// Primary border
	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB
	frameRGBA(dst, x-1, y-1, dw+2, dh+2, pr, pg, pb)
	// Hint
	if u.painter != nil {
		hint := "Esc or click to close"
		hx := margin + 8
		hy := h - margin - 18
		for _, r := range []rune(hint) {
			u.painter.drawGlyph(dst, hx, hy, r, chrome.SoftR, chrome.SoftG, chrome.SoftB)
			hx += u.painter.cellW
			if u.painter.cellW < 1 {
				hx += 8
			}
		}
	} else {
		_ = color.RGBA{}
	}
}

func frameRGBA(dst *image.RGBA, x, y, w, h int, r, g, b byte) {
	if w < 2 || h < 2 {
		return
	}
	fillRectRGBA(dst, x, y, w, 1, r, g, b)
	fillRectRGBA(dst, x, y+h-1, w, 1, r, g, b)
	fillRectRGBA(dst, x, y, 1, h, r, g, b)
	fillRectRGBA(dst, x+w-1, y, 1, h, r, g, b)
}
