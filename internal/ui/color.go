package ui

import (
	"sync"
	"unicode"

	"github.com/hinshun/vt10x"

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

// ANSI 0–15 (and xterm 256 cube/grayscale) → RGB.
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

// cellPix is one painted cell (viewport).
type cellPix struct {
	Ch         rune
	FR, FG, FB byte // foreground
	BR, BG, BB byte // background
	Bold       bool
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

func displayRune(r rune) rune {
	if r == 0 || r == 0xFFFD {
		return ' '
	}
	// C0 / DEL / C1 controls
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return ' '
	}
	// Private Use (Nerd Fonts etc.) → empty, not a □ mystery box
	if r >= 0xE000 && r <= 0xF8FF {
		return ' '
	}
	if r >= 0xF0000 {
		return ' '
	}
	// CJK (Han / kana / fullwidth) — drawn with fallback when mono lacks glyphs.
	if isEastAsianRune(r) {
		return r
	}
	// Status / spinner glyphs used by chrome (tab activity) and TUIs.
	// Braille Patterns = the classic 6-dot cells (⠿ ⠋ …) every CLI uses.
	if r >= 0x2800 && r <= 0x28FF {
		return r
	}
	// Geometric Shapes: ● ○ ◉ ◆ ◎ ◐ …
	if r >= 0x25A0 && r <= 0x25FF {
		return r
	}
	// Beyond Latin Extended-B: keep box-drawing / light punctuation; drop
	// exotic scripts we cannot paint cleanly without per-script fallbacks.
	if r > 0x024F &&
		!(r >= 0x2500 && r <= 0x259F) && // box / block drawing
		!(r >= 0x2000 && r <= 0x206F) { // general punctuation
		switch r {
		case '✓', '✗', '→', '←', '▶', '❯', 'λ', '•':
			return ' '
		default:
			if !unicode.In(r, unicode.Latin, unicode.Common) {
				return ' '
			}
		}
	}
	if unicode.Is(unicode.Cf, r) {
		return ' '
	}
	if !unicode.IsPrint(r) && r != ' ' {
		return ' '
	}
	return r
}

// isEastAsianRune is true for scripts we paint via the CJK fallback face.
func isEastAsianRune(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0x31F0 && r <= 0x31FF: // Katakana Phonetic Extensions
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Ext A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK Compatibility Ideographs
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth and Fullwidth Forms
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK Symbols and Punctuation
		return true
	default:
		return false
	}
}
