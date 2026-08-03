//go:build darwin

package ui

import (
	"fmt"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hinshun/vt10x"
)

// ptyKeyFromEbiten maps a key press to PTY bytes for alt-screen apps.
// super is the macOS Command key (Kitty "super" modifier).
// kk is the tab's Kitty progressive-enhancement state (may be nil).
func ptyKeyFromEbiten(term vt10x.Terminal, kk *kittyKeyboard, key ebiten.Key, ctrl, shift, alt, super bool) []byte {
	// Bare Alt+letter is left for host. Alt+Enter is an app key (Grok newline).
	// Alt+arrows are pane focus at the host; Ctrl+arrows are word jump (Grok).
	// Still encode Alt+arrows if called (tests / future paths).
	if alt && !ctrl && !super && key != ebiten.KeyEnter &&
		key != ebiten.KeyArrowLeft && key != ebiten.KeyArrowRight &&
		key != ebiten.KeyArrowUp && key != ebiten.KeyArrowDown &&
		key != ebiten.KeyBackspace && key != ebiten.KeyDelete {
		return nil
	}
	appCursor := term != nil && term.Mode()&vt10x.ModeAppCursor != 0

	switch key {
	case ebiten.KeyEnter:
		return encodeEnter(kk, shift, alt, ctrl, super)
	case ebiten.KeyEscape:
		return []byte{0x1b}
	case ebiten.KeyTab:
		if shift {
			return []byte{0x1b, '[', 'Z'}
		}
		return []byte{'\t'}
	case ebiten.KeyBackspace:
		// Option+Backspace → delete word (readline Meta+DEL / many TUIs Alt+BS).
		if alt && !ctrl && !super {
			return []byte{0x1b, 0x7f}
		}
		return []byte{0x7f}
	case ebiten.KeyDelete:
		if alt || ctrl || shift || super {
			// Modified delete: xterm CSI 3 ; mods ~
			mods := kittyMods(shift, alt, ctrl, super)
			return []byte(fmt.Sprintf("\x1b[3;%d~", mods))
		}
		return []byte("\x1b[3~")
	case ebiten.KeyInsert:
		return []byte("\x1b[2~")
	case ebiten.KeyHome:
		if appCursor && !shift && !alt && !ctrl && !super {
			return []byte("\x1bOH")
		}
		if shift || alt || ctrl || super {
			mods := kittyMods(shift, alt, ctrl, super)
			return []byte(fmt.Sprintf("\x1b[1;%dH", mods))
		}
		return []byte("\x1b[H")
	case ebiten.KeyEnd:
		if appCursor && !shift && !alt && !ctrl && !super {
			return []byte("\x1bOF")
		}
		if shift || alt || ctrl || super {
			mods := kittyMods(shift, alt, ctrl, super)
			return []byte(fmt.Sprintf("\x1b[1;%dF", mods))
		}
		return []byte("\x1b[F")
	case ebiten.KeyPageUp:
		return []byte("\x1b[5~")
	case ebiten.KeyPageDown:
		return []byte("\x1b[6~")
	case ebiten.KeyArrowUp: // KeyUp is the same constant
		return encodeArrow(kk, 'A', appCursor, shift, alt, ctrl, super)
	case ebiten.KeyArrowDown:
		return encodeArrow(kk, 'B', appCursor, shift, alt, ctrl, super)
	case ebiten.KeyArrowRight:
		// Cmd+Right → End (macOS text-field convention for apps that don't
		// understand Super+arrow).
		if super && !alt && !ctrl && !shift {
			if appCursor {
				return []byte("\x1bOF")
			}
			return []byte("\x1b[F")
		}
		return encodeArrow(kk, 'C', appCursor, shift, alt, ctrl, super)
	case ebiten.KeyArrowLeft:
		if super && !alt && !ctrl && !shift {
			if appCursor {
				return []byte("\x1bOH")
			}
			return []byte("\x1b[H")
		}
		return encodeArrow(kk, 'D', appCursor, shift, alt, ctrl, super)
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
