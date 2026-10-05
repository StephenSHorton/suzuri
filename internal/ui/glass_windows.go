//go:build windows

package ui

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// Windows glass backdrop.
//
// Mac leaves chroma-key shell pixels at alpha 0 and blurs the desktop behind
// them (CGSSetWindowBackgroundBlurRadius, reapplied every turn). This port
// does not use a layered HWND: no UpdateLayeredWindow, no per-pixel alpha,
// no WS_EX_LAYERED. DwmExtendFrameIntoClientArea(-1) is the documented
// non-layered color key — client pixels that stay RGB(0,0,0) show the
// accent acrylic. Chroma-key cells already skip their own fill; the
// glass underlay is stock black so those cells remain the key.
//
// DWM has no point radius. HostBackdrop (DWMWA_SYSTEMBACKDROP_TYPE
// Mica / Acrylic / Tabbed) cannot vary frost continuously and goes
// solid — often a light gray — when the window is inactive. Glass
// therefore uses one material for every slider step:
// ACCENT_ENABLE_ACRYLICBLURBEHIND + AccentFlags=2 + a theme-void
// AABBGGRR tint whose alpha follows blur (and veil). Flags=0 paints
// the system accent (bright orange). SYSTEMBACKDROP stays None so it
// does not fight the accent or swap in the inactive fallback.
// Veil 100 is an opaque near-black fill instead of the color key.

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
	dwmwaSystemBackdropType   = 38
	dwmwaColorDefault         = 0xFFFFFFFF

	dwmsbtNone int32 = 1

	wcaAccentPolicy       = 19
	wcaUseDarkModeColors  = 26
	accentDisabled        = 0
	accentAcrylic         = 4
	accentFlagUseGradient = 2 // honor GradientColor; Flags=0 → system accent
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

	glassMu      sync.Mutex
	glassHw      win.HWND
	glassOn      bool
	glassKind    int32
	glassBlur    int
	glassVeil    int
	glassPushing atomic.Bool
)

func winBuildNumber() uint32 {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0
	}
	return info.BuildNumber
}

// glassThemeTint is a theme-void wash in AABBGGRR. Flags=2 makes DWM
// honor this instead of the system accent.
func glassThemeTint(alpha byte) uint32 {
	return glassAccentColor(alpha, chrome.VoidR, chrome.VoidG, chrome.VoidB)
}

// glassBackdropType is always None. HostBackdrop materials (Mica / Acrylic
// / Tabbed) jump in darkness at the type boundaries and go solid — often
// light — when the window is inactive. Live frost is the accent policy.
func glassBackdropType(c config.Config) int32 {
	return dwmsbtNone
}

// glassAccentForBlur is the one live material: acrylic + Flags=2 + a
// theme-void tint whose alpha follows the slider. Same recipe on Win10
// and Win11 so unfocused glass stays blurred instead of a flat fallback.
func glassAccentForBlur(blur, veil int) (state, flags, color uint32) {
	return accentAcrylic, accentFlagUseGradient, glassThemeTint(glassFrostAlpha(blur, veil))
}

