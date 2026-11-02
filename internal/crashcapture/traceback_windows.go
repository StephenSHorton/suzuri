//go:build windows

package crashcapture

import (
	"runtime/debug"

	"golang.org/x/sys/windows"
)

// SetupTraceback makes native faults reach Windows Error Reporting.
//
// By default Go's last-chance exception handler prints the fault and calls
// exit(2), so WER never sees an access violation in ConPTY or GDI and
// LocalDumps writes nothing. "wer" (Go 1.21+) prints the traceback, then
// re-raises the exception with RaiseFailFastException: WER writes the dump
// and the process exit code is the original NTSTATUS (0xC0000005). It also
// sets WER_FAULT_REPORTING_NO_UI, so no "stopped working" dialog.
// It covers dev and release builds: without it there is never a dump.
func SetupTraceback(version string) {
	debug.SetTraceback("wer")
}

// nativeAccessViolation writes through a bad pointer inside ntdll (not Go
// code), the same class of fault as a ConPTY crash.
func nativeAccessViolation() {
	p := windows.NewLazySystemDLL("ntdll.dll").NewProc("memset")
	_, _, _ = p.Call(0x10, 0, 64)
}
