package iconstroke

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"sync/atomic"
)

// Slot is one top-right control, left-to-right: bell, coffee, min, max, close.
type Slot int

const (
	SlotBell Slot = iota
	SlotCoffee
	SlotMin
	SlotMax
	SlotClose
	slotCount
)

// DrawCounts is how many times each slot was painted in the last RenderStrip.
var (
	drawGen    atomic.Uint64
	drawCounts [slotCount]atomic.Uint32
)

func lastDrawCounts() [slotCount]uint32 {
	var out [slotCount]uint32
	for i := range out {
		out[i] = drawCounts[i].Load()
	}
	return out
}

func resetDrawCounts() {
	for i := range drawCounts {
		drawCounts[i].Store(0)
	}
	drawGen.Add(1)
}

func noteDraw(s Slot) {
	if s >= 0 && s < slotCount {
		drawCounts[s].Add(1)
	}
}

// StripOpts lays out the trailing cluster the way the Win32 host does.
type StripOpts struct {
	DPI        int
	Zoomed     bool
	CaffeineOn bool
	BellUnread bool
	Width      int
	Height     int
}

func buttonWidth(dpi int) int {
	if dpi < 96 {
		dpi = 96
	}
	w := 46 * dpi / 96
	if w < 32 {
		w = 32
	}
	return w
}

// RenderStrip paints the five trailing icons once each onto a bar.
func RenderStrip(o StripOpts) *image.RGBA {
	if o.DPI < 96 {
		o.DPI = 96
	}
	if o.Height < 16 {
		o.Height = 27 * o.DPI / 96
	}
	if o.Width < 8 {
		o.Width = buttonWidth(o.DPI)*5 + 24*o.DPI/96
	}
	dst := image.NewRGBA(image.Rect(0, 0, o.Width, o.Height))
	bar := color.RGBA{R: 0x14, G: 0x14, B: 0x18, A: 255}
	fg := color.RGBA{R: 0xe8, G: 0xe6, B: 0xe3, A: 255}
	prim := color.RGBA{R: 0xb8, G: 0xa0, B: 0xc8, A: 255}
	for y := 0; y < o.Height; y++ {
		for x := 0; x < o.Width; x++ {
			dst.SetRGBA(x, y, bar)
		}
	}
	resetDrawCounts()
	bw := buttonWidth(o.DPI)
	chip := o.Height
	if chip < 20 {
		chip = 20
	}
	right := o.Width
	paint := func(slot Slot, kind Kind, w int, col color.RGBA) {
		left := right - w
		cx := left + w/2
		cy := o.Height / 2
		s := o.Height * 2 / 5
		if s < 10 {
			s = 10
		}
		if s > o.Height-6 {
			s = o.Height - 6
		}
		if w > 8 && s > w-8 {
			s = w - 8
		}
		noteDraw(slot)
		Paint(dst, Raster(kind, cx, cy, s, StrokeForSize(s)), col, bar)
		right = left
	}
	paint(SlotClose, Close, bw, fg)
	maxKind := Max
	if o.Zoomed {
		maxKind = Restore
	}
	paint(SlotMax, maxKind, bw, fg)
	paint(SlotMin, Min, bw, fg)
	right -= 6 * o.DPI / 96
	cafe := prim
	if !o.CaffeineOn {
		cafe = fg
	}
	paint(SlotCoffee, Coffee, chip, cafe)
	right -= 4 * o.DPI / 96
	bell := fg
	if o.BellUnread {
		bell = prim
	}
	paint(SlotBell, Bell, chip, bell)
	return dst
}

// WritePNG writes dst to path.
func WritePNG(path string, dst *image.RGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, dst)
}
