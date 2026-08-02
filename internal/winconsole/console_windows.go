//go:build windows

// Package winconsole restores parent-console stdio for CLI subcommands when
// the binary is linked with -H windowsgui (no console of its own).
package winconsole

import (
	"os"

	"golang.org/x/sys/windows"
)

// ATTACH_PARENT_PROCESS attaches to the console of the parent process.
// See https://learn.microsoft.com/windows/console/attachconsole
const attachParentProcess = ^uint32(0) // (DWORD)-1

var (
	modKernel32       = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole = modKernel32.NewProc("AttachConsole")
)

// AttachParent binds stdin/stdout/stderr to the parent's console when the
// process has no usable stdio (typical for -H windowsgui launched from a
// terminal without redirection).
//
// Call only for interactive CLI subcommands (e.g. `version`). Do not call for
// `mcp` — that mode inherits redirected pipes from the host and must keep them.
//
// No-op when:
//   - stdout is already a pipe, file, or console device (keep it)
//   - built without -H windowsgui (already has a console)
//   - double-clicked / no parent console (attach fails)
func AttachParent() {
	if stdoutUsable() {
		return
	}
	r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess))
	if r == 0 {
		return
	}
	// GetStdHandle alone is often still unusable with -H windowsgui; open the
	// console device files so fmt/os writes reach the parent terminal.
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
}

func stdoutUsable() bool {
	h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, err := windows.GetFileType(h)
	if err != nil {
		return false
	}
	switch t {
	case windows.FILE_TYPE_DISK, windows.FILE_TYPE_PIPE, windows.FILE_TYPE_CHAR:
		return true
	default:
		return false
	}
}
