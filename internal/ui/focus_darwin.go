//go:build darwin

package ui

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>

void suzuri_reclaim_focus(void);
int suzuri_clipboard_png_write(const char *path);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// reclaimWindowFocus activates the suzuri NSApplication so keyboard input
// returns after overlay dismiss or clipboard work (can leave the ebiten/GLFW
// window without key focus — user had to alt-tab).
//
// Safe to call from the ebiten UI thread only.
func reclaimWindowFocus() {
	C.suzuri_reclaim_focus()
}

// writeClipboardPNGNative dumps the general pasteboard image to path via
// NSPasteboard (in-process). Returns true if a PNG was written.
func writeClipboardPNGNative(path string) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("empty path")
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	switch C.suzuri_clipboard_png_write(cpath) {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, fmt.Errorf("NSPasteboard PNG write failed")
	}
}
