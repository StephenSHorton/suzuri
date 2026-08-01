//go:build windows

package ui

import (
	"unicode/utf8"

	"github.com/hinshun/vt10x"
	"github.com/lxn/win"
)

// ptyKeyFromWin maps a WM_KEYDOWN to bytes for ConPTY when a full-screen
// (alt-screen) app owns the keyboard. Host chrome shortcuts are handled first
// by the caller; this only covers app-bound keys.
func ptyKeyFromWin(term vt10x.Terminal, wParam uintptr, ctrl, shift, alt bool) []byte {
	// Leave Alt+… alone (menu / system); bare keys only.
	if alt && !ctrl {
		return nil
	}
	appCursor := term != nil && term.Mode()&vt10x.ModeAppCursor != 0

	switch wParam {
	case win.VK_RETURN:
		return []byte{'\r'}
	case win.VK_ESCAPE:
		return []byte{0x1b}
	case win.VK_TAB:
		if shift {
			return []byte{0x1b, '[', 'Z'} // back-tab
		}
		return []byte{'\t'}
	case win.VK_BACK:
		return []byte{0x7f}
	case win.VK_DELETE:
		return []byte("\x1b[3~")
	case win.VK_INSERT:
		return []byte("\x1b[2~")
	case win.VK_HOME:
		if appCursor {
			return []byte("\x1bOH")
		}
		return []byte("\x1b[H")
	case win.VK_END:
		if appCursor {
			return []byte("\x1bOF")
		}
		return []byte("\x1b[F")
	case win.VK_PRIOR: // Page Up
		return []byte("\x1b[5~")
	case win.VK_NEXT: // Page Down
		return []byte("\x1b[6~")
	case win.VK_UP:
		if appCursor {
			return []byte("\x1bOA")
		}
		return []byte("\x1b[A")
	case win.VK_DOWN:
		if appCursor {
			return []byte("\x1bOB")
		}
		return []byte("\x1b[B")
	case win.VK_RIGHT:
		if appCursor {
			return []byte("\x1bOC")
		}
		return []byte("\x1b[C")
	case win.VK_LEFT:
		if appCursor {
			return []byte("\x1bOD")
		}
		return []byte("\x1b[D")
	case win.VK_F1:
		return []byte("\x1bOP")
	case win.VK_F2:
		return []byte("\x1bOQ")
	case win.VK_F3:
		return []byte("\x1bOR")
	case win.VK_F4:
		return []byte("\x1bOS")
	case win.VK_F5:
		return []byte("\x1b[15~")
	case win.VK_F6:
		return []byte("\x1b[17~")
	case win.VK_F7:
		return []byte("\x1b[18~")
	case win.VK_F8:
		return []byte("\x1b[19~")
	case win.VK_F9:
		return []byte("\x1b[20~")
	case win.VK_F10:
		return []byte("\x1b[21~")
	case win.VK_F11:
		return []byte("\x1b[23~")
	case win.VK_F12:
		return []byte("\x1b[24~")
	}

	// Ctrl+letter → C0 control (Ctrl+A=1 … Ctrl+Z=26).
	if ctrl && !shift {
		if wParam >= 'A' && wParam <= 'Z' {
			return []byte{byte(wParam - 'A' + 1)}
		}
		if wParam >= 'a' && wParam <= 'z' {
			return []byte{byte(wParam - 'a' + 1)}
		}
		// Common controls already covered as letter; Ctrl+Space → NUL
		if wParam == win.VK_SPACE {
			return []byte{0}
		}
	}
	return nil
}

// ptyRuneUTF8 encodes a printable WM_CHAR rune for the PTY.
func ptyRuneUTF8(r rune) []byte {
	if r < 32 && r != '\t' {
		return nil
	}
	if r == 0x7f {
		return []byte{0x7f}
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	return buf[:n]
}
