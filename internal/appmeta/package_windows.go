//go:build windows

package appmeta

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	packageOnce sync.Once
	packageFull string
	isPackaged  bool
)

// IsPackaged reports whether this process is running inside an MSIX/Store
// package identity (Desktop Bridge / packaged classic app).
//
// Store builds must not self-update from GitHub Releases — the Store owns
// the install path and re-signs packages.
func IsPackaged() bool {
	packageOnce.Do(detectPackage)
	return isPackaged
}

// PackageFullName returns the package full name when packaged, else "".
func PackageFullName() string {
	packageOnce.Do(detectPackage)
	return packageFull
}

func detectPackage() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetCurrentPackageFullName")
	if err := proc.Find(); err != nil {
		return
	}

	// First call: length query. Packaged apps return ERROR_INSUFFICIENT_BUFFER (122).
	var n uint32
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&n)), 0)
	const (
		errorSuccess            = 0
		errorInsufficientBuffer = 122
		appModelErrorNoPackage  = 15700 // APPMODEL_ERROR_NO_PACKAGE
	)
	if r == appModelErrorNoPackage || n == 0 {
		return
	}
	if r != errorInsufficientBuffer && r != errorSuccess {
		return
	}

	buf := make([]uint16, n)
	r, _, _ = proc.Call(uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&buf[0])))
	if r != errorSuccess {
		return
	}
	name := syscall.UTF16ToString(buf)
	if name == "" {
		return
	}
	isPackaged = true
	packageFull = name
}
