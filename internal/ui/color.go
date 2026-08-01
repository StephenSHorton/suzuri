package ui

import (
	"sync"

	"github.com/hinshun/vt10x"
	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// Active shell ANSI-16 table (theme remapped). Protected for paint vs settings.
var (
	ansiMu   sync.RWMutex
	ansi16   = chrome.StockANSI16
	ansiMode = config.ANSIMapSoft
)

// SetShellANSIMap updates the live 16-color remap used by glyph paint.
func SetShellANSIMap(mode string) {
	ansiMu.Lock()
	defer ansiMu.Unlock()
	ansiMode = mode
	ansi16 = chrome.RemapANSI16(mode)
}

// ANSI 0–15 (and xterm 256 cube/grayscale) → RGB for GDI.
// Truecolor from vt10x is packed as r<<16|g<<8|b with value often >255.

func colorToRGB(c vt10x.Color, bold bool) (r, g, b byte) {
	switch c {
	case vt10x.DefaultFG:
		return 220, 220, 220
	case vt10x.DefaultBG, vt10x.DefaultCursor:
		return 0, 0, 0
	}

	v := uint32(c)
	// Truecolor: packed 0x00RRGGBB when set via SGR 38;2 / 48;2.
	// Palette indices are 0–255.
	if v > 255 {
		return byte(v >> 16), byte(v >> 8), byte(v)
	}

	idx := int(v)
	if idx < 16 {
		if bold && idx < 8 {
			idx += 8
		}
		ansiMu.RLock()
		rgb := ansi16[idx]
		ansiMu.RUnlock()
		return rgb[0], rgb[1], rgb[2]
	}

	// xterm 256: 16–231 cube, 232–255 grayscale
	if idx < 232 {
		idx -= 16
		r6 := idx / 36
		g6 := (idx % 36) / 6
		b6 := idx % 6
		return cube6(r6), cube6(g6), cube6(b6)
	}
	// grayscale
	level := byte(8 + (idx-232)*10)
	return level, level, level
}

func cube6(v int) byte {
	if v == 0 {
		return 0
	}
	return byte(55 + v*40)
}

func rgbCOLORREF(r, g, b byte) win.COLORREF {
	return win.RGB(r, g, b)
}

// cellPix is one painted cell (viewport).
type cellPix struct {
	Ch       rune
	FR, FG, FB byte // foreground
	BR, BG, BB byte // background
	Bold     bool
}

func glyphToCell(g vt10x.Glyph) cellPix {
	// attrBold is unexported; Mode bit 1<<2 from vt10x state: attrBold = 1 << iota after reverse, underline
	// From state.go: attrReverse, attrUnderline, attrBold, attrGfx, attrItalic, attrBlink, attrWrap
	const (
		attrReverse = 1 << iota
		attrUnderline
		attrBold
		attrGfx
		attrItalic
		attrBlink
		attrWrap
	)
	bold := g.Mode&attrBold != 0
	fr, fg, fb := colorToRGB(g.FG, bold)
	br, bg, bb := colorToRGB(g.BG, false)
	if g.Mode&attrReverse != 0 {
		fr, br = br, fr
		fg, bg = bg, fg
		fb, bb = bb, fb
	}
	return cellPix{
		Ch:   displayRune(g.Char),
		FR:   fr,
		FG:   fg,
		FB:   fb,
		BR:   br,
		BG:   bg,
		BB:   bb,
		Bold: bold,
	}
}
