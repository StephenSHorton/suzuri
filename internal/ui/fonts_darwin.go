//go:build darwin

package ui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"

	"github.com/StephenSHorton/suzuri/assets"
	"github.com/StephenSHorton/suzuri/internal/config"
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

// monoFaceFiles maps settings face names (lower) to candidate font files.
// First existing file wins. User Library is preferred for third-party monos.
func monoFaceFiles() map[string][]string {
	home, _ := os.UserHomeDir()
	userFonts := filepath.Join(home, "Library", "Fonts")
	return map[string][]string{
		"menlo": {
			"/System/Library/Fonts/Menlo.ttc",
		},
		"sf mono": {
			"/System/Library/Fonts/SFNSMono.ttf",
			"/System/Library/Fonts/SFNSMono.ttc",
		},
		"monaco": {
			"/System/Library/Fonts/Monaco.ttf",
		},
		"courier new": {
			"/System/Library/Fonts/Supplemental/Courier New.ttf",
		},
		"fira code": {
			filepath.Join(userFonts, "FiraCodeNerdFontMono-Regular.ttf"),
			filepath.Join(userFonts, "FiraCodeNerdFont-Regular.ttf"),
			filepath.Join(userFonts, "FiraCode-Regular.ttf"),
			"/Library/Fonts/FiraCode-Regular.ttf",
		},
		"jetbrains mono": {
			filepath.Join(userFonts, "JetBrainsMono-Regular.ttf"),
			filepath.Join(userFonts, "JetBrainsMonoNL-Regular.ttf"),
			"/Library/Fonts/JetBrainsMono-Regular.ttf",
		},
		"source code pro": {
			filepath.Join(userFonts, "SourceCodePro-Regular.ttf"),
			"/Library/Fonts/SourceCodePro-Regular.ttf",
		},
	}
}

var (
	fontMu     sync.Mutex
	bundledTTF *truetype.Font
	cjkTTF     *truetype.Font
	cjkPath    string
	bundledOK  bool

	// Active primary mono (settings FontFace) for coverage checks.
	activePrimaryTTF *truetype.Font
	// Cache loaded named fonts (path → parsed TTF).
	namedTTFCache = map[string]*truetype.Font{}
	// Cache opentype faces for TTC/OTF that truetype can't own.
	// We store raw file bytes path → collection index 0 face factory via bytes.
	namedOTFCache = map[string]*opentype.Font{}
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
	activePrimaryTTF = ttf
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
	activePrimaryTTF = nil
	namedTTFCache = map[string]*truetype.Font{}
	namedOTFCache = map[string]*opentype.Font{}
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

func faceFromOTF(otf *opentype.Font, sizePx float64) font.Face {
	if otf == nil {
		return nil
	}
	if sizePx < 10 {
		sizePx = 14
	}
	face, err := opentype.NewFace(otf, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingNone,
	})
	if err != nil {
		return nil
	}
	return face
}

func isBundledFaceName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return true
	}
	return strings.EqualFold(n, BundledFace) ||
		strings.EqualFold(n, config.DefaultFontFace) ||
		strings.Contains(strings.ToLower(n), "gohu")
}

// faceForName returns a monospaced face for the settings FontFace + size.
// Falls back to bundled Gohu, then Go Mono.
func faceForName(faceName string, sizePx float64) font.Face {
	if sizePx < 10 {
		sizePx = 14
	}
	if isBundledFaceName(faceName) {
		fontMu.Lock()
		activePrimaryTTF = bundledTTF
		ttf := bundledTTF
		fontMu.Unlock()
		if face := faceFromTTF(ttf, sizePx); face != nil {
			return face
		}
	} else if face := loadNamedMonoFace(faceName, sizePx); face != nil {
		return face
	}
	// Fallback chain: bundled → Go Mono.
	fontMu.Lock()
	activePrimaryTTF = bundledTTF
	ttf := bundledTTF
	fontMu.Unlock()
	if face := faceFromTTF(ttf, sizePx); face != nil {
		log.Warn("font face fallback to bundled", "want", faceName)
		return face
	}
	fb, err := truetype.Parse(gomono.TTF)
	if err != nil {
		return nil
	}
	fontMu.Lock()
	activePrimaryTTF = fb
	fontMu.Unlock()
	return faceFromTTF(fb, sizePx)
}

