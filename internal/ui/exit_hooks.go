//go:build windows || darwin

package ui

// WndProc / DWM / process-exit policy shared by Windows and tests.
// Native stack overflow from a recursive WndProc never reaches Go's
// recover or debug.SetCrashOutput — these guards exist to stop that
// recursion and to leave a trail if something still kills the process.

const (
	wmActivate   = 0x0006
	wmNCActivate = 0x0086

	// maxWndProcDepth is well below the native callback stack. DwmExtend /
	// SetWindowCompositionAttribute / DwmSetWindowAttribute send
	// WM_NCACTIVATE / WM_NCCALCSIZE / WM_PAINT synchronously. Past this
	// we stop handling and return DefWindowProc so we do not overflow.
	maxWndProcDepth = 12

	gdiObjectWarn  = 8000 // process limit is 10_000; warn before the cliff
	userObjectWarn = 8000
)

// glassAllowDWM is whether a DWM composition call may run on this stack.
// Nested WndProc and WM_NCACTIVATE must never touch DwmExtend / SetAccent /
// DwmSetWindowAttribute — those APIs re-enter WndProc and were the silent
// death on de25fe7 (no Go panic, no WER, no crash file).
func glassAllowDWM(wndProcDepth int, msg uint32, force, stateSame bool) (ok bool, why string) {
	if wndProcDepth > 1 {
		return false, "nested-wndproc"
	}
	if msg == wmNCActivate {
		return false, "ncactivate"
	}
	if stateSame && !force {
		return false, "unchanged"
	}
	if stateSame && msg != wmActivate && msg != 0 {
		return false, "unchanged"
	}
	return true, "apply"
}

func wndProcShouldAbort(depth int32) bool {
	return depth > maxWndProcDepth
}

// fatalWinException is a first-chance code that actually kills the process.
// OutputDebugString / SetThreadName / C++ EH / breakpoints are ignored so
// we do not fight the Go runtime's own VEH use.
func fatalWinException(code uint32) bool {
	switch code {
	case 0xC0000005, // EXCEPTION_ACCESS_VIOLATION
		0xC00000FD, // EXCEPTION_STACK_OVERFLOW
		0xC0000374, // STATUS_HEAP_CORRUPTION
		0xC0000409, // STATUS_STACK_BUFFER_OVERRUN / fastfail
		0xC000001D, // EXCEPTION_ILLEGAL_INSTRUCTION
		0xC0000094, // EXCEPTION_INT_DIVIDE_BY_ZERO
		0xC0000096, // EXCEPTION_PRIV_INSTRUCTION
		0xC0000025: // EXCEPTION_NONCONTINUABLE_EXCEPTION
		return true
	}
	return false
}

func formatNativeException(code uint32) []byte {
	return formatExceptionDetail(code, 0, "")
}

func formatExceptionDetail(code uint32, addr uintptr, module string) []byte {
	var buf [384]byte
	n := copy(buf[:], "native-exception code=0x")
	n = appendHex32(buf[:], n, code)
	if addr != 0 {
		n += copy(buf[n:], " addr=0x")
		n = appendHexPtr(buf[:], n, addr)
	}
	if module != "" {
		n += copy(buf[n:], " module=")
		n += copy(buf[n:], module)
	}
	buf[n] = '\n'
	return buf[:n+1]
}

func appendHex32(buf []byte, n int, v uint32) int {
	const hexdigits = "0123456789ABCDEF"
	for i := 7; i >= 0; i-- {
		buf[n] = hexdigits[(v>>(i*4))&0xF]
		n++
	}
	return n
}

func appendHexPtr(buf []byte, n int, v uintptr) int {
	const hexdigits = "0123456789ABCDEF"
	for i := 15; i >= 0; i-- {
		if n >= len(buf) {
			return n
		}
		buf[n] = hexdigits[(v>>uint(i*4))&0xF]
		n++
	}
	return n
}
