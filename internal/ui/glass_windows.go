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
// Pre-PR86 classic (classic/charm-ui) is the recipe that showed the
// desktop on this machine:
//
//   - DWMWA_SYSTEMBACKDROP_TYPE = Mica (blur 0) or Desktop Acrylic (blur > 0)
//   - DwmExtendFrameIntoClientArea(-1) sheet-of-glass
//   - Accent policy OFF on Win11 when veil=0 (accent *replaces* HostBackdrop)
//   - Client holes stay RGB(0,0,0) — the DWM color key
//
// PR86 switched HostBackdrop to None and drove frost through
// ACCENT_ENABLE_ACRYLICBLURBEHIND. extend_ok was true but the desktop
// never showed: without WS_SYSMENU the accent policy often fails or
// paints a dark slab, and a 32-bit backbuffer realized alpha so the
// color key became opaque black. Restore HostBackdrop. Accent is only
// the Win10 fallback and the veil tint (Flags=2 so it is not orange).
// Caption sprites stay suppressed by stripping WS_SYSMENU / MINIMIZEBOX
// / MAXIMIZEBOX. Re-apply backdrop + extend + accent AFTER FRAMECHANGED.
//
// The backbuffer is a 24-bit sys-mem DIB (glassPresentBits): no alpha
// channel, dual-GPU-safe, same present path as caption ink.

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
	dwmwaSystemBackdropType   = 38
	dwmwaColorDefault         = 0xFFFFFFFF

	dwmsbtNone            int32 = 1
	dwmsbtMainWindow      int32 = 2 // Mica
	dwmsbtTransientWindow int32 = 3 // Desktop Acrylic

	win11BackdropBuild = 22621

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
	dwmExtend    = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmExtendFrameIntoClientArea")
	setWinComp   = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowCompositionAttribute")
	procGetPixel = windows.NewLazySystemDLL("gdi32.dll").NewProc("GetPixel")

	glassMu      sync.Mutex
	glassHw      win.HWND
	glassOn      bool
	glassKind    int32
	glassBlur    int
	glassVeil    int
	glassPushing atomic.Bool
	glassDebugN  atomic.Uint32
)

func winBuildNumber() uint32 {
	info := windows.RtlGetVersion()
	if info == nil {
		return 0
	}
	return info.BuildNumber
}

// glassThemeTint is a theme-void wash in AABBGGRR. Flags=2 makes DWM
// honor this instead of the system accent. Never use this RGB as a GDI
// fill — holes must stay 0,0,0.
func glassThemeTint(alpha byte) uint32 {
	r, g, b := glassTintRGB()
	return glassAccentColor(alpha, r, g, b)
}

// glassBackdropType is the DWM HostBackdrop that actually showed the
// desktop on pre-PR86 classic. Solid is none. Glass blur 0 is Mica;
// any positive blur is Desktop Acrylic. SYSTEMBACKDROP None + accent
// was the 63a9568 path that logged OK and stayed opaque.
func glassBackdropType(c config.Config) int32 {
	if c.Backdrop != config.BackdropGlass {
		return dwmsbtNone
	}
	if c.GlassBlur <= 0 {
		return dwmsbtMainWindow
	}
	return dwmsbtTransientWindow
}

// glassUseAccent is true when the legacy acrylic policy should run.
// On Win11 22H2+ with veil 0 it must stay off — accent replaces
// HostBackdrop and was not composing after WS_SYSMENU was stripped.
func glassUseAccent(build uint32, on bool, veil int) bool {
	if !on {
		return false
	}
	if veil > 0 && veil < 100 {
		return true
	}
	return build < win11BackdropBuild
}