// faceForSize returns the bundled monospaced face (legacy helper).
func faceForSize(sizePx float64) font.Face {
	return faceForName(BundledFace, sizePx)
}

// loadNamedMonoFace resolves a settings face name to a font.Face.
func loadNamedMonoFace(faceName string, sizePx float64) font.Face {
	key := strings.ToLower(strings.TrimSpace(faceName))
	paths := monoFaceFiles()[key]
	// Also try fuzzy: any path list whose key is contained in the name.
	if len(paths) == 0 {
		for k, ps := range monoFaceFiles() {
			if strings.Contains(key, k) || strings.Contains(k, key) {
				paths = ps
				break
			}
		}
	}
	// Scan user fonts directory for a filename containing the name.
	if len(paths) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			dir := filepath.Join(home, "Library", "Fonts")
			_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() {
					return nil
				}
				base := strings.ToLower(info.Name())
				want := strings.ReplaceAll(key, " ", "")
				if strings.Contains(strings.ReplaceAll(base, " ", ""), want) &&
					(strings.HasSuffix(base, ".ttf") || strings.HasSuffix(base, ".otf") || strings.HasSuffix(base, ".ttc")) {
					paths = append(paths, path)
				}
				return nil
			})
		}
	}

	for _, path := range paths {
		if face := faceFromPath(path, sizePx); face != nil {
			log.Info("loaded mono face", "name", faceName, "path", path)
			return face
		}
	}
	log.Warn("mono face not found on disk", "name", faceName)
	return nil
}

func faceFromPath(path string, sizePx float64) font.Face {
	fontMu.Lock()
	if ft, ok := namedTTFCache[path]; ok {
		activePrimaryTTF = ft
		fontMu.Unlock()
		return faceFromTTF(ft, sizePx)
	}
	if ot, ok := namedOTFCache[path]; ok {
		activePrimaryTTF = nil // opentype — primaryHasRune uses heuristic
		fontMu.Unlock()
		return faceFromOTF(ot, sizePx)
	}
	fontMu.Unlock()

	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	// Prefer classic TrueType parse.
	if ft, err := truetype.Parse(b); err == nil {
		fontMu.Lock()
		namedTTFCache[path] = ft
		activePrimaryTTF = ft
		fontMu.Unlock()
		return faceFromTTF(ft, sizePx)
	}
	// TTC / CFF OpenType collections (Menlo, etc.).
	if col, err := opentype.ParseCollection(b); err == nil && col.NumFonts() > 0 {
		ot, err := col.Font(0)
		if err == nil {
			fontMu.Lock()
			namedOTFCache[path] = ot
			activePrimaryTTF = nil
			fontMu.Unlock()
			return faceFromOTF(ot, sizePx)
		}
	}
	if ot, err := opentype.Parse(b); err == nil {
		fontMu.Lock()
		namedOTFCache[path] = ot
		activePrimaryTTF = nil
		fontMu.Unlock()
		return faceFromOTF(ot, sizePx)
	}
	return nil
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
	// Never claim CJK on the primary mono — always use CJK fallback face.
	if isEastAsianRune(r) || isHalfwidthKatakana(r) {
		return false
	}
	fontMu.Lock()
	ft := activePrimaryTTF
	if ft == nil {
		ft = bundledTTF
	}
	fontMu.Unlock()
	if ft != nil {
		return ttfHasRune(ft, r)
	}
	// OpenType primary (e.g. Menlo via collection): assume Latin/common coverage.
	return r < 0x2500
}

func cjkHasRune(r rune) bool {
	fontMu.Lock()
	ft := cjkTTF
	fontMu.Unlock()
	return ttfHasRune(ft, r)
}
