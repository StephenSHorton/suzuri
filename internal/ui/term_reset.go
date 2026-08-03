package ui

import (
	"github.com/hinshun/vt10x"
)

// hostResetMouseModes feeds CSI sequences into the host VT so mouse tracking
// bits clear even when the app was killed without sending ?1003l etc.
// Without this, suzuri keeps injecting SGR motion into the PTY and the shell
// prints garbage like "35;12;8M35;13;8M…".
var hostResetMouseSeq = []byte(
	"\x1b[?1000l" + // X10 / basic
		"\x1b[?1002l" + // button-event
		"\x1b[?1003l" + // any-event
		"\x1b[?1005l" + // utf-8
		"\x1b[?1006l" + // SGR
		"\x1b[?1015l", // urxvt
)

// resetHostMouseModes clears mouse-related modes on the host-side VT parser.
// Does not write to the PTY (the child is gone or confused).
func resetHostMouseModes(term vt10x.Terminal) {
	if term == nil {
		return
	}
	_, _ = term.Write(hostResetMouseSeq)
}

// resetHostAfterAltApp also drops app-cursor / focus if left armed.
func resetHostAfterAltApp(term vt10x.Terminal) {
	if term == nil {
		return
	}
	resetHostMouseModes(term)
	// App cursor keys, focus reporting — common leftovers after a hard kill.
	_, _ = term.Write([]byte("\x1b[?1l\x1b[?1004l\x1b[?2004l\x1b[?25h"))
}
