//go:build darwin

package ui

import (
	"image"
	"testing"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// On glass the logo must sit on top of the veil. It used to replace the veil
// pixels with a thinner alpha, so the mark showed the bright desktop through
// and read as solid even at a low Logo setting.
func TestGlassWatermarkBlendsOverVeil(t *testing.T) {
	RegisterBundledFonts()
	t.Cleanup(UnregisterBundledFonts)
	p := newSoftwarePainter("", 14)
	if p.cjkFace == nil || !cjkHasRune(shellWatermarkRune) {
		t.Skip("no CJK face for the watermark")
	}
	const veil = 191 // glass_veil 75
	fb := image.NewRGBA(image.Rect(0, 0, 600, 400))
	fillRectAlpha(fb, 0, 0, 600, 400, 0, 0, 0, veil)
	p.paintShellWatermark(fb, 0, 400, 0.40, true, false)

	// Composite over a white desktop, as the window server would.
	drew := false
	maxLum := 0
	for i := 0; i < len(fb.Pix); i += 4 {
		a := fb.Pix[i+3]
		if a < veil {
			t.Fatalf("pixel %d alpha %d is thinner than the veil %d", i/4, a, veil)
		}
		if a == veil && fb.Pix[i] == 0 && fb.Pix[i+1] == 0 && fb.Pix[i+2] == 0 {
			continue
		}
		drew = true
		lum := 0
		for c := 0; c < 3; c++ {
			v := int(fb.Pix[i+c]) + 255*(255-int(a))/255
			lum += v
		}
		if lum > maxLum {
			maxLum = lum
		}
	}
	if !drew {
		t.Fatal("watermark drew nothing")
	}
	prim := int(chrome.PrimR) + int(chrome.PrimG) + int(chrome.PrimB)
	if maxLum >= prim {
		t.Fatalf("40%% logo over white composites to %d, at or above primary %d", maxLum, prim)
	}
}
