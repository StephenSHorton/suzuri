//go:build windows

package ui

import (
	"sync"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

var (
	exitHooksOnce sync.Once
	vehCallback   uintptr
	ctrlCallback  uintptr
)

type winExceptionRecord struct {
	ExceptionCode        uint32
	ExceptionFlags       uint32
	ExceptionRecord      uintptr
	ExceptionAddress     uintptr
	NumberParameters     uint32
	ExceptionInformation [15]uintptr
}

type winExceptionPointers struct {
	ExceptionRecord *winExceptionRecord
	ContextRecord   uintptr
}

// InstallProcessExitHooks leaves a crash-trail line for native deaths that
// skip Go panic / debug.SetCrashOutput (stack overflow in WndProc, AV in
// DWM/GDI, console close). The handler always continues the search so Go
// and WER still see the exception.
func InstallProcessExitHooks() {
	exitHooksOnce.Do(func() {
		installVectoredExceptionTrail()
		installConsoleCtrlTrail()
		log.Info("process exit hooks installed")
	})
}

func installVectoredExceptionTrail() {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("AddVectoredExceptionHandler")
	if err := proc.Find(); err != nil {
		return
	}
	vehCallback = windows.NewCallback(func(info *winExceptionPointers) uintptr {
		if info == nil || info.ExceptionRecord == nil {
			return 0
		}
		code := info.ExceptionRecord.ExceptionCode
		if !fatalWinException(code) {
			return 0
		}
		line := formatNativeException(code)
		applog.WriteRaw(line)
		return 0 // EXCEPTION_CONTINUE_SEARCH
	})
	_, _, _ = proc.Call(1, vehCallback)
}

func installConsoleCtrlTrail() {
	ctrlCallback = windows.NewCallback(func(ctrlType uintptr) uintptr {
		applog.Trail("console-ctrl", "type", uint32(ctrlType))
		applog.WriteRaw([]byte("console-ctrl\n"))
		applog.Sync()
		return 0 // FALSE — not handled; default terminate still runs
	})
	mod := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	if err := mod.Find(); err != nil {
		return
	}
	_, _, _ = mod.Call(ctrlCallback, 1)
}

var (
	procGetGuiResources = windows.NewLazySystemDLL("user32.dll").NewProc("GetGuiResources")
)

const (
	grGDIObjects  = 0
	grUSERObjects = 1
)

func guiObjectCounts() (gdi, user uint32) {
	if err := procGetGuiResources.Find(); err != nil {
		return 0, 0
	}
	p := windows.CurrentProcess()
	r1, _, _ := procGetGuiResources.Call(uintptr(p), grGDIObjects)
	r2, _, _ := procGetGuiResources.Call(uintptr(p), grUSERObjects)
	return uint32(r1), uint32(r2)
}
