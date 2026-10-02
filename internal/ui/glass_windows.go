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
// DWM has no point radius. Blur 0 selects Mica (no live blur). Any positive
// blur selects Desktop Acrylic. Veil is a black tint on the legacy acrylic
// accent policy, because the system-backdrop attribute has no tint. Veil 100
// is an opaque near-black fill instead of the color key. Rim stays in the
// Mac software painter; GDI text has no alpha glyph mask.

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaSystemBackdropType   = 38

	dwmsbtNone            int32 = 1
	dwmsbtMainWindow      int32 = 2 // Mica
	dwmsbtTransientWindow int32 = 3 // Desktop Acrylic

	win11BackdropBuild = 22621

	wcaAccentPolicy = 19
	accentDisabled  = 0
	accentAcrylic   = 4
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

// glassBackdropType is the DWM material for cfg. Solid is none. Glass with
// blur 0 is Mica; any positive blur is Desktop Acrylic.
func glassBackdropType(c config.Config) int32 {
	if c.Backdrop != config.BackdropGlass {
		return dwmsbtNone
	}
	if c.GlassBlur <= 0 {
		return dwmsbtMainWindow
	}
	return dwmsbtTransientWindow
}

// glassUsesColorKey is true when empty cells must stay pure black so DWM
// shows the backdrop. Veil 100 is an opaque wash, not a hole.
func glassUsesColorKey(c config.Config) bool {
	return c.Backdrop == config.BackdropGlass && c.GlassVeil < 100
}

// glassVeilABGR is a black tint, alpha in the high byte (0–255 from 0–100).
func glassVeilABGR(veil int) uint32 {
	if veil < 0 {
		veil = 0
	}
	if veil > 100 {
		veil = 100
	}
	a := uint32((veil*255 + 50) / 100)
	return a << 24
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

func setAccent(hwnd win.HWND, state, gradient uint32) {
	if err := setWinComp.Find(); err != nil {
		return
	}
	policy := accentPolicy{State: state, Gradient: gradient}
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
		extendFrame(u.hwnd, dwmMargins{})
	}

	build := winBuildNumber()
	switch {
	case !on:
		setAccent(u.hwnd, accentDisabled, 0)
	case veil > 0 && veil < 100:
		// Tint the blur. On 22621+ this can override the system material
		// with legacy acrylic; that is what carries the veil.
		setAccent(u.hwnd, accentAcrylic, glassVeilABGR(veil))
	case build < win11BackdropBuild:
		// No Mica/Acrylic attribute. Legacy acrylic, nearly clear tint.
		setAccent(u.hwnd, accentAcrylic, 1<<24)
	default:
		setAccent(u.hwnd, accentDisabled, 0)
	}

	glassStateStore(u.hwnd, on, kind, blur, veil)
	log.Info("glass backdrop", "on", on, "kind", kind, "blur", blur, "veil", veil, "build", build)
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
