package aicontrol

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	statsUser32         = windows.NewLazySystemDLL("user32.dll")
	procGetGuiResources = statsUser32.NewProc("GetGuiResources")
	statsKernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procGetHandleCount  = statsKernel32.NewProc("GetProcessHandleCount")
)

const (
	grGDIObjects  = 0
	grUserObjects = 1
)

func addOSStats(out map[string]any) {
	h := uintptr(windows.CurrentProcess())
	gdi, _, _ := procGetGuiResources.Call(h, grGDIObjects)
	user, _, _ := procGetGuiResources.Call(h, grUserObjects)
	out["gdi_handles"] = uint32(gdi)
	out["user_handles"] = uint32(user)
	var count uint32
	if r, _, _ := procGetHandleCount.Call(h, uintptr(unsafe.Pointer(&count))); r != 0 {
		out["kernel_handles"] = count
	}
}
