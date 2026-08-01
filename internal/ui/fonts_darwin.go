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
//
// Prefer Arial Unicode: it has real half-width katakana (U+FF61–FF9F) used by
// matrix rain. AppleGothic maps those code points to glyph index 0 (.notdef),
// which freetype still "draws" as hollow tofu boxes.
var cjkFontPaths = []string{
	"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
	"/Library/Fonts/Arial Unicode.ttf",
	"/System/Library/Fonts/Supplemental/AppleGothic.ttf",
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
	ok := registerFonts()
	if ok {
		// Outside fontMu — initMatrixRainRunes builds faces via cjkFaceForSize.
		initMatrixRainRunes()
		log.Info("matrix rain glyphs", "count", len(matrixRainRunes))
	}
	return ok
}

func registerFonts() bool {
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
	// Gohu reports GlyphAdvance for missing CJK but paints .notdef boxes.
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
		// Must cover brand 硯. Prefer faces that also have half-width ｱ (rain).
		if ft.Index('硯') == 0 {
			continue
		}
		hasHW := ft.Index('ｱ') != 0
		cjkTTF = ft
		cjkPath = path
		log.Info("cjk fallback font ready", "path", path, "halfwidth_katakana", hasHW)
		// Prefer a face with half-width coverage when available.
		if hasHW {
			break
		}
		// Keep looking for a better face; if none, last with 硯 wins.
	}
	if cjkTTF == nil {
		log.Warn("no system CJK font found — Japanese glyphs will be blank")
	} else if cjkTTF.Index('ｱ') == 0 {
		log.Warn("cjk face lacks half-width katakana; matrix rain will use fullwidth/ASCII only")
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

// ttfHasRune is true when the font has a non-.notdef glyph for r.
// freetype still "draws" index 0 as a hollow box — never treat that as coverage.
func ttfHasRune(ft *truetype.Font, r rune) bool {
	return ft != nil && ft.Index(r) != 0
}

func faceFromTTF(ttf *truetype.Font, sizePx float64) font.Face {
	if ttf == nil {
		return nil
	}
	if sizePx < 10 {
		sizePx = 14
	}
	// HintingNone avoids freetype panics on some system CJK tables and
	// produces more reliable outlines for katakana at terminal sizes.
	return truetype.NewFace(ttf, &truetype.Options{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingNone,
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

// primaryHasRune / cjkHasRune report real glyph coverage (not .notdef).
func primaryHasRune(r rune) bool {
	fontMu.Lock()
	ft := bundledTTF
	fontMu.Unlock()
	return ttfHasRune(ft, r)
}

func cjkHasRune(r rune) bool {
	fontMu.Lock()
	ft := cjkTTF
	fontMu.Unlock()
	return ttfHasRune(ft, r)
}