func glassAccentFor(build uint32, blur, veil int) (state, flags, color uint32) {
	if veil > 0 && veil < 100 {
		return accentAcrylic, accentFlagUseGradient, glassVeilABGR(veil)
	}
	if build < win11BackdropBuild {
		return accentAcrylic, accentFlagUseGradient, glassThemeTint(glassFrostAlpha(blur, veil))
	}
	return accentDisabled, 0, 0
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

func glassMaterialName(on bool, kind int32, accent bool) string {
	if !on {
		return "none"
	}
	if accent {
		return "accent-acrylic"
	}
	switch kind {
	case dwmsbtMainWindow:
		return "mica"
	case dwmsbtTransientWindow:
		return "desktop-acrylic"
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

func dwmSetInt32(hwnd win.HWND, attr uint32, v int32) bool {
	if hwnd == 0 {
		return false
	}
	err := windows.DwmSetWindowAttribute(windows.HWND(hwnd), attr, unsafe.Pointer(&v), uint32(unsafe.Sizeof(v)))
	return err == nil
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

func extendFrame(hwnd win.HWND, m dwmMargins) bool {
	if hwnd == 0 {
		return false
	}
	if err := dwmExtend.Find(); err != nil {
		log.Warn("DwmExtendFrameIntoClientArea missing", "err", err)
		return false
	}
	hr, _, callErr := dwmExtend.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))
	if hr != 0 {
		log.Warn("DwmExtendFrameIntoClientArea failed",
			"hr", hr, "err", callErr,
			"left", m.Left, "right", m.Right, "top", m.Top, "bottom", m.Bottom)
		return false
	}
	return true
}

// glassFrameMargins is a full sheet of glass while acrylic is on so
// BLACK_BRUSH / default-bg cells color-key through the client (including
// the title strip). Off: 1px bottom keeps the DWM shadow.
func glassFrameMargins(on bool) dwmMargins {
	if on {
		return dwmMargins{Left: -1, Right: -1, Top: -1, Bottom: -1}
	}
	return dwmMargins{Bottom: 1}
}

func setAccent(hwnd win.HWND, state, flags, gradient uint32) (ok bool, ret uintptr) {
	if hwnd == 0 {
		return false, 0
	}
	if err := setWinComp.Find(); err != nil {
		log.Warn("SetWindowCompositionAttribute missing", "err", err)
		return false, 0
	}
	policy := accentPolicy{State: state, Flags: flags, Gradient: gradient}
	data := compositionAttribData{
		Attrib: wcaAccentPolicy,
		data:   unsafe.Pointer(&policy),
		size:   unsafe.Sizeof(policy),
	}
	r1, _, callErr := setWinComp.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
	if r1 == 0 {
		log.Warn("SetWindowCompositionAttribute failed",
			"ret", r1, "err", callErr, "state", state, "flags", flags, "gradient", gradient)
		return false, r1
	}
	return true, r1
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
	// Absolute: no DWM composition API during a modal size/move.
	// Title-bar drag posts wmSuzuriGlassRefresh from WA_CLICKACTIVE;
	// DispatchMessage then runs this on the drag loop and AVs in ntdll.
	if u.inSizeMove {
		u.glassRefreshDeferred = true
		applog.Trail("glass skip", "reason", "sizemove", "msg", fromMsg)
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
	if ok, why := glassAllowDWM(depth, fromMsg, force, same, u.inSizeMove); !ok {
		if why == "sizemove" {
			u.glassRefreshDeferred = true
			applog.Trail("glass skip", "reason", why, "depth", depth, "msg", fromMsg)
			return
		}
		if why == "nested-wndproc" || why == "ncactivate" {
			applog.Trail("glass skip", "reason", why, "depth", depth, "msg", fromMsg)
			u.scheduleGlassRefresh()
		}
		return
	}
	if !glassPushing.CompareAndSwap(false, true) {
		applog.Trail("glass skip", "reason", "reentrant", "depth", depth)
		return
	}
	defer glassPushing.Store(false)

	applyGlassChromeColors(u.hwnd, on)
	dwmSetInt32(u.hwnd, dwmwaWindowCornerPreference, windowCornerPreference())
	// HostBackdrop FIRST (classic). Style bits next (no FRAMECHANGED —
	// that re-enters WndProc). Extend then accent LAST so a later
	// FRAMECHANGED caller can re-enter here and put the sheet back.
	okBack := dwmSetInt32(u.hwnd, dwmwaSystemBackdropType, kind)
	applyFrameChromeStyle(u.hwnd)
	okExt := extendFrame(u.hwnd, glassFrameMargins(on))

	build := winBuildNumber()
	useAccent := glassUseAccent(build, on, veil)
	var okAccent bool
	var accentRet uintptr
	if useAccent {
		state, flags, color := glassAccentFor(build, blur, veil)
		okAccent, accentRet = setAccent(u.hwnd, state, flags, color)
	} else {
		okAccent, accentRet = setAccent(u.hwnd, accentDisabled, 0, 0)
	}

	glassStateStore(u.hwnd, on, kind, blur, veil)
	style := uint32(0)
	if u.hwnd != 0 {
		style = uint32(win.GetWindowLong(u.hwnd, win.GWL_STYLE))
	}
	// Force re-apply after FRAMECHANGED must still log on first apply
	// (registerUI used to swallow the launch line). Skip the chatter on
	// unchanged activate refresh unless SUZURI_GLASS_DEBUG=1.
	if !force || !same || glassDebugOn() {
		log.Info("glass backdrop",
			"on", on, "blur", blur, "veil", veil,
			"material", glassMaterialName(on, kind, useAccent),
			"backdrop", kind, "backdrop_ok", okBack,
			"accent", useAccent, "accent_ok", okAccent, "accent_ret", accentRet,
			"extend_ok", okExt,
			"style", style, "sysmenu", style&styleSysmenu != 0,
			"build", build)
	}
	u.inputOnlyDirty = false
	u.overlaySceneReady = false
	u.chromeDirty = true
	win.InvalidateRect(u.hwnd, nil, false)
}

func samplePixelRGB(hdc win.HDC, x, y int32) (r, g, b byte, ok bool) {
	if hdc == 0 || procGetPixel.Find() != nil {
		return 0, 0, 0, false
	}
	ret, _, _ := procGetPixel.Call(uintptr(hdc), uintptr(x), uintptr(y))
	const clrInvalid = uintptr(0xFFFFFFFF)
	if ret == clrInvalid {
		return 0, 0, 0, false
	}
	return byte(ret), byte(ret >> 8), byte(ret >> 16), true
}

func glassDebugShouldLog() bool {
	if !glassDebugOn() {
		return false
	}
	n := glassDebugN.Add(1)
	return n <= 8 || n%30 == 0
}

func (u *winUI) logGlassPresentSample(hdc win.HDC, rect win.RECT) {
	if u == nil || !glassDebugShouldLog() {
		return
	}
	x := rect.Left + 8
	y := u.shellPadY() + 8
	if y >= rect.Bottom {
		y = rect.Top + 8
	}
	if x >= rect.Right {
		x = rect.Left
	}
	r, g, b, ok := samplePixelRGB(hdc, x, y)
	log.Info("glass debug present",
		"ok", ok, "x", x, "y", y,
		"r", r, "g", g, "b", b,
		"key", ok && r == 0 && g == 0 && b == 0,
		"bits", glassPresentBits,
		"void", []int{int(chrome.VoidR), int(chrome.VoidG), int(chrome.VoidB)},
		"tint", func() []int { tr, tg, tb := glassTintRGB(); return []int{int(tr), int(tg), int(tb)} }(),
		"color_key", u != nil && glassUsesColorKey(u.cfg),
	)
}

// fillClientBase is the one client underlay. Glass uses stock black (the DWM
// color key) so chroma-key cells, which skip their own fill, stay holes.
// Veil 100 uses near-black that is not the key. One brush at most.
// glassTintRGB (12,12,16) is the acrylic/caption wash ONLY — never a GDI fill.
func (u *winUI) fillClientBase(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	if u != nil && glassUsesColorKey(u.cfg) {
		fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))
		if glassDebugOn() {
			r, g, b, ok := samplePixelRGB(hdc, rect.Left+8, rect.Top+8)
			if glassDebugShouldLog() || (ok && (r != 0 || g != 0 || b != 0)) {
				log.Info("glass debug base", "ok", ok, "r", r, "g", g, "b", b,
					"key", ok && r == 0 && g == 0 && b == 0)
			}
		}
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
