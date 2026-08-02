//go:build darwin

package ui

import (
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hinshun/vt10x"
)

// ptyKeyFromEbiten maps a key press to PTY bytes for alt-screen apps.
func ptyKeyFromEbiten(term vt10x.Terminal, key ebiten.Key, ctrl, shift, alt bool) []byte {
	if alt && !ctrl {
		return nil
	}
	appCursor := term != nil && term.Mode()&vt10x.ModeAppCursor != 0

	switch key {
	case ebiten.KeyEnter:
		return []byte{'\r'}
	case ebiten.KeyEscape:
		return []byte{0x1b}
	case ebiten.KeyTab:
		if shift {
			return []byte{0x1b, '[', 'Z'}
		}
		return []byte{'\t'}
	case ebiten.KeyBackspace:
		return []byte{0x7f}
	case ebiten.KeyDelete:
		return []byte("\x1b[3~")
	case ebiten.KeyInsert:
		return []byte("\x1b[2~")
	case ebiten.KeyHome:
		if appCursor {
			return []byte("\x1bOH")
		}
		return []byte("\x1b[H")
	case ebiten.KeyEnd:
		if appCursor {
			return []byte("\x1bOF")
		}
		return []byte("\x1b[F")
	case ebiten.KeyPageUp:
		return []byte("\x1b[5~")
	case ebiten.KeyPageDown:
		return []byte("\x1b[6~")
	case ebiten.KeyArrowUp:
		if appCursor {
			return []byte("\x1bOA")
		}
		return []byte("\x1b[A")
	case ebiten.KeyArrowDown:
		if appCursor {
			return []byte("\x1bOB")
		}
		return []byte("\x1b[B")
	case ebiten.KeyArrowRight:
		if appCursor {
			return []byte("\x1bOC")
		}
		return []byte("\x1b[C")
	case ebiten.KeyArrowLeft:
		if appCursor {
			return []byte("\x1bOD")
		}
		return []byte("\x1b[D")
	case ebiten.KeyF1:
		return []byte("\x1bOP")
	case ebiten.KeyF2:
		return []byte("\x1bOQ")
	case ebiten.KeyF3:
		return []byte("\x1bOR")
	case ebiten.KeyF4:
		return []byte("\x1bOS")
	case ebiten.KeyF5:
		return []byte("\x1b[15~")
	case ebiten.KeyF6:
		return []byte("\x1b[17~")
	case ebiten.KeyF7:
		return []byte("\x1b[18~")
	case ebiten.KeyF8:
		return []byte("\x1b[19~")
	case ebiten.KeyF9:
		return []byte("\x1b[20~")
	case ebiten.KeyF10:
		return []byte("\x1b[21~")
	case ebiten.KeyF11:
		return []byte("\x1b[23~")
	case ebiten.KeyF12:
		return []byte("\x1b[24~")
	}

	if ctrl && !shift {
		// Ctrl+A..Z
		if key >= ebiten.KeyA && key <= ebiten.KeyZ {
			return []byte{byte(key - ebiten.KeyA + 1)}
		}
		if key == ebiten.KeySpace {
			return []byte{0}
		}
	}
	return nil
}

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
