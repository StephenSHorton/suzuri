package ui

import (
	"testing"

	"github.com/StephenSHorton/suzuri/internal/config"
)

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

func TestGlassFrostAlphaIsContinuous(t *testing.T) {
	if glassFrostAlpha(0, 0) != 0 {
		t.Fatalf("blur 0 veil 0 must be clear, frost %d", glassFrostAlpha(0, 0))
	}
	// Old 20–196 curve put blur 36 at 99 — a solid dark slab.
	if a := glassFrostAlpha(36, 0); a == 0 || a >= 64 {
		t.Fatalf("blur 36 frost %d must stay see-through (was 99)", a)
	}
	prev := glassFrostAlpha(0, 0)
	for blur := 1; blur <= config.GlassBlurMax; blur++ {
		cur := glassFrostAlpha(blur, 0)
		if cur < prev {
			t.Fatalf("frost dropped at blur %d: %d → %d", blur, prev, cur)
		}
		if int(cur)-int(prev) > 4 {
			t.Fatalf("frost jumped at blur %d: %d → %d", blur, prev, cur)
		}
		prev = cur
	}
	jump := int(glassFrostAlpha(40, 0)) - int(glassFrostAlpha(36, 0))
	if jump < 0 || jump > 12 {
		t.Fatalf("36→40 must not cliff: Δ %d", jump)
	}
	if glassFrostAlpha(config.GlassBlurMax, 0) <= glassFrostAlpha(0, 0) {
		t.Fatal("max blur must frost more than 0")
	}
	if glassFrostAlpha(16, 40) <= glassFrostAlpha(16, 0) {
		t.Fatal("veil must add frost")
	}
	if glassFrostAlpha(0, 100) > 200 || glassFrostAlpha(config.GlassBlurMax, 100) > 200 {
		t.Fatal("frost alpha cap")
	}
}

func TestGlassPresentBitsHaveNoAlpha(t *testing.T) {
	if glassPresentBits != 24 {
		t.Fatalf("present bits %d — 32-bit DIB realizes alpha and kills the color key", glassPresentBits)
	}
}

func TestGlassTintRGBLiftsBlackVoid(t *testing.T) {
	r, g, b := glassTintRGB()
	if r == 0 && g == 0 && b == 0 {
		t.Fatal("black GradientColor RGB is a solid plate; lift HC void")
	}
	if glassAccentColor(50, r, g, b)&0x00FFFFFF == 0 {
		t.Fatal("tinted acrylic must not be #000")
	}
}
