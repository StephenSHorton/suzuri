package ui

import "github.com/StephenSHorton/suzuri/internal/config"

// glassAccentColor packs DWM GradientColor as AABBGGRR. Windows reads the
// low byte as red and the next as green; AARRGGBB (or RGB/BGR swapped)
// turns a cool tint into orange.
func glassAccentColor(alpha, r, g, b byte) uint32 {
	return uint32(alpha)<<24 | uint32(b)<<16 | uint32(g)<<8 | uint32(r)
}

// glassFrostAlpha maps blur 0–80 and veil 0–100 onto one continuous
// 20–220 frost. Same material at every step — only alpha changes — so
// 36→40 is a few units, not a Mica-Alt cliff.
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
	const minA, maxA = 20, 196
	a := minA + blur*(maxA-minA)/config.GlassBlurMax
	a += veil * 40 / 100
	if a > 220 {
		a = 220
	}
	return byte(a)
}
