//go:build windows

package ui

import (
	"image"
	"sync"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

var (
	modGdi32Img    = windows.NewLazySystemDLL("gdi32.dll")
	procSetDIBits  = modGdi32Img.NewProc("SetDIBits")
	procCreateDIB  = modGdi32Img.NewProc("CreateDIBSection")
	procStretchBlt = modGdi32Img.NewProc("StretchBlt")
	procCreateComp = modGdi32Img.NewProc("CreateCompatibleDC")
	procDeleteDC   = modGdi32Img.NewProc("DeleteDC")
	procSelectObj  = modGdi32Img.NewProc("SelectObject")
	procDeleteObj  = modGdi32Img.NewProc("DeleteObject")
)

const (
	biRGB        = 0
	dibRGBColors = 0
	srcCopy      = 0x00CC0020
	halftone     = 4
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type imgBitmap struct {
	hbm win.HBITMAP
	w   int
	h   int
}

var imgBitmaps = struct {
	m map[*tabImage]*imgBitmap
}{m: map[*tabImage]*imgBitmap{}}

func (im *tabImage) ensureBitmap(hdc win.HDC) *imgBitmap {
	if im == nil || im.img == nil {
		return nil
	}
	if b, ok := imgBitmaps.m[im]; ok && b != nil && b.hbm != 0 {
		return b
	}
	b := rgbaToHBITMAP(hdc, im.img)
	if b == nil {
		return nil
	}
	imgBitmaps.m[im] = b
	im.ready = true
	return b
}

func rgbaToHBITMAP(hdc win.HDC, src image.Image) *imgBitmap {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 1 || h < 1 {
		return nil
	}
	rowBytes := (w*4 + 3) & ^3
	pixels := make([]byte, rowBytes*h)
	for y := 0; y < h; y++ {
		dy := h - 1 - y
		off := dy * rowBytes
		for x := 0; x < w; x++ {
			r, g, b8, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := off + x*4
			pixels[i+0] = byte(b8 >> 8)
			pixels[i+1] = byte(g >> 8)
			pixels[i+2] = byte(r >> 8)
			pixels[i+3] = byte(a >> 8)
		}
	}
	var hdr bitmapInfoHeader
	hdr.Size = uint32(unsafe.Sizeof(hdr))
	hdr.Width = int32(w)
	hdr.Height = int32(h)
	hdr.Planes = 1
	hdr.BitCount = 32
	hdr.Compression = biRGB

	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIB.Call(
		uintptr(hdc),
		uintptr(unsafe.Pointer(&hdr)),
		uintptr(dibRGBColors),
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbm == 0 {
		return nil
	}
	if bits != nil {
		dst := unsafe.Slice((*byte)(bits), len(pixels))
		copy(dst, pixels)
	} else {
		procSetDIBits.Call(
			uintptr(hdc),
			hbm,
			0,
			uintptr(h),
			uintptr(unsafe.Pointer(&pixels[0])),
			uintptr(unsafe.Pointer(&hdr)),
			uintptr(dibRGBColors),
		)
	}
	return &imgBitmap{hbm: win.HBITMAP(hbm), w: w, h: h}
}

// paintTabImages draws scrollback-attached images for the focused pane.
// Alt-screen (Grok) never gets host overlays — use the image modal instead.
func (u *winUI) paintTabImages(hdc win.HDC, rect win.RECT) {
	t := u.activeTab()
	if t == nil || t.altScreen() {
		return
	}
	g := paneGeom{pane: t, y: u.shellPadY(), x: 8, w: rect.Right - 16, h: u.shellBottomY(rect.Bottom-rect.Top) - u.shellPadY(), rows: u.rows}
	if pg := u.paneGeomFor(t.id); pg != nil {
		g = *pg
	}
	u.paintPaneImages(hdc, rect, g)
}

// kittyHBMCache holds GDI bitmaps for Kitty graphics images (tabID, imageID).
// Guarded: clear can run on alt-screen exit while a paint is mid-flight if we
// ever move work off the UI thread; keep mutex cheap for UI-thread use too.
var kittyHBMCache = struct {
	mu sync.Mutex
	m  map[kittyHBMKey]*imgBitmap
}{m: map[kittyHBMKey]*imgBitmap{}}

type kittyHBMKey struct {
	tabID int
	imgID uint32
}

func clearKittyHBMCache(tabID int) {
	kittyHBMCache.mu.Lock()
	defer kittyHBMCache.mu.Unlock()
	for k, b := range kittyHBMCache.m {
		if k.tabID != tabID {
			continue
		}
		if b != nil && b.hbm != 0 {
			procDeleteObj.Call(uintptr(b.hbm))
		}
		delete(kittyHBMCache.m, k)
	}
}

func kittyEnsureBitmap(hdc win.HDC, tabID int, imgID uint32, src image.Image) *imgBitmap {
	if src == nil {
		return nil
	}
	k := kittyHBMKey{tabID: tabID, imgID: imgID}
	kittyHBMCache.mu.Lock()
	if b, ok := kittyHBMCache.m[k]; ok && b != nil && b.hbm != 0 {
		kittyHBMCache.mu.Unlock()
		return b
	}
	kittyHBMCache.mu.Unlock()

	// Build outside the lock — CreateDIBSection can be expensive on large PNGs.
	b := rgbaToHBITMAP(hdc, src)
	if b == nil {
		return nil
	}
	kittyHBMCache.mu.Lock()
	// Drop previous for this key if any (race: another paint won).
	if old, ok := kittyHBMCache.m[k]; ok && old != nil && old.hbm != 0 {
		procDeleteObj.Call(uintptr(old.hbm))
	}
	kittyHBMCache.m[k] = b
	kittyHBMCache.mu.Unlock()
	return b
}

// paintKittyPlacements draws Grok/Kitty graphics protocol images over the
// shell cell grid (prompt previews, inline media). Cell coords are 0-based.
// Painted on alt-screen too (unlike scrollback path images).
func (u *winUI) paintKittyPlacements(hdc win.HDC, rect win.RECT, t *tab, padY, shellBot int32) {
	if hdc == 0 || t == nil || t.kittyGfx == nil {
		return
	}
	places := t.kittyGfx.snapshotPlacements()
	if len(places) == 0 {
		return
	}
	cw, ch := int(u.metricW), int(u.metricH)
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX = 4
	// Stable paint order: lower z first so higher z draws on top.
	ordered := append([]kittyPlace(nil), places...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[j].z < ordered[i].z {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	memDC, _, _ := procCreateComp.Call(uintptr(hdc))
	if memDC == 0 {
		return
	}
	defer procDeleteDC.Call(memDC)

	clipL := rect.Left
	clipR := rect.Right
	clipT := padY
	if clipT < rect.Top {
		clipT = rect.Top
	}
	clipB := shellBot
	if clipB > rect.Bottom {
		clipB = rect.Bottom
	}

	for _, pl := range ordered {
		img := t.kittyGfx.image(pl.id)
		if img == nil {
			continue
		}
		sb := img.Bounds()
		// Optional source crop (Kitty x,y,w,h in pixels).
		srcX, srcY, srcW, srcH := 0, 0, sb.Dx(), sb.Dy()
		if pl.srcW > 0 && pl.srcH > 0 {
			r := image.Rect(pl.srcX, pl.srcY, pl.srcX+pl.srcW, pl.srcY+pl.srcH).Intersect(sb)
			if r.Empty() {
				continue
			}
			srcX, srcY = r.Min.X-sb.Min.X, r.Min.Y-sb.Min.Y
			srcW, srcH = r.Dx(), r.Dy()
		}
		if srcW < 1 || srcH < 1 {
			continue
		}
		// Build HBITMAP from full image; StretchBlt crops via source rect.
		bm := kittyEnsureBitmap(hdc, t.id, pl.id, img)
		if bm == nil {
			continue
		}
		px := int32(padX) + int32(pl.col*cw)
		py := padY + int32(pl.row*ch)
		boxW := pl.cols * cw
		boxH := pl.rows * ch
		if boxW < 4 || boxH < 4 {
			continue
		}
		dw, dh := fitPreferNative(srcW, srcH, boxW, boxH)
		if dw < 1 || dh < 1 {
			continue
		}
		// Center in placement box.
		ox := px + int32((boxW-dw)/2)
		oy := py + int32((boxH-dh)/2)
		// Clip to shell region.
		dstL, dstT := ox, oy
		dstR, dstB := ox+int32(dw), oy+int32(dh)
		if dstL < clipL {
			dstL = clipL
		}
		if dstT < clipT {
			dstT = clipT
		}
		if dstR > clipR {
			dstR = clipR
		}
		if dstB > clipB {
			dstB = clipB
		}
		if dstR <= dstL || dstB <= dstT {
			continue
		}
		// Adjust source when destination is clipped (uniform scale approximation).
		drawW := int(dstR - dstL)
		drawH := int(dstB - dstT)
		old, _, _ := procSelectObj.Call(memDC, uintptr(bm.hbm))
		win.SetStretchBltMode(hdc, halftone)
		// Map crop into full bitmap space (image origin at 0,0 in HBITMAP).
		sx, sy := srcX, srcY
		if pl.srcW > 0 && pl.srcH > 0 {
			sx, sy = pl.srcX, pl.srcY
			if sx < 0 {
				sx = 0
			}
			if sy < 0 {
				sy = 0
			}
		}
		procStretchBlt.Call(
			uintptr(hdc),
			uintptr(dstL),
			uintptr(dstT),
			uintptr(drawW),
			uintptr(drawH),
			memDC,
			uintptr(sx),
			uintptr(sy),
			uintptr(srcW),
			uintptr(srcH),
			uintptr(srcCopy),
		)
		procSelectObj.Call(memDC, old)
	}
}

// paintPaneImages draws scrollback images clipped to a pane layout rect.
func (u *winUI) paintPaneImages(hdc win.HDC, rect win.RECT, g paneGeom) {
	if hdc == 0 || g.pane == nil || g.pane.altScreen() {
		return
	}
	t := g.pane
	viewRows := g.rows
	if viewRows < 1 {
		viewRows = u.rows
	}
	vis := t.sb.visibleImages(t.term, viewRows)
	if len(vis) == 0 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	padY := g.y
	shellBot := g.y + g.h
	padX := g.x + 4
	availW := int(g.w) - 8
	if availW < 40 {
		return
	}

	memDC, _, _ := procCreateComp.Call(uintptr(hdc))
	if memDC == 0 {
		return
	}
	defer procDeleteDC.Call(memDC)

	for _, v := range vis {
		if v.img == nil {
			continue
		}
		bm := v.img.ensureBitmap(hdc)
		if bm == nil {
			continue
		}
		yTop := padY + int32(v.viewRow)*ch
		bodyTop := yTop
		bodyRows := v.span
		if v.span > 1 {
			bodyTop = yTop + ch
			bodyRows = v.span - 1
		}
		yBot := bodyTop + int32(bodyRows)*ch
		if yBot > shellBot {
			yBot = shellBot
		}
		if bodyTop >= shellBot || yBot <= padY {
			continue
		}
		boxH := int(yBot - bodyTop)
		if boxH < 8 {
			continue
		}
		dw, dh := fitPreferNative(bm.w, bm.h, availW, boxH)
		if dw < 1 || dh < 1 {
			continue
		}
		drawTop := bodyTop
		if drawTop < padY {
			if bodyTop+int32(dh) < padY {
				continue
			}
			drawTop = padY
		}
		old, _, _ := procSelectObj.Call(memDC, uintptr(bm.hbm))
		win.SetStretchBltMode(hdc, halftone)
		procStretchBlt.Call(
			uintptr(hdc),
			uintptr(padX),
			uintptr(drawTop),
			uintptr(dw),
			uintptr(dh),
			memDC,
			0,
			0,
			uintptr(bm.w),
			uintptr(bm.h),
			uintptr(srcCopy),
		)
		procSelectObj.Call(memDC, old)
		if br := createSolidBrushRGB(win.RGB(60, 60, 70)); br != 0 {
			frameBorder(hdc, padX-1, drawTop-1, padX+int32(dw)+1, drawTop+int32(dh)+1, br)
			win.DeleteObject(win.HGDIOBJ(br))
		}
	}
}

// paintImageModal draws a dim full-window lightbox for modalImage.
func (u *winUI) paintImageModal(hdc win.HDC, rect win.RECT) {
	if hdc == 0 || u.modalImage == nil {
		return
	}
	im := u.modalImage
	// Dim backdrop
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(0, 0, 0)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		// ~75% black via solid for simplicity (full black then image pops)
		fillRect(hdc, rect, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
	// Soft center panel
	margin := int32(24)
	panel := win.RECT{
		Left:   rect.Left + margin,
		Top:    rect.Top + margin,
		Right:  rect.Right - margin,
		Bottom: rect.Bottom - margin,
	}
	if panel.Right <= panel.Left || panel.Bottom <= panel.Top {
		return
	}
	bm := im.ensureBitmap(hdc)
	if bm == nil {
		return
	}
	maxW := int(panel.Right - panel.Left)
	maxH := int(panel.Bottom - panel.Top) - 28 // room for hint
	dw, dh := fitPreferNative(bm.w, bm.h, maxW, maxH)
	if dw < 1 || dh < 1 {
		return
	}
	x := panel.Left + (int32(maxW)-int32(dw))/2
	y := panel.Top + (int32(maxH)-int32(dh))/2
	memDC, _, _ := procCreateComp.Call(uintptr(hdc))
	if memDC == 0 {
		return
	}
	defer procDeleteDC.Call(memDC)
	old, _, _ := procSelectObj.Call(memDC, uintptr(bm.hbm))
	win.SetStretchBltMode(hdc, halftone)
	procStretchBlt.Call(
		uintptr(hdc),
		uintptr(x),
		uintptr(y),
		uintptr(dw),
		uintptr(dh),
		memDC,
		0,
		0,
		uintptr(bm.w),
		uintptr(bm.h),
		uintptr(srcCopy),
	)
	procSelectObj.Call(memDC, old)
	if br := createSolidBrushRGB(win.RGB(chrome.PrimR, chrome.PrimG, chrome.PrimB)); br != 0 {
		frameBorder(hdc, x-1, y-1, x+int32(dw)+1, y+int32(dh)+1, br)
		win.DeleteObject(win.HGDIOBJ(br))
	}
	// Hint
	if u.font != 0 {
		oldF := win.SelectObject(hdc, win.HGDIOBJ(u.font))
		win.SetBkMode(hdc, win.TRANSPARENT)
		win.SetTextColor(hdc, win.RGB(chrome.SoftR, chrome.SoftG, chrome.SoftB))
		hint, err := syscall.UTF16FromString("Esc or click to close")
		if err == nil && len(hint) >= 2 {
			win.TextOut(hdc, panel.Left+8, panel.Bottom-22, &hint[0], int32(len(hint)-1))
		}
		win.SelectObject(hdc, oldF)
	}
}

// tryOpenImageModalAt opens the image lightbox if the click is on a shell
// image block or near Grok's path / "[Open Image]" line.
func (u *winUI) tryOpenImageModalAt(px, py int32) bool {
	t := u.activeTab()
	if t == nil {
		return false
	}
	_, y, viewRows := u.pixelToCellInPane(px, py, t)
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
		// Fallback: nearest path on screen if user clicked roughly in the pane.
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

func createSolidBrushRGB(c win.COLORREF) win.HBRUSH {
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: c}
	return win.CreateBrushIndirect(&lb)
}

func frameBorder(hdc win.HDC, l, t, r, b int32, br win.HBRUSH) {
	fillRect(hdc, win.RECT{Left: l, Top: t, Right: r, Bottom: t + 1}, br)
	fillRect(hdc, win.RECT{Left: l, Top: b - 1, Right: r, Bottom: b}, br)
	fillRect(hdc, win.RECT{Left: l, Top: t, Right: l + 1, Bottom: b}, br)
	fillRect(hdc, win.RECT{Left: r - 1, Top: t, Right: r, Bottom: b}, br)
}

