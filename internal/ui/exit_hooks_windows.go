//go:build windows

package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/config"
)

var (
	exitHooksOnce sync.Once
	vehCallback   uintptr
	uefCallback   uintptr
	ctrlCallback  uintptr

	vehNote   [512]byte
	vehStack  [32 << 10]byte
	vehFrames [16]uintptr
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
// DWM/GDI, console close). VEH + UEF always continue-search so WER still
// writes a LocalDumps minidump.
func InstallProcessExitHooks() {
	exitHooksOnce.Do(func() {
		enableLocalDumps()
		installVectoredExceptionTrail()
		installUnhandledExceptionTrail()
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
		writeFatalException(info)
		return 0 // EXCEPTION_CONTINUE_SEARCH
	})
	_, _, _ = proc.Call(1, vehCallback)
}

func installUnhandledExceptionTrail() {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetUnhandledExceptionFilter")
	if err := proc.Find(); err != nil {
		return
	}
	uefCallback = windows.NewCallback(func(info *winExceptionPointers) uintptr {
		writeFatalException(info)
		return 0 // EXCEPTION_CONTINUE_SEARCH — WER LocalDumps still runs
	})
	_, _, _ = proc.Call(uefCallback)
}

func writeFatalException(info *winExceptionPointers) {
	if info == nil || info.ExceptionRecord == nil {
		return
	}
	rec := info.ExceptionRecord
	if !fatalWinException(rec.ExceptionCode) {
		return
	}
	mod := moduleAt(rec.ExceptionAddress)
	line := formatExceptionDetail(rec.ExceptionCode, rec.ExceptionAddress, mod)
	n := copy(vehNote[:], line)
	applog.WriteRaw(vehNote[:n])
	if rec.ExceptionCode == 0xC0000005 && rec.NumberParameters >= 2 {
		applog.WriteRaw(formatAVInfo(rec.ExceptionInformation[0], rec.ExceptionInformation[1]))
	}
	if rec.ExceptionCode != 0xC00000FD {
		if frames := captureNativeFrames(); len(frames) > 0 {
			applog.WriteRaw(formatNativeFrames(frames))
		}
		ns := runtime.Stack(vehStack[:], false)
		applog.WriteRaw(vehStack[:ns])
	}
	applog.Sync()
}

func captureNativeFrames() []uintptr {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("RtlCaptureStackBackTrace")
	if err := proc.Find(); err != nil {
		return nil
	}
	n, _, _ := proc.Call(2, 16, uintptr(unsafe.Pointer(&vehFrames[0])), 0)
	if n == 0 {
		return nil
	}
	if n > 16 {
		n = 16
	}
	return vehFrames[:n]
}

func moduleAt(addr uintptr) string {
	if addr == 0 {
		return ""
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleExW")
	if err := proc.Find(); err != nil {
		return ""
	}
	const flags = 0x4 | 0x2 // FROM_ADDRESS | UNCHANGED_REFCOUNT
	var h windows.Handle
	r, _, _ := proc.Call(flags, addr, uintptr(unsafe.Pointer(&h)))
	if r == 0 || h == 0 {
		return ""
	}
	var buf [windows.MAX_PATH]uint16
	n, err := windows.GetModuleFileName(h, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

func installConsoleCtrlTrail() {
	ctrlCallback = windows.NewCallback(func(ctrlType uintptr) uintptr {
		applog.Trail("console-ctrl", "type", uint32(ctrlType))
		applog.WriteRaw([]byte("console-ctrl\n"))
		applog.Sync()
		return 0
	})
	mod := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")
	if err := mod.Find(); err != nil {
		return
	}
	_, _, _ = mod.Call(ctrlCallback, 1)
}

func enableLocalDumps() {
	dir := filepath.Join(config.Dir(), "CrashDumps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	names := []string{"suzuri.exe"}
	if exe := filepath.Base(os.Args[0]); exe != "" && exe != "suzuri.exe" {
		names = append(names, exe)
	}
	for _, name := range names {
		keyPath := `Software\Microsoft\Windows\Windows Error Reporting\LocalDumps\` + name
		k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
		if err != nil {
			log.Warn("WER LocalDumps key", "exe", name, "err", err)
			continue
		}
		_ = k.SetStringValue("DumpFolder", dir)
		_ = k.SetDWordValue("DumpType", 2) // full dump (1 = mini)
		_ = k.SetDWordValue("DumpCount", 5)
		_ = k.Close()
	}
	log.Info("WER LocalDumps enabled", "dir", dir, "keep", 5)
}

var (
	procGetGuiResources     = windows.NewLazySystemDLL("user32.dll").NewProc("GetGuiResources")
	procGetProcessHandleCnt = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
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

func processHandleCount() uint32 {
	if err := procGetProcessHandleCnt.Find(); err != nil {
		return 0
	}
	var n uint32
	p := windows.CurrentProcess()
	r, _, _ := procGetProcessHandleCnt.Call(uintptr(p), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return 0
	}
	return n
}
