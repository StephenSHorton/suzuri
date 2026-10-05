package ui

import (
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// glassAccentColor packs DWM GradientColor as AABBGGRR. Windows reads the
// low byte as red and the next as green; AARRGGBB (or RGB/BGR swapped)
// turns a cool tint into orange.
func glassAccentColor(alpha, r, g, b byte) uint32 {
	return uint32(alpha)<<24 | uint32(b)<<16 | uint32(g)<<8 | uint32(r)
}

// glassFrostAlpha maps blur 0–80 and veil 0–100 onto one continuous frost.
// Accent acrylic GradientColor alpha is heavy: the old 20–196 curve put
// blur 36 at 99, which reads as a solid dark slab. Low blur must stay
// see-through (desktop clearly visible); the top of the slider frosts.
func glassFrostAlpha(blur, veil int) byte {
	if blur < 0 {
		blur = 0
	}
	if blur > config.GlassBlurMax {
		blur = config.GlassBlurMax
	}
	if veil < 0 {
		veil = 0
	}
	if veil > 100 {
		veil = 100
	}
	const maxBlurA = 112
	a := blur * maxBlurA / config.GlassBlurMax
	a += veil * 88 / 100
	if a > 200 {
		a = 200
	}
	return byte(a)
}

// glassTintRGB is the acrylic wash. High-contrast void is #000 — using
// that as GradientColor RGB makes any frost a black plate. Lift it the
// same way caption colors do so color-key holes stay 0,0,0 and the tint
// is a dark gray, not the key.
func glassTintRGB() (r, g, b byte) {
	r, g, b = chrome.VoidR, chrome.VoidG, chrome.VoidB
	if r == 0 && g == 0 && b == 0 {
		return 12, 12, 16
	}
	return r, g, b
}
