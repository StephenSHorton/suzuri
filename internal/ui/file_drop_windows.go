//go:build windows

package ui

import (
	"syscall"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// setWindowFileDropAccept toggles classic CF_HDROP acceptance.
// Only enable while the Send-file prompt is open so drops are not claimed
// for transfer at other times (OS shows "no drop" / other windows can take them).
func setWindowFileDropAccept(hwnd win.HWND, accept bool) {
	if hwnd == 0 {
		return
	}
	win.DragAcceptFiles(hwnd, accept)
}

func pathsFromHDROP(hDrop win.HDROP) []string {
	if hDrop == 0 {
		return nil
	}
	// Count
	n := win.DragQueryFile(hDrop, 0xFFFFFFFF, nil, 0)
	if n == 0 {
		dragFinish(hDrop)
		return nil
	}
	out := make([]string, 0, n)
	var buf [windows.MAX_PATH + 1]uint16
	for i := uint(0); i < n; i++ {
		// Query length
		cch := win.DragQueryFile(hDrop, i, nil, 0)
		if cch == 0 {
			continue
		}
		if cch >= uint(len(buf)) {
			// Oversized path — allocate
			big := make([]uint16, cch+1)
			if win.DragQueryFile(hDrop, i, &big[0], cch+1) == 0 {
				continue
			}
			out = append(out, syscall.UTF16ToString(big))
			continue
		}
		if win.DragQueryFile(hDrop, i, &buf[0], uint(len(buf))) == 0 {
			continue
		}
		out = append(out, syscall.UTF16ToString(buf[:]))
	}
	dragFinish(hDrop)
	return out
}

// lxn/win DragFinish incorrectly calls DragAcceptFiles; call shell32 properly.
func dragFinish(hDrop win.HDROP) {
	mod := windows.NewLazySystemDLL("shell32.dll")
	proc := mod.NewProc("DragFinish")
	_, _, _ = proc.Call(uintptr(hDrop))
}

