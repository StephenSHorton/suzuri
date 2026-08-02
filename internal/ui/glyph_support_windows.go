//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// GGI_MARK_NONEXISTING_GLYPHS — missing cmap entries become 0xFFFF.
const ggiMarkNonexistingGlyphs = 0x0001

var (
	modGdi32Glyph      = syscall.NewLazyDLL("gdi32.dll")
	procGetGlyphIndices = modGdi32Glyph.NewProc("GetGlyphIndicesW")
)

// fontHasRunes reports whether the font currently selected in hdc maps every
// rune (missing glyphs are 0xFFFF when GGI_MARK_NONEXISTING_GLYPHS is set).
func fontHasRunes(hdc win.HDC, runes ...rune) bool {
	if hdc == 0 || len(runes) == 0 {
		return false
	}
	if procGetGlyphIndices.Find() != nil {
		return false
	}
	// UTF-16 code units (BMP only — our key glyphs are BMP).
	u16 := make([]uint16, len(runes))
	for i, r := range runes {
		if r > 0xFFFF {
			return false
		}
		u16[i] = uint16(r)
	}
	gi := make([]uint16, len(runes))
	ret, _, _ := procGetGlyphIndices.Call(
		uintptr(hdc),
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(len(u16)),
		uintptr(unsafe.Pointer(&gi[0])),
		uintptr(ggiMarkNonexistingGlyphs),
	)
	// GDI_ERROR is (DWORD)-1.
	if ret == 0 || ret == ^uintptr(0) {
		return false
	}
	for _, g := range gi {
		if g == 0xFFFF {
			return false
		}
	}
	return true
}

// probeKeyGlyphs checks the active UI font for key glyphs and updates chrome.
// Safe after fonts are created (uses screen DC if hwnd is not ready yet).
func (u *winUI) probeKeyGlyphs() {
	if u == nil || u.font == 0 {
		chrome.SetKeyGlyphSupport(false, false)
		chrome.SetTabStateGlyphs(chrome.TabGlyphsASCII)
		u.primaryHasGeo = false
		u.primaryHasBraille = false
		return
	}
	hwnd := u.hwnd
	hdc := win.GetDC(hwnd)
	if hdc == 0 {
		chrome.SetKeyGlyphSupport(false, false)
		chrome.SetTabStateGlyphs(chrome.TabGlyphsASCII)
		u.primaryHasGeo = false
		u.primaryHasBraille = false
		return
	}
	old := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	fancy := fontHasRunes(hdc, '⇧', '⌃')
	arrows := fontHasRunes(hdc, '↑', '↓')
	// Prefer primary face for status marks so advance width matches the cell grid.
	u.primaryHasGeo = fontHasRunes(hdc, chrome.TabGlyphRunes(chrome.TabGlyphsGeometric)...)
	u.primaryHasBraille = fontHasRunes(hdc, chrome.TabGlyphRunes(chrome.TabGlyphsBraille)...)
	brailleOK := u.primaryHasBraille
	if !brailleOK && u.symFont != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.symFont))
		brailleOK = fontHasRunes(hdc, chrome.TabGlyphRunes(chrome.TabGlyphsBraille)...)
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
	// Braille is the product choice for tab activity (same family as CLI spinners).
	// Cascadia symbol fallback paints it when FiraCode Nerd lacks the block.
	tabSet, tabName := chrome.TabGlyphsASCII, "ascii"
	switch {
	case brailleOK:
		tabSet, tabName = chrome.TabGlyphsBraille, "braille"
	case u.primaryHasGeo:
		tabSet, tabName = chrome.TabGlyphsGeometric, "geometric"
	default:
		if u.symFont != 0 {
			win.SelectObject(hdc, win.HGDIOBJ(u.symFont))
			if fontHasRunes(hdc, chrome.TabGlyphRunes(chrome.TabGlyphsGeometric)...) {
				tabSet, tabName = chrome.TabGlyphsGeometric, "geometric"
			}
			win.SelectObject(hdc, win.HGDIOBJ(u.font))
		}
	}
	win.SelectObject(hdc, old)
	win.ReleaseDC(hwnd, hdc)
	chrome.SetKeyGlyphSupport(fancy, arrows)
	chrome.SetTabStateGlyphs(tabSet)
	log.Debug("key glyph support", "fancy", fancy, "arrows", arrows, "tabs", tabName,
		"face", fontFaceName(u.font), "sym", fontFaceName(u.symFont),
		"primaryGeo", u.primaryHasGeo, "primaryBraille", u.primaryHasBraille)
}
