//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Named mutex: one interactive GUI per user session.
// Override with SUZURI_ALLOW_MULTI=1 for side-by-side testing.
const singleInstanceMutexName = `Local\SuzuriTerminalSingleInstance`

// Held for process lifetime so the mutex is not GC-closed.
var singleInstanceMu windows.Handle

// EnsureSingleInstance returns false when another GUI instance owns the
// mutex (and that window was activated). Returns true when this process
// should continue into ui.Run.
//
// CLI subcommands (mcp, send/receive, version) must not call this — they
// are meant to coexist with a running GUI.
func EnsureSingleInstance() bool {
	if os.Getenv("SUZURI_ALLOW_MULTI") == "1" {
		log.Info("single-instance skipped", "env", "SUZURI_ALLOW_MULTI=1")
		return true
	}
	name, err := windows.UTF16PtrFromString(singleInstanceMutexName)
	if err != nil {
		return true
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		log.Info("another suzuri instance is running; activating it")
		activateExistingSuzuriWindow()
		return false
	}
	if err != nil {
		// Do not block startup if mutex APIs fail.
		log.Warn("single-instance mutex failed; continuing", "err", err)
		return true
	}
	singleInstanceMu = h
	return true
}

func activateExistingSuzuriWindow() {
	cls, err := syscall.UTF16PtrFromString(className)
	if err != nil {
		return
	}
	hwnd := win.FindWindow(cls, nil)
	if hwnd == 0 {
		// Race: mutex held but window not registered yet — nothing to show.
		log.Warn("single-instance: mutex held but no window found")
		return
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	} else {
		win.ShowWindow(hwnd, win.SW_SHOW)
	}
	// AllowSetForegroundWindow can help when focus is stolen; best-effort.
	var pid uint32
	_ = win.GetWindowThreadProcessId(hwnd, &pid)
	if pid != 0 {
		_ = allowSetForegroundWindow(pid)
	}
	if !win.SetForegroundWindow(hwnd) {
		// Fallback: flash the taskbar so the user notices the existing window.
		flashWindow(hwnd)
	}
}

func allowSetForegroundWindow(pid uint32) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("AllowSetForegroundWindow")
	r, _, err := proc.Call(uintptr(pid))
	if r == 0 {
		return err
	}
	return nil
}

func flashWindow(hwnd win.HWND) {
	type flashInfo struct {
		cbSize    uint32
		hwnd      win.HWND
		dwFlags   uint32
		uCount    uint32
		dwTimeout uint32
	}
	const (
		flashwCaption = 0x00000001
		flashwTray    = 0x00000002
		flashwTimer   = 0x00000004
	)
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("FlashWindowEx")
	fi := flashInfo{
		cbSize:  uint32(unsafe.Sizeof(flashInfo{})),
		hwnd:    hwnd,
		dwFlags: flashwCaption | flashwTray | flashwTimer,
		uCount:  3,
	}
	_, _, _ = proc.Call(uintptr(unsafe.Pointer(&fi)))
}
