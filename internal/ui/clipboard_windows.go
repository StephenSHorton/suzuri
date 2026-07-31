//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

const (
	gmemMoveable = 0x0002
	gmemZeroInit = 0x0040
)

// setClipboardText puts UTF-16 text on the Windows clipboard (CF_UNICODETEXT).
func setClipboardText(hwnd win.HWND, text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	bytes := len(utf16) * 2

	if !win.OpenClipboard(hwnd) {
		return windows.GetLastError()
	}
	defer win.CloseClipboard()
	if !win.EmptyClipboard() {
		return windows.GetLastError()
	}

	h := win.GlobalAlloc(gmemMoveable|gmemZeroInit, uintptr(bytes))
	if h == 0 {
		return windows.GetLastError()
	}
	ptr := win.GlobalLock(h)
	if ptr == nil {
		win.GlobalFree(h)
		return windows.GetLastError()
	}
	mem := unsafe.Slice((*byte)(ptr), bytes)
	src := unsafe.Slice((*byte)(unsafe.Pointer(&utf16[0])), bytes)
	copy(mem, src)
	win.GlobalUnlock(h)

	if win.SetClipboardData(win.CF_UNICODETEXT, win.HANDLE(h)) == 0 {
		win.GlobalFree(h)
		return windows.GetLastError()
	}
	// System owns h after SetClipboardData succeeds.
	return nil
}

// getClipboardText reads UTF-16 text from the clipboard.
func getClipboardText(hwnd win.HWND) (string, error) {
	if !win.OpenClipboard(hwnd) {
		return "", windows.GetLastError()
	}
	defer win.CloseClipboard()

	h := win.GetClipboardData(win.CF_UNICODETEXT)
	if h == 0 {
		return "", nil
	}
	ptr := win.GlobalLock(win.HGLOBAL(uintptr(h)))
	if ptr == nil {
		return "", windows.GetLastError()
	}
	defer win.GlobalUnlock(win.HGLOBAL(uintptr(h)))

	// Decode null-terminated UTF-16
	u16 := (*[1 << 27]uint16)(ptr)
	n := 0
	for u16[n] != 0 {
		n++
		if n > 10_000_000 {
			break
		}
	}
	return syscall.UTF16ToString(u16[:n]), nil
}
