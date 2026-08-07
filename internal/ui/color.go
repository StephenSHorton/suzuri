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
	// Mode bits match vt10x/state.go (attrReverse=1<<0 … attrBold=1<<2 …).
	const (
		attrReverse = 1 << 0
		attrBold    = 1 << 2
	)
	bold := g.Mode&attrBold != 0
	// vt10x setChar already swaps FG/BG into the stored glyph when reverse is
	// set (and leaves Mode|attrReverse). Do not re-swap here — that undoes
	// reverse video so selection highlights (SGR 7 / lipgloss Reverse) paint
	// as normal text with a skipped black default BG (invisible on rain).
	fr, fg, fb := colorToRGB(g.FG, bold)
	br, bg, bb := colorToRGB(g.BG, false)
	// Truecolor SGR ignores the bold brighten path in colorToRGB (only ANSI
	// 0–7 step up). Modest paint-time FG lift stands in for a bold face
	// (Darwin often has none). Do NOT invent a selection-style background for
	// bold alone — lots of TUIs use SGR 1 for emphasis (headers, markdown,
	// ls dirs). Real list selection is SGR 7 reverse / explicit BG (below).
	// Stamping a band on every bright bold cell made normal bold look selected.
	if bold {
		fr, fg, fb = brightenBoldRGB(fr, fg, fb)
	}
	// Reverse video always needs an opaque field. If the stored BG still
	// resolves near-black (truecolor 0 collides with ANSI black; some TUIs
	// reverse light-on-dark into dark-on-dark), force a light selection field
	// so paint does not skip the fill under shell rain / transparent default BG.
	if g.Mode&attrReverse != 0 && nearBlackRGB(br, bg, bb) {
		br, bg, bb = 220, 220, 224
		if nearBlackRGB(fr, fg, fb) {
			// Both ends dark after convert — use dark ink on light field.
			fr, fg, fb = 18, 18, 22
		}
	}
	// Low-contrast reverse (dark slate on darker slate) also vanishes under
	// rain; ensure a minimum field/ink split when reverse is set.
	if g.Mode&attrReverse != 0 && lowContrastRGB(fr, fg, fb, br, bg, bb) {
		br, bg, bb = 220, 220, 224
		fr, fg, fb = 18, 18, 22
	}
	// Explicit (non-default) backgrounds: floor-lift so themed selection /
	// dim bands (Grok bg_visual ~#363636, prompt strips, code panels) stay
	// visible over ambient rain. Pure default black (0,0,0) is unchanged so
	// rain still shows through empty cells. Never invent a BG for bold-only
	// emphasis — those cells keep default black and skip fill in paint.
	if g.Mode&attrReverse == 0 {
		br, bg, bb = ensureRainVisibleBG(br, bg, bb)
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

// rainVisibleBGFloor is the minimum mean RGB for an explicit cell background
// when shell rain/ambient is under the grid. Below this, a non-zero slate
// still "paints" but reads as no highlight on busy underlays.
const rainVisibleBGFloor = 72

// ensureRainVisibleBG raises very dark non-default backgrounds to a floor
// that remains readable over ambient rain. Default pure-black stays 0,0,0
// so paint can skip the fill (rain shows through).
func ensureRainVisibleBG(r, g, b byte) (byte, byte, byte) {
	if r == 0 && g == 0 && b == 0 {
		return r, g, b
	}
	mean := (int(r) + int(g) + int(b)) / 3
	if mean >= rainVisibleBGFloor {
		return r, g, b
	}
	boost := rainVisibleBGFloor - mean
	lift := func(c byte) byte {
		v := int(c) + boost
		if v > 255 {
			return 255
		}
		return byte(v)
	}
	return lift(r), lift(g), lift(b)
}

func nearBlackRGB(r, g, b byte) bool {
	return r < 28 && g < 28 && b < 28
}

// lowContrastRGB reports FG and BG that are too close to distinguish as a band.
func lowContrastRGB(fr, fg, fb, br, bg, bb byte) bool {
	dr := absByte(fr, br)
	dg := absByte(fg, bg)
	db := absByte(fb, bb)
	return dr+dg+db < 90
}

func absByte(a, b byte) int {
	if a >= b {
		return int(a - b)
	}
	return int(b - a)
}

// brightenBoldRGB is a paint-time stand-in for a bold face on truecolor /
// already-bright ANSI ink (where colorToRGB cannot step the palette).
// Modest lift only — not a selection cue. List selection must use reverse/BG.
func brightenBoldRGB(r, g, b byte) (byte, byte, byte) {
	// ~35% toward white: readable emphasis without looking like a highlight bar.
	return pushTowardWhite(r, 90), pushTowardWhite(g, 90), pushTowardWhite(b, 90)
}

func pushTowardWhite(c byte, amount int) byte {
	if amount < 1 {
		return c
	}
	// c + (255-c)*amount/255
	d := (int(255-c) * amount) / 255
	v := int(c) + d
	if v > 255 {
		return 255
	}
	return byte(v)
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
	// Common terminal / chrome UI symbol blocks (Gohu Nerd Font covers these).
	switch {
	case r >= 0x2190 && r <= 0x21FF: // Arrows: ← → ↑ ↓ ⇒ …
		return r
	case r >= 0x2200 && r <= 0x22FF: // Math: ∞ ≈ ≠ ≤ ≥ …
		return r
	case r >= 0x2300 && r <= 0x23FF: // Technical: ⌘ ⌥ ⌫ ⏎ …
		return r
	case r >= 0x2500 && r <= 0x259F: // Box / block drawing
		return r
	case r >= 0x25A0 && r <= 0x25FF: // Geometric: ● ○ ◉ ◆ ▶ …
		return r
	case r >= 0x2600 && r <= 0x26FF: // Misc symbols: ☕ ☀ …
		return r
	case r >= 0x2700 && r <= 0x27BF: // Dingbats: ✓ ✗ ❯ ✂ …
		return r
	case r >= 0x2800 && r <= 0x28FF: // Braille spinners
		return r
	case r >= 0x2000 && r <= 0x206F: // General punctuation: • — …
		return r
	}
	// Latin / Greek used in TUIs (λ); drop other exotic scripts without fallbacks.
	if r > 0x024F {
		switch r {
		case 'λ', 'μ', 'π', 'Σ', 'Ω':
			return r
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
