package ui

import "testing"

func TestGlassAccentColorPacksAABBGGRR(t *testing.T) {
	// Windows GradientColor is AABBGGRR. A cool RGB tint packed as
	// AARRGGBB is what DWM would read as orange (R/B swapped).
	const a byte = 0x80
	blue := glassAccentColor(a, 0x20, 0x60, 0xC0)
	if blue != 0x80C06020 {
		t.Fatalf("blue AABBGGRR %#x want 0x80c06020", blue)
	}
	asARGB := uint32(a)<<24 | 0x20<<16 | 0x60<<8 | 0xC0
	if blue == asARGB {
		t.Fatal("packed AARRGGBB; DWM would render that tint orange")
	}
	if asARGB != 0x802060C0 {
		t.Fatalf("control ARGB %#x", asARGB)
	}
	orangeIfSwapped := glassAccentColor(a, 0xC0, 0x60, 0x20)
	if orangeIfSwapped != 0x802060C0 {
		t.Fatalf("swapped pack %#x", orangeIfSwapped)
	}
	if glassAccentColor(0xAA, 0, 0, 0) != 0xAA000000 {
		t.Fatalf("black tint %#x", glassAccentColor(0xAA, 0, 0, 0))
	}
	if glassAccentColor(0xFF, 0xFF, 0, 0) != 0xFF0000FF {
		t.Fatal("red must sit in the low byte (AABBGGRR)")
	}
	if glassAccentColor(0xFF, 0, 0, 0xFF) != 0xFFFF0000 {
		t.Fatal("blue must sit in the 0x00FF0000 byte")
	}
}
