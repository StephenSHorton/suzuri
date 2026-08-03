//go:build windows || darwin

package ui

import (
	"fmt"

	"github.com/hinshun/vt10x"
)

// encodeArrow returns PTY bytes for arrow keys with optional modifiers.
// dir is the CSI final letter: A=up B=down C=right D=left.
//
// Bare arrows use app-cursor (SS3) when the app requested it. Modified arrows
// always use CSI 1;mods X (xterm) or Kitty CSI-u when progressive keyboard
// is active — Bubble Tea / Grok use Ctrl+Left (and Alt+Left) for word motion.
func encodeArrow(kk *kittyKeyboard, dir byte, appCursor, shift, alt, ctrl, super bool) []byte {
	if dir != 'A' && dir != 'B' && dir != 'C' && dir != 'D' {
		return nil
	}
	if !shift && !alt && !ctrl && !super {
		if appCursor {
			return []byte{0x1b, 'O', dir}
		}
		return []byte{0x1b, '[', dir}
	}
	mods := kittyMods(shift, alt, ctrl, super)
	if kk != nil && kk.active() {
		// Kitty functional key codes for arrows.
		var code int
		switch dir {
		case 'A':
			code = 57352 // UP
		case 'B':
			code = 57353 // DOWN
		case 'C':
			code = 57351 // RIGHT
		case 'D':
			code = 57350 // LEFT
		}
		return kittyCSIU(code, mods)
	}
	// xterm: CSI 1 ; mods A/B/C/D
	return []byte(fmt.Sprintf("\x1b[1;%d%c", mods, dir))
}

// encodeMouseButton builds an xterm mouse report for button press/release.
// btn: 0=left, 1=middle, 2=right. col/row are 1-based.
// press=false emits SGR release (…m).
func encodeMouseButton(term vt10x.Terminal, col, row, btn int, press bool) []byte {
	if term == nil || !mouseTracking(term) {
		return nil
	}
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	if btn < 0 {
		btn = 0
	}
	if mouseSGR(term) {
		end := byte('M')
		if !press {
			end = 'm'
		}
		return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, col, row, end))
	}
	// X10 / UTF-8 mouse: limited to 223 cols; only press.
	if !press {
		return nil
	}
	cb := byte(32 + btn)
	cx := byte(32 + col)
	cy := byte(32 + row)
	return []byte{0x1b, '[', 'M', cb, cx, cy}
}
