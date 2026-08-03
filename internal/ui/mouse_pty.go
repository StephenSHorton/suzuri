package ui

import (
	"fmt"

	"github.com/hinshun/vt10x"
)

// mouseTracking is true when the app enabled any xterm mouse report mode
// (1000/1002/1003/9). Wheel and clicks should go to the PTY in that case.
func mouseTracking(term vt10x.Terminal) bool {
	if term == nil {
		return false
	}
	return term.Mode()&vt10x.ModeMouseMask != 0
}

func mouseSGR(term vt10x.Terminal) bool {
	if term == nil {
		return false
	}
	return term.Mode()&vt10x.ModeMouseSgr != 0
}

// mouseAnyMotion is CSI ?1003 h — report all pointer moves (needed for hover).
func mouseAnyMotion(term vt10x.Terminal) bool {
	if term == nil {
		return false
	}
	return term.Mode()&vt10x.ModeMouseMany != 0
}

// mouseDragMotion is CSI ?1002 h — report motion while a button is held.
func mouseDragMotion(term vt10x.Terminal) bool {
	if term == nil {
		return false
	}
	return term.Mode()&vt10x.ModeMouseMotion != 0
}

// encodeMouseWheel builds PTY bytes for a wheel gesture on an alt-screen app.
//
// steps > 0: wheel away / "scroll up" (show older content / move list up)
// steps < 0: wheel toward / "scroll down"
//
// col/row are 1-based cell coordinates (xterm mouse protocol).
//
// When the app has mouse tracking on, emits xterm wheel buttons (64/65).
// Otherwise falls back to arrow keys (classic alternate-scroll behavior used
// by most hosts so TUIs like Grok still scroll without mouse mode).
func encodeMouseWheel(term vt10x.Terminal, col, row, steps int) []byte {
	if steps == 0 {
		return nil
	}
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	n := steps
	if n < 0 {
		n = -n
	}
	// Cap so a trackpad fling cannot flood the PTY write queue.
	if n > 32 {
		n = 32
	}

	if mouseTracking(term) && mouseSGR(term) {
		// SGR: button 64 = wheel up, 65 = wheel down.
		btn := 64
		if steps < 0 {
			btn = 65
		}
		out := make([]byte, 0, n*16)
		for i := 0; i < n; i++ {
			out = append(out, []byte(fmt.Sprintf("\x1b[<%d;%d;%dM", btn, col, row))...)
		}
		return out
	}

	// No (or non-SGR) mouse tracking: arrow keys. App-cursor mode when set.
	appCursor := term != nil && term.Mode()&vt10x.ModeAppCursor != 0
	var seq []byte
	if steps > 0 {
		if appCursor {
			seq = []byte("\x1bOA")
		} else {
			seq = []byte("\x1b[A")
		}
	} else {
		if appCursor {
			seq = []byte("\x1bOB")
		} else {
			seq = []byte("\x1b[B")
		}
	}
	out := make([]byte, 0, n*len(seq))
	for i := 0; i < n; i++ {
		out = append(out, seq...)
	}
	return out
}
