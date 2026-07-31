package ui

import (
	"github.com/hinshun/vt10x"
	"github.com/lxn/win"
)

// ANSI 0–15 (and xterm 256 cube/grayscale) → RGB for GDI.
// Truecolor from vt10x is packed as r<<16|g<<8|b with value often >255.

var ansi16 = [16][3]byte{
	{0, 0, 0},       // black
	{205, 49, 49},   // red
	{13, 188, 121},  // green
	{229, 229, 16},  // yellow
	{36, 114, 200},  // blue
	{188, 63, 188},  // magenta
	{17, 168, 205},  // cyan
	{204, 204, 204}, // light grey
	{118, 118, 118}, // dark grey
	{241, 76, 76},   // light red
	{35, 209, 139},  // light green
	{245, 245, 67},  // light yellow
	{59, 142, 234},  // light blue
	{214, 112, 214}, // light magenta
	{41, 184, 219},  // light cyan
	{229, 229, 229}, // white
}

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
		rgb := ansi16[idx]
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
		attrReverse   = 1 << iota
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
