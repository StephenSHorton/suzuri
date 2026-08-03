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
