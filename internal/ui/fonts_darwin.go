//go:build darwin

package ui

import (
	"sync"

	"github.com/charmbracelet/log"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"

	"github.com/StephenSHorton/suzuri/assets"
)

// BundledFace is the logical face name for the embedded monospaced font.
const BundledFace = assets.FontFaceBundled

var (
	fontMu     sync.Mutex
	bundledTTF *truetype.Font
	bundledOK  bool
)

// RegisterBundledFonts parses the embedded Gohu TTF for software paint.
func RegisterBundledFonts() bool {
	fontMu.Lock()
	defer fontMu.Unlock()
	if bundledOK && bundledTTF != nil {
		return true
	}
	data := assets.BundledFontRegular
	if len(data) == 0 {
		log.Warn("bundled font embed empty")
		return false
	}
	ttf, err := truetype.Parse(data)
	if err != nil {
		log.Warn("bundled font parse failed", "err", err)
		return false
	}
	bundledTTF = ttf
	bundledOK = true
	log.Info("bundled font registered", "face", BundledFace, "bytes", len(data))
	return true
}

// UnregisterBundledFonts releases private font resources (call on exit).
func UnregisterBundledFonts() {
	fontMu.Lock()
	defer fontMu.Unlock()
	bundledTTF = nil
	bundledOK = false
}

// BundledFontReady is true after a successful RegisterBundledFonts.
func BundledFontReady() bool {
	fontMu.Lock()
	defer fontMu.Unlock()
	return bundledOK
}

// faceForSize returns a monospaced face at the given pixel size.
func faceForSize(sizePx float64) font.Face {
	if sizePx < 10 {
		sizePx = 14
	}
	fontMu.Lock()
	ttf := bundledTTF
	fontMu.Unlock()
	if ttf != nil {
		return truetype.NewFace(ttf, &truetype.Options{
			Size:    sizePx,
			DPI:     72,
			Hinting: font.HintingFull,
		})
	}
	// Fallback: Go Mono from x/image.
	fb, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil
	}
	return truetype.NewFace(fb, &truetype.Options{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}
