//go:build windows

package ui

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/assets"
)

// Win32 WM_SETICON wParam values (not always exported by lxn/win).
const (
	iconSmall = 0 // ICON_SMALL — title bar / Alt-Tab small
	iconBig   = 1 // ICON_BIG   — taskbar / Alt-Tab large
)

// appIconID is the RT_GROUP_ICON id produced by akavel/rsrc (first -ico file).
const appIconID = 1

var (
	iconOnce   sync.Once
	iconBigH   win.HICON
	iconSmallH win.HICON
	iconOwned  bool // true if we must DestroyIcon on teardown
)

// loadAppIcons returns large + small HICONs for the glowy 硯 mark.
// Prefers the PE resource linked via rsrc_windows_*.syso; falls back to the
// embedded .ico written to a temp file once per process.
func loadAppIcons(hInst win.HINSTANCE) (big, small win.HICON) {
	iconOnce.Do(func() {
		if loadIconsFromResource(hInst) {
			return
		}
		if loadIconsFromEmbedded() {
			return
		}
		log.Warn("app icon unavailable (no PE resource and embed empty)")
	})
	return iconBigH, iconSmallH
}

func loadIconsFromResource(hInst win.HINSTANCE) bool {
	if hInst == 0 {
		return false
	}
	cxBig := win.GetSystemMetrics(win.SM_CXICON)
	cyBig := win.GetSystemMetrics(win.SM_CYICON)
	cxSm := win.GetSystemMetrics(win.SM_CXSMICON)
	cySm := win.GetSystemMetrics(win.SM_CYSMICON)
	name := win.MAKEINTRESOURCE(appIconID)

	big := win.HICON(win.LoadImage(hInst, name, win.IMAGE_ICON, cxBig, cyBig, win.LR_DEFAULTCOLOR))
	if big == 0 {
		// Probe with default size — some builds only have one size.
		big = win.HICON(win.LoadImage(hInst, name, win.IMAGE_ICON, 0, 0, win.LR_DEFAULTSIZE|win.LR_DEFAULTCOLOR))
	}
	if big == 0 {
		return false
	}
	small := win.HICON(win.LoadImage(hInst, name, win.IMAGE_ICON, cxSm, cySm, win.LR_DEFAULTCOLOR))
	if small == 0 {
		small = big
	}
	iconBigH, iconSmallH = big, small
	iconOwned = true
	log.Info("app icon loaded from PE resource", "id", appIconID)
	return true
}

func loadIconsFromEmbedded() bool {
	data := assets.AppIconICO
	if len(data) == 0 {
		return false
	}
	dir := filepath.Join(os.TempDir(), "suzuri")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "suzuri.ico")
	// Rewrite only when missing or size differs (dev rebuilds).
	if st, err := os.Stat(path); err != nil || st.Size() != int64(len(data)) {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			log.Warn("app icon temp write failed", "err", err)
			return false
		}
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	cxBig := win.GetSystemMetrics(win.SM_CXICON)
	cyBig := win.GetSystemMetrics(win.SM_CYICON)
	cxSm := win.GetSystemMetrics(win.SM_CXSMICON)
	cySm := win.GetSystemMetrics(win.SM_CYSMICON)

	// hinst=0 + LR_LOADFROMFILE loads from the path in lpszName.
	big := win.HICON(win.LoadImage(0, p, win.IMAGE_ICON, cxBig, cyBig, win.LR_LOADFROMFILE|win.LR_DEFAULTCOLOR))
	if big == 0 {
		big = win.HICON(win.LoadImage(0, p, win.IMAGE_ICON, 0, 0, win.LR_LOADFROMFILE|win.LR_DEFAULTSIZE|win.LR_DEFAULTCOLOR))
	}
	if big == 0 {
		log.Warn("app icon LoadImage from file failed", "path", path)
		return false
	}
	small := win.HICON(win.LoadImage(0, p, win.IMAGE_ICON, cxSm, cySm, win.LR_LOADFROMFILE|win.LR_DEFAULTCOLOR))
	if small == 0 {
		small = big
	}
	iconBigH, iconSmallH = big, small
	iconOwned = true
	log.Info("app icon loaded from embedded ico", "path", path, "bytes", len(data))
	return true
}

// releaseAppIcons destroys privately-owned icon handles (call on exit).
func releaseAppIcons() {
	if !iconOwned {
		return
	}
	if iconSmallH != 0 && iconSmallH != iconBigH {
		win.DestroyIcon(iconSmallH)
	}
	if iconBigH != 0 {
		win.DestroyIcon(iconBigH)
	}
	iconBigH, iconSmallH = 0, 0
	iconOwned = false
}

// applyWindowIcons sets class defaults (via return values) and WM_SETICON on hwnd.
func applyWindowIcons(hwnd win.HWND, big, small win.HICON) {
	if hwnd == 0 {
		return
	}
	if big != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, iconBig, uintptr(big))
	}
	if small != 0 {
		win.SendMessage(hwnd, win.WM_SETICON, iconSmall, uintptr(small))
	}
}
