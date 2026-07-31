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
