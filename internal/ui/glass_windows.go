//go:build windows

package ui

import (
	"sync"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// Windows glass backdrop.
//
// Mac leaves chroma-key shell pixels at alpha 0 and blurs the desktop behind
// them (CGSSetWindowBackgroundBlurRadius, reapplied every turn). This port
// does not use a layered HWND: no UpdateLayeredWindow, no per-pixel alpha,
// no WS_EX_LAYERED. DWMWA_SYSTEMBACKDROP_TYPE draws Mica or Desktop Acrylic
// behind the existing window. DwmExtendFrameIntoClientArea(-1) is the
// documented non-layered color key — client pixels that stay RGB(0,0,0)
// show that material. Chroma-key cells already skip their own fill; the
// glass underlay is stock black so those cells remain the key.
//
// DWM has no point radius. Blur 0 selects Mica (no live blur). 1–39 is
// Desktop Acrylic. 40–80 is Tabbed Mica. Win11 22H2+ uses those materials
// only. The Win10 fallback is ACCENT_ENABLE_ACRYLICBLURBEHIND with
// AccentFlags=2 and a theme-void AABBGGRR tint — Flags=0 paints the
// system accent over the holes (bright orange on many machines). Veil 100
// is an opaque near-black fill instead of the color key. Rim stays in the
// Mac software painter; GDI text has no alpha glyph mask.

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaSystemBackdropType   = 38

	dwmsbtNone            int32 = 1
	dwmsbtMainWindow      int32 = 2 // Mica (static wallpaper tint)
	dwmsbtTransientWindow int32 = 3 // Desktop Acrylic (live blur)
	dwmsbtTabbedWindow    int32 = 4 // Tabbed Mica (heavier frost)

	// Blur slider → material. DWM has no radius; these are the visible steps.
	glassBlurAcrylicAfter = 0
	glassBlurTabbedAfter  = 39

	win11BackdropBuild = 22621

	wcaAccentPolicy = 19
	accentDisabled  = 0
	accentAcrylic   = 4

	// AccentFlags bit 1: honor GradientColor. Flags=0 makes DWM ignore the
	// packed tint and wash ACCENT_ENABLE_ACRYLICBLURBEHIND with the Windows
	// system accent (often a saturated orange / blue).
	accentFlagUseGradient = 2
)

type dwmMargins struct {
	Left, Right, Top, Bottom int32
}

type accentPolicy struct {
	State, Flags, Gradient, Animation uint32
}

type compositionAttribData struct {
	Attrib uint32
	data   unsafe.Pointer
	size   uintptr
}

var (
	dwmExtend  = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmExtendFrameIntoClientArea")
	setWinComp = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowCompositionAttribute")

	glassMu   sync.Mutex
	glassHw   win.HWND
	glassOn   bool
	glassKind int32
	glassBlur int
	glassVeil int
	glassDark bool
)

func winBuildNumber() uint32 {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0
	}
	return info.BuildNumber
}

// glassThemeTint is a theme-void wash in AABBGGRR. Dark mica already tints
// itself; this is the Win10 acrylic fallback so Flags=2 never falls back
// to the system accent.
func glassThemeTint(alpha byte) uint32 {
	return glassAccentColor(alpha, chrome.VoidR, chrome.VoidG, chrome.VoidB)
}

// glassBackdropType is the DWM material for cfg. Solid is none. DWM has no
// blur radius: 0 is Mica, 1–39 Acrylic, 40–80 Tabbed. Win11 22H2+ uses
// that material only — stacking an acrylic accent on top of it paints
// the system accent over the holes.
func glassBackdropType(c config.Config) int32 {
	if c.Backdrop != config.BackdropGlass {
		return dwmsbtNone
	}
	if c.GlassBlur <= glassBlurAcrylicAfter {
		return dwmsbtMainWindow
	}
	if c.GlassBlur <= glassBlurTabbedAfter {
		return dwmsbtTransientWindow
	}
	return dwmsbtTabbedWindow
}

// glassAccentForBlur is the Win10 composition tint. Higher blur → more
// frost. Veil adds theme-void wash. Flags is always accentFlagUseGradient
// when the accent is on, so GradientColor is not replaced by the accent.
func glassAccentForBlur(blur, veil int) (state, flags, color uint32) {
	if blur <= 0 && veil <= 0 {
		return accentDisabled, 0, 0
	}
	a := 28 + blur*2
	if veil > 0 {
		a += veil * 255 / 200
	}
	if a > 220 {
		a = 220
	}
	if blur <= 0 {
		if veil < 0 {
			veil = 0
		}
		if veil > 100 {
			veil = 100
		}
		return accentDisabled, 0, glassThemeTint(byte((veil*255 + 50) / 100))
	}
	return accentAcrylic, accentFlagUseGradient, glassThemeTint(byte(a))
}

// glassCompositionAccent is what SetWindowCompositionAttribute should
// receive. Win11 22H2+ must disable the accent so Mica / Desktop Acrylic
// / Tabbed stay visible through the color-key holes.
func glassCompositionAccent(build uint32, blur, veil int) (state, flags, color uint32) {
	if build >= win11BackdropBuild {
		return accentDisabled, 0, 0
	}
	state, flags, color = glassAccentForBlur(blur, veil)
	if state == accentDisabled {
		// No SYSTEMBACKDROP on Win10. Keep a faint theme-neutral acrylic
		// so glass-on at blur 0 is not a no-op.
		return accentAcrylic, accentFlagUseGradient, glassThemeTint(8)
	}
	return state, flags, color
}

