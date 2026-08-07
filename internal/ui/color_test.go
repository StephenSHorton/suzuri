package ui

import (
	"testing"

	"github.com/hinshun/vt10x"
)

func TestColorToRGBANSI(t *testing.T) {
	r, g, b := colorToRGB(vt10x.Red, false)
	if r < 100 || g > 100 {
		t.Fatalf("red got %d,%d,%d", r, g, b)
	}
	// bold red → light red
	r2, _, _ := colorToRGB(vt10x.Red, true)
	if r2 <= r {
		t.Fatalf("bold should brighten ANSI red")
	}
}

func TestColorToRGBTruecolor(t *testing.T) {
	c := vt10x.Color(0x12<<16 | 0x34<<8 | 0x56)
	r, g, b := colorToRGB(c, false)
	if r != 0x12 || g != 0x34 || b != 0x56 {
		t.Fatalf("got %02x%02x%02x", r, g, b)
	}
}

func TestColorDefault(t *testing.T) {
	r, g, b := colorToRGB(vt10x.DefaultFG, false)
	if r < 200 {
		t.Fatalf("default fg %d,%d,%d", r, g, b)
	}
}

func TestDisplayRuneKeepsArrowsAndUISymbols(t *testing.T) {
	// These used to be blanked (looked like extra spaces in "dim → bright").
	keep := []rune{'→', '←', '↑', '↓', '⇒', '▶', '❯', '✓', '✗', '•', '☕', '∞', '⌘', '⌥', '⌫', 'λ'}
	for _, r := range keep {
		if got := displayRune(r); got != r {
			t.Fatalf("displayRune(%q U+%04X) = %q, want kept", r, r, got)
		}
	}
	// Still blank tofu / controls / private use.
	if displayRune(0) != ' ' || displayRune(0xE0A0) != ' ' {
		t.Fatal("expected tofu/PUA blanked")
	}
}

// Reverse video (SGR 7 / lipgloss Reverse) must produce a non-black background
// so list selection highlights are visible when default-black BG is left transparent.
func TestReverseVideoHighlightVisible(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 3))
	if _, err := term.Write([]byte("\x1b[7mSELECTED\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	for x := 0; x < 8; x++ {
		c := glyphToCell(term.Cell(x, 0))
		// After reverse: light-ish BG, dark-ish FG (not both black).
		if c.BR == 0 && c.BG == 0 && c.BB == 0 {
			t.Fatalf("cell %d reverse BG is black (highlight invisible) FR=%d,%d,%d",
				x, c.FR, c.FG, c.FB)
		}
		if c.FR > 100 && c.BR > 100 {
			// Both bright — reverse didn't separate ink from field.
			t.Fatalf("cell %d reverse not contrasted FR=%d BR=%d", x, c.FR, c.BR)
		}
	}
}

// Truecolor black packs as 0 (collides with ANSI black). Reverse of white-on-black
// must still get an opaque light field after conversion.
func TestReverseTruecolorBlackField(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(10, 2))
	// White FG, black BG, then reverse → black ink on white field.
	if _, err := term.Write([]byte("\x1b[38;2;255;255;255m\x1b[48;2;0;0;0m\x1b[7mHI\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if nearBlackRGB(c.BR, c.BG, c.BB) {
		t.Fatalf("truecolor reverse field still near-black BR=%d,%d,%d FR=%d,%d,%d",
			c.BR, c.BG, c.BB, c.FR, c.FG, c.FB)
	}
}

// Truecolor + SGR bold: modest ink lift (no bold face on Darwin). BG stays
// default-black so host rain can show through — bold is emphasis, not selection.
func TestBoldTruecolorInkBrightens(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 2))
	// Mid grey #90: brighten alone is enough; no selection band required.
	if _, err := term.Write([]byte("\x1b[38;2;144;144;144mX\x1b[1mY\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	normal := glyphToCell(term.Cell(0, 0))
	bold := glyphToCell(term.Cell(1, 0))
	if !bold.Bold {
		t.Fatal("expected bold mode on second cell")
	}
	if bold.FR <= normal.FR && bold.FG <= normal.FG && bold.FB <= normal.FB {
		t.Fatalf("bold truecolor ink not brighter: normal=%d,%d,%d bold=%d,%d,%d",
			normal.FR, normal.FG, normal.FB, bold.FR, bold.FG, bold.FB)
	}
	if !nearBlackRGB(bold.BR, bold.BG, bold.BB) {
		t.Fatalf("bold must not invent a selection band BR=%d,%d,%d", bold.BR, bold.BG, bold.BB)
	}
}

// Bright bold text (headers, markdown, ls dirs, etc.) must stay plain emphasis —
// no fake selection field. Real list selection uses reverse (TestReverse*).
func TestBoldBrightTruecolorNoSelectionBand(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 2))
	// #d0d0d0 bold on default BG.
	if _, err := term.Write([]byte("\x1b[38;2;208;208;208m\x1b[1mSEL\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if !c.Bold {
		t.Fatal("expected bold")
	}
	if !nearBlackRGB(c.BR, c.BG, c.BB) {
		t.Fatalf("bright bold must not paint a selection field BR=%d,%d,%d", c.BR, c.BG, c.BB)
	}
}

// Grok /resume selection is a themed truecolor band (bg_visual ~#363636), not
// reverse. Paint must keep a non-default field so rain hosts do not skip fill.
func TestThemedSelectionBandVisibleOverRain(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 2))
	// Light ink on GrokNight-ish selection slate + bold (real picker contract).
	if _, err := term.Write([]byte("\x1b[38;2;225;225;225m\x1b[48;2;54;54;54m\x1b[1mROW\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.BR == 0 && c.BG == 0 && c.BB == 0 {
		t.Fatalf("selection band became default black (invisible under rain)")
	}
	mean := (int(c.BR) + int(c.BG) + int(c.BB)) / 3
	if mean < rainVisibleBGFloor {
		t.Fatalf("selection band too dim for rain mean=%d BR=%d,%d,%d", mean, c.BR, c.BG, c.BB)
	}
}

// Default-black cells stay pure black so paint can leave rain visible.
func TestDefaultBGStaysTransparentForRain(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(8, 2))
	if _, err := term.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.BR != 0 || c.BG != 0 || c.BB != 0 {
		t.Fatalf("default BG must stay 0,0,0 for rain skip, got %d,%d,%d", c.BR, c.BG, c.BB)
	}
}

func TestEnsureRainVisibleBG(t *testing.T) {
	if r, g, b := ensureRainVisibleBG(0, 0, 0); r != 0 || g != 0 || b != 0 {
		t.Fatalf("pure black must stay 0,0,0 got %d,%d,%d", r, g, b)
	}
	r, g, b := ensureRainVisibleBG(54, 54, 54)
	mean := (int(r) + int(g) + int(b)) / 3
	if mean < rainVisibleBGFloor {
		t.Fatalf("dim slate not lifted: %d,%d,%d mean=%d", r, g, b, mean)
	}
	if r2, g2, b2 := ensureRainVisibleBG(120, 120, 120); r2 != 120 || g2 != 120 || b2 != 120 {
		t.Fatalf("already-bright BG changed: %d,%d,%d", r2, g2, b2)
	}
}
