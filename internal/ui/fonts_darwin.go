//go:build darwin

package ui

import (
	"os"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"

	"github.com/StephenSHorton/suzuri/assets"
)

// BundledFace is the logical face name for the embedded monospaced font.
const BundledFace = assets.FontFaceBundled

// System CJK faces (plain .ttf preferred — truetype.Parse cannot read TTC).
var cjkFontPaths = []string{
	"/System/Library/Fonts/Supplemental/AppleGothic.ttf",
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
	"/Library/Fonts/Arial Unicode.ttf",
}

var (
	fontMu     sync.Mutex
	bundledTTF *truetype.Font
	cjkTTF     *truetype.Font
	cjkPath    string
	bundledOK  bool
)

// RegisterBundledFonts parses the embedded Gohu TTF and a system CJK face.
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

	// CJK fallback for 硯 / 猫 / matrix rain / shell JP text.
	// Gohu (and most Latin mono faces) have no Han/kana coverage.
	for _, path := range cjkFontPaths {
		b, err := os.ReadFile(path)
		if err != nil || len(b) == 0 {
			continue
		}
		ft, err := truetype.Parse(b)
		if err != nil {
			log.Debug("cjk font parse skip", "path", path, "err", err)
			continue
		}
		// Sanity: must cover brand 硯 and half-width katakana used in matrix rain.
		face := truetype.NewFace(ft, &truetype.Options{Size: 14, DPI: 72})
		_, okSuzuri := face.GlyphAdvance('硯')
		_, okKata := face.GlyphAdvance('ｱ')
		_ = face.Close()
		if !okSuzuri {
			continue
		}
		cjkTTF = ft
		cjkPath = path
		log.Info("cjk fallback font ready", "path", path, "katakana", okKata)
		break
	}
	if cjkTTF == nil {
		log.Warn("no system CJK font found — Japanese glyphs will be blank")
	}
	return true
}

// UnregisterBundledFonts releases private font resources (call on exit).
func UnregisterBundledFonts() {
	fontMu.Lock()
	defer fontMu.Unlock()
	bundledTTF = nil
	cjkTTF = nil
	cjkPath = ""
	bundledOK = false
}

// BundledFontReady is true after a successful RegisterBundledFonts.
func BundledFontReady() bool {
	fontMu.Lock()
	defer fontMu.Unlock()
	return bundledOK
}

func faceFromTTF(ttf *truetype.Font, sizePx float64) font.Face {
	if ttf == nil {
		return nil
	}
	if sizePx < 10 {
		sizePx = 14
	}
	return truetype.NewFace(ttf, &truetype.Options{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// faceForSize returns the primary monospaced face at the given pixel size.
func faceForSize(sizePx float64) font.Face {
	fontMu.Lock()
	ttf := bundledTTF
	fontMu.Unlock()
	if face := faceFromTTF(ttf, sizePx); face != nil {
		return face
	}
	// Fallback: Go Mono from x/image.
	fb, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil
	}
	return faceFromTTF(fb, sizePx)
}

// cjkFaceForSize returns the system CJK face (or nil).
func cjkFaceForSize(sizePx float64) font.Face {
	fontMu.Lock()
	ttf := cjkTTF
	fontMu.Unlock()
	return faceFromTTF(ttf, sizePx)
}