// glassUsesColorKey is true when empty cells must stay pure black so DWM
// shows the backdrop. Veil 100 is an opaque wash, not a hole.
func glassUsesColorKey(c config.Config) bool {
	return c.Backdrop == config.BackdropGlass && c.GlassVeil < 100
}

// glassVeilABGR is a black AABBGGRR tint (0–100 → alpha 0–255).
func glassVeilABGR(veil int) uint32 {
	if veil < 0 {
		veil = 0
	}
	if veil > 100 {
		veil = 100
	}
	return glassAccentColor(byte((veil*255+50)/100), 0, 0, 0)
}

func glassMaterialName(kind int32) string {
	switch kind {
	case dwmsbtMainWindow:
		return "mica"
	case dwmsbtTransientWindow:
		return "acrylic"
	case dwmsbtTabbedWindow:
		return "tabbed"
	default:
		return "none"
	}
}

func (u *winUI) shellGlass() bool {
	return u != nil && u.cfg.Backdrop == config.BackdropGlass
}

func glassStateSame(hwnd win.HWND, on bool, kind int32, blur, veil int) bool {
	glassMu.Lock()
	defer glassMu.Unlock()
	return glassHw == hwnd && glassOn == on && glassKind == kind && glassBlur == blur && glassVeil == veil
}

func glassStateStore(hwnd win.HWND, on bool, kind int32, blur, veil int) {
	glassMu.Lock()
	glassHw, glassOn, glassKind, glassBlur, glassVeil = hwnd, on, kind, blur, veil
	glassMu.Unlock()
}

func dwmSetInt32(hwnd win.HWND, attr uint32, v int32) {
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), attr, unsafe.Pointer(&v), uint32(unsafe.Sizeof(v)))
}

func extendFrame(hwnd win.HWND, m dwmMargins) {
	if err := dwmExtend.Find(); err != nil {
		return
	}
	_, _, _ = dwmExtend.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))
}

func setAccent(hwnd win.HWND, state, flags, gradient uint32) {
	if err := setWinComp.Find(); err != nil {
		return
	}
	policy := accentPolicy{State: state, Flags: flags, Gradient: gradient}
	data := compositionAttribData{
		Attrib: wcaAccentPolicy,
		data:   unsafe.Pointer(&policy),
		size:   unsafe.Sizeof(policy),
	}
	_, _, _ = setWinComp.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
}

// applyGlassBackdrop pushes Backdrop / blur / veil onto the existing HWND.
// Safe to call before the window exists (no-op) and repeatedly; unchanged
// settings do not touch DWM. Call from config apply and once the HWND is born.
func (u *winUI) applyGlassBackdrop() {
	if u == nil || u.hwnd == 0 {
		return
	}
	on := u.shellGlass()
	kind := int32(dwmsbtNone)
	blur, veil := 0, 0
	if on {
		kind = glassBackdropType(u.cfg)
		blur = u.cfg.GlassBlur
		veil = u.cfg.GlassVeil
	}
	if glassStateSame(u.hwnd, on, kind, blur, veil) {
		return
	}

	if on && !glassDark {
		var dark int32 = 1
		dwmSetInt32(u.hwnd, dwmwaUseImmersiveDarkMode, dark)
		glassMu.Lock()
		glassDark = true
		glassMu.Unlock()
	}

	// System backdrop first. Win11 22H2+ honors it. Older builds ignore it;
	// the accent policy below is the blur fallback and the only veil tint.
	dwmSetInt32(u.hwnd, dwmwaSystemBackdropType, kind)
	if on {
		extendFrame(u.hwnd, dwmMargins{-1, -1, -1, -1})
	} else {
		// 1px extend keeps the DWM shadow and stops the native caption
		// from painting over a FrameWindows client that already ate it.
		extendFrame(u.hwnd, dwmMargins{Bottom: 1})
	}

	build := winBuildNumber()
	switch {
	case !on:
		setAccent(u.hwnd, accentDisabled, 0, 0)
	default:
		state, flags, color := glassCompositionAccent(build, blur, veil)
		setAccent(u.hwnd, state, flags, color)
	}

	glassStateStore(u.hwnd, on, kind, blur, veil)
	log.Info("glass backdrop", "on", on, "kind", kind, "blur", blur, "veil", veil,
		"material", glassMaterialName(kind), "build", build)
	u.inputOnlyDirty = false
	u.overlaySceneReady = false
	u.chromeDirty = true
	win.InvalidateRect(u.hwnd, nil, false)
}

// fillClientBase is the one client underlay. Glass uses stock black (the DWM
// color key) so chroma-key cells, which skip their own fill, stay holes.
// Veil 100 uses near-black that is not the key. One brush at most.
func (u *winUI) fillClientBase(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	if u != nil && glassUsesColorKey(u.cfg) {
		fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))
		return
	}
	r, g, b := chrome.VoidR, chrome.VoidG, chrome.VoidB
	if u != nil && u.shellGlass() {
		r, g, b = 1, 1, 1
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(r, g, b)}
	brush := win.CreateBrushIndirect(&lb)
	if brush == 0 {
		return
	}
	fillRect(hdc, rect, brush)
	win.DeleteObject(win.HGDIOBJ(brush))
}
