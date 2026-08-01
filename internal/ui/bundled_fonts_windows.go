//go:build windows

package ui

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/assets"
)

// BundledFace is the GDI face name for the embedded monospaced font.
const BundledFace = assets.FontFaceBundled

var (
	modGdi32Font           = syscall.NewLazyDLL("gdi32.dll")
	procAddFontMemResource = modGdi32Font.NewProc("AddFontMemResourceEx")
	procRemFontMemResource = modGdi32Font.NewProc("RemoveFontMemResourceEx")

	bundledMu      sync.Mutex
	bundledHandles []syscall.Handle
	bundledOK      bool
)

// RegisterBundledFonts loads the embedded TTF into the process font table
// (private — not installed system-wide). Safe to call once at startup.
func RegisterBundledFonts() bool {
	bundledMu.Lock()
	defer bundledMu.Unlock()
	if bundledOK {
		return true
	}
	if procAddFontMemResource.Find() != nil {
		log.Warn("AddFontMemResourceEx unavailable — bundled font disabled")
		return false
	}
	data := assets.BundledFontRegular
	if len(data) == 0 {
		log.Warn("bundled font embed empty")
		return false
	}
	var nFonts uint32
	h, _, err := procAddFontMemResource.Call(
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0,
		uintptr(unsafe.Pointer(&nFonts)),
	)
	if h == 0 {
		log.Warn("bundled font register failed", "err", err)
		return false
	}
	bundledHandles = append(bundledHandles, syscall.Handle(h))
	bundledOK = true
	log.Info("bundled font registered", "face", BundledFace, "fonts", nFonts, "bytes", len(data))
	return true
}

// UnregisterBundledFonts releases private font resources (call on exit).
func UnregisterBundledFonts() {
	bundledMu.Lock()
	defer bundledMu.Unlock()
	if procRemFontMemResource.Find() != nil {
		bundledHandles = nil
		bundledOK = false
		return
	}
	for _, h := range bundledHandles {
		_, _, _ = procRemFontMemResource.Call(uintptr(h))
	}
	bundledHandles = nil
	bundledOK = false
}

// BundledFontReady is true after a successful RegisterBundledFonts.
func BundledFontReady() bool {
	bundledMu.Lock()
	defer bundledMu.Unlock()
	return bundledOK
}
