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

// Grok /resume selection is a themed truecolor band (not reverse). Host must
// paint the explicit RGB as-is so multi-tone hierarchy from the TUI is kept.
// Floor-lifting here would collapse paste chips / code / prompt bands.
func TestThemedSelectionBandVisibleOverRain(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 2))
	// Light ink on GrokNight-ish selection slate + bold (real picker contract).
	// Fork floor-lifts selection to ~96; native 54 still must paint non-zero.
	if _, err := term.Write([]byte("\x1b[38;2;225;225;225m\x1b[48;2;54;54;54m\x1b[1mROW\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.BR == 0 && c.BG == 0 && c.BB == 0 {
		t.Fatalf("selection band became default black (invisible under rain)")
	}
	if c.BR != 54 || c.BG != 54 || c.BB != 54 {
		t.Fatalf("host must not recolor themed BG, got %d,%d,%d", c.BR, c.BG, c.BB)
	}
}

// Sunken paste chip (Grok paste_bg ~#111111) must stay darker than elevated
// panels — no host floor that equalizes all dark BGs.
func TestPasteChipBGPreserved(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 2))
	if _, err := term.Write([]byte("\x1b[38;2;200;200;200m\x1b[48;2;17;17;17m[Image #1]\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	c := glyphToCell(term.Cell(0, 0))
	if c.BR != 17 || c.BG != 17 || c.BB != 17 {
		t.Fatalf("paste chip BG must stay 17,17,17 got %d,%d,%d", c.BR, c.BG, c.BB)
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