// glassCompositionAccent is what SetWindowCompositionAttribute receives
// while glass is on. HostBackdrop is never stacked on top (that was the
// orange wash when Flags=0, and the white/gray inactive fallback).
func glassCompositionAccent(build uint32, blur, veil int) (state, flags, color uint32) {
	return glassAccentForBlur(blur, veil)
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

func glassMaterialName(on bool) string {
	if on {
		return "accent-acrylic"
	}
	return "none"
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

func dwmSetU32(hwnd win.HWND, attr uint32, v uint32) {
	_ = windows.DwmSetWindowAttribute(windows.HWND(hwnd), attr, unsafe.Pointer(&v), uint32(unsafe.Sizeof(v)))
}

func setCompBool(hwnd win.HWND, attrib uint32, on bool) {
	if err := setWinComp.Find(); err != nil {
		return
	}
	var v int32
	if on {
		v = 1
	}
	data := compositionAttribData{
		Attrib: attrib,
		data:   unsafe.Pointer(&v),
		size:   unsafe.Sizeof(v),
	}
	_, _, _ = setWinComp.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
}

// applyGlassChromeColors keeps any DWM fallback (caption, border, unused
// HostBackdrop) on the theme void. Light OS theme otherwise paints those
// surfaces white/gray when the window is inactive.
func applyGlassChromeColors(hwnd win.HWND, on bool) {
	if hwnd == 0 {
		return
	}
	if !on {
		dwmSetU32(hwnd, dwmwaCaptionColor, dwmwaColorDefault)
		dwmSetU32(hwnd, dwmwaBorderColor, dwmwaColorDefault)
		dwmSetU32(hwnd, dwmwaTextColor, dwmwaColorDefault)
		return
	}
	dwmSetInt32(hwnd, dwmwaUseImmersiveDarkMode, 1)
	setCompBool(hwnd, wcaUseDarkModeColors, true)
	r, g, b := chrome.VoidR, chrome.VoidG, chrome.VoidB
	if r == 0 && g == 0 && b == 0 {
		r, g, b = 12, 12, 16
	}
	cr := uint32(win.RGB(r, g, b))
	dwmSetU32(hwnd, dwmwaCaptionColor, cr)
	dwmSetU32(hwnd, dwmwaBorderColor, cr)
	dwmSetU32(hwnd, dwmwaTextColor, uint32(win.RGB(chrome.TextR, chrome.TextG, chrome.TextB)))
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
	u.pushGlassBackdrop(false, 0)
}

// applyGlassBackdropForced is only for a *posted* refresh on a clean
// message-loop stack (wmSuzuriGlassRefresh). Never call this from
// WM_NCACTIVATE / WM_ACTIVATE — DwmExtend / SetAccent re-enter WndProc.
func (u *winUI) applyGlassBackdropForced() {
	u.pushGlassBackdrop(true, 0)
}

func (u *winUI) pushGlassBackdrop(force bool, fromMsg uint32) {
	if u == nil || u.hwnd == 0 {
		return
	}
	depth := int(uiWatchDepth.Load())
	on := u.shellGlass()
	kind := glassBackdropType(u.cfg)
	blur, veil := 0, 0
	if on {
		blur = u.cfg.GlassBlur
		veil = u.cfg.GlassVeil
	}
	same := glassStateSame(u.hwnd, on, kind, blur, veil)
	if ok, why := glassAllowDWM(depth, fromMsg, force, same); !ok {
		if why == "nested-wndproc" || why == "ncactivate" {
			applog.Trail("glass skip", "reason", why, "depth", depth, "msg", fromMsg)
		}
		return
	}
	if !glassPushing.CompareAndSwap(false, true) {
		applog.Trail("glass skip", "reason", "reentrant", "depth", depth)
		return
	}
	defer glassPushing.Store(false)

	applyGlassChromeColors(u.hwnd, on)
	// Square corners — DWM can forget DONOTROUND when extend/accent change.
	dwmSetInt32(u.hwnd, dwmwaWindowCornerPreference, windowCornerPreference())
	// Always None. A HostBackdrop type is what DWM replaces with a solid
	// (often light) fallback on deactivate.
	dwmSetInt32(u.hwnd, dwmwaSystemBackdropType, dwmsbtNone)
	if on {
		extendFrame(u.hwnd, dwmMargins{-1, -1, -1, -1})
	} else {
		// 1px extend keeps the DWM shadow and stops the native caption
		// from painting over a FrameWindows client that already ate it.
		extendFrame(u.hwnd, dwmMargins{Bottom: 1})
	}

	if on {
		state, flags, color := glassCompositionAccent(winBuildNumber(), blur, veil)
		setAccent(u.hwnd, state, flags, color)
	} else {
		setAccent(u.hwnd, accentDisabled, 0, 0)
	}

	glassStateStore(u.hwnd, on, kind, blur, veil)
	if force {
		return
	}
	log.Info("glass backdrop", "on", on, "blur", blur, "veil", veil,
		"material", glassMaterialName(on), "frost", glassFrostAlpha(blur, veil))
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
