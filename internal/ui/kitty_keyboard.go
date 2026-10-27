//go:build windows || darwin

package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// Kitty keyboard progressive-enhancement flags (CSI = / CSI > / CSI ? u).
// Spec: https://sw.kovidgoyal.net/kitty/keyboard-protocol/
const (
	kittyDisambiguate     = 1  // 0b1
	kittyEventTypes       = 2  // 0b10
	kittyAlternateKeys    = 4  // 0b100
	kittyAllKeysAsEscapes = 8  // 0b1000
	kittyAssociatedText   = 16 // 0b10000
)

const kittyStackMax = 16

// kittyKeyboard tracks the progressive-enhancement flags the app requested.
// Without this, Shift+Enter collapses to plain Enter (legacy C0 table).
//
// The spec keeps separate flags and stacks for the main and alternate
// screens. flags/stack are the live screen's; main holds the main screen's
// while the alt screen is up.
type kittyKeyboard struct {
	flags int
	stack []int
	onAlt bool
	main  struct {
		flags int
		stack []int
	}
	// scan carries consumeHostQueries across PTY reads.
	scan hostScan
}

// hostScan is the host-query scanner's state between reads.
type hostScan struct {
	// str is the introducer of the string body we are inside
	// (']' OSC, 'P' DCS, '_' APC, '^' PM, 'X' SOS), or 0.
	str byte
	// tail is an escape cut off at the end of the last read (a lone ESC or
	// an unfinished CSI), scanned again with the next read.
	tail []byte
}

// hostScanTailMax bounds a held unfinished CSI; longer ones are dropped.
const hostScanTailMax = 32

// setAltScreen switches to the other screen's flags (CSI ? 1049/1047/47 h/l).
func (k *kittyKeyboard) setAltScreen(on bool) {
	if k == nil || k.onAlt == on {
		return
	}
	k.onAlt = on
	if on {
		k.main.flags, k.main.stack = k.flags, k.stack
		k.flags, k.stack = 0, nil
		return
	}
	k.flags, k.stack = k.main.flags, k.main.stack
	k.main.flags, k.main.stack = 0, nil
}

// reset drops every flag and stack on both screens (RIS).
func (k *kittyKeyboard) reset() {
	if k == nil {
		return
	}
	scan := k.scan
	*k = kittyKeyboard{scan: scan}
}

// active is true when the app has enabled disambiguation or full key reporting.
func (k *kittyKeyboard) active() bool {
	if k == nil {
		return false
	}
	return k.flags&(kittyDisambiguate|kittyAllKeysAsEscapes) != 0
}

func (k *kittyKeyboard) apply(flags, mode int) {
	if k == nil {
		return
	}
	if mode < 1 {
		mode = 1
	}
	switch mode {
	case 2: // set bits in flags, leave others
		k.flags |= flags
	case 3: // clear bits in flags
		k.flags &^= flags
	default: // replace
		k.flags = flags
	}
}

func (k *kittyKeyboard) push(flags int) {
	if k == nil {
		return
	}
	if len(k.stack) >= kittyStackMax {
		k.stack = k.stack[1:]
	}
	k.stack = append(k.stack, k.flags)
	k.flags = flags
}

func (k *kittyKeyboard) pop(n int) {
	if k == nil {
		return
	}
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if len(k.stack) == 0 {
			k.flags = 0
			return
		}
		k.flags = k.stack[len(k.stack)-1]
		k.stack = k.stack[:len(k.stack)-1]
	}
}

// kittyMods encodes shift/alt/ctrl/super as (1 + bitfield) per the protocol.
func kittyMods(shift, alt, ctrl, super bool) int {
	m := 0
	if shift {
		m |= 1
	}
	if alt {
		m |= 2
	}
	if ctrl {
		m |= 4
	}
	if super {
		m |= 8
	}
	return 1 + m
}

// kittyCSIU encodes a functional/unicode key as CSI key ; mods u.
func kittyCSIU(key, mods int) []byte {
	if mods <= 1 {
		return []byte(fmt.Sprintf("\x1b[%du", key))
	}
	return []byte(fmt.Sprintf("\x1b[%d;%du", key, mods))
}

// encodeKittyChar emits CSI-u for a unicode code point with modifiers.
// Used for Ctrl/Cmd+Shift+letter and Ctrl+punctuation that have no classic C0.
// Always CSI-u (Grok enables Kitty progressive enhancement; legacy terminals
// ignore unknown CSI-u and lose the chord either way).
func encodeKittyChar(ch rune, shift, alt, ctrl, super bool) []byte {
	if ch < 0 || ch > 0x10ffff {
		return nil
	}
	mods := kittyMods(shift, alt, ctrl, super)
	if mods <= 1 {
		// Bare character should go through rune path, not CSI-u.
		return nil
	}
	return kittyCSIU(int(ch), mods)
}

// encodeEnter returns PTY bytes for Enter with modifiers.
//
// When the Kitty protocol is active, modified Enter uses CSI-u so Grok and
// other modern TUIs can treat Shift+Enter / Alt+Enter / Cmd+Enter as newline.
// Without negotiation: Alt+Enter is legacy ESC CR (Grok fallback); Shift and
// Super still emit CSI-u so apps that parse it without a flags push work.
func encodeEnter(kk *kittyKeyboard, shift, alt, ctrl, super bool) []byte {
	if !shift && !alt && !ctrl && !super {
		return []byte{'\r'}
	}
	mods := kittyMods(shift, alt, ctrl, super)
	if kk != nil && kk.active() {
		return kittyCSIU(13, mods)
	}
	// Legacy Alt+Enter (Grok doctor fallback when protocol unavailable).
	if alt && !shift && !ctrl && !super {
		return []byte{0x1b, '\r'}
	}
	// Shift / Super / Ctrl combos: CSI-u is unambiguous and what modern apps expect.
	return kittyCSIU(13, mods)
}

// consumeHostQueries scans app→terminal bytes for Kitty keyboard protocol
// management and primary/secondary DA. Returns replies to write back to the PTY.
// Data is left intact for the VT parser (unknown sequences are ignored there).
//
// OSC/DCS/APC/PM/SOS bodies are skipped, so an ESC c or CSI inside one (tmux
// passthrough, a title) is not acted on. An escape cut by the read boundary
// is held and finished with the next read.
func (k *kittyKeyboard) consumeHostQueries(data []byte) []byte {
	if k == nil || len(data) == 0 {
		return nil
	}
	if len(k.scan.tail) > 0 {
		data = append(k.scan.tail, data...)
		k.scan.tail = nil
	}
	var out []byte
	i := 0
	for i < len(data) {
		if k.scan.str != 0 {
			i = k.skipStringBody(data, i)
			continue
		}
		if data[i] != 0x1b {
			i++
			continue
		}
		if i+1 >= len(data) {
			k.scan.tail = []byte{0x1b}
			break
		}
		switch c := data[i+1]; c {
		case 'c':
			// RIS: full reset, keyboard state included.
			k.reset()
			i += 2
			continue
		case ']', 'P', '_', '^', 'X':
			k.scan.str = c
			i += 2
			continue
		case '[':
		default:
			i++
			continue
		}
		// CSI ... final
		j := i + 2
		for j < len(data) && (data[j] < 0x40 || data[j] > 0x7e) {
			j++
		}
		if j >= len(data) {
			if len(data)-i <= hostScanTailMax {
				k.scan.tail = append([]byte(nil), data[i:]...)
			}
			break
		}
		body := string(data[i+2 : j])
		final := data[j]
		i = j + 1

		switch final {
		case 'u':
			if reply := k.handleKittyU(body); len(reply) > 0 {
				out = append(out, reply...)
			}
		case 'c':
			if reply := handleDeviceAttrs(body); len(reply) > 0 {
				out = append(out, reply...)
			}
		case 'h', 'l':
			if altScreenModes(body) {
				k.setAltScreen(final == 'h')
			}
		}
	}
	return out
}

// skipStringBody advances past string-body bytes from i and returns where
// scanning resumes. ST (ESC \), BEL for OSC, and CAN/SUB end the body.
// ESC ESC is body data (tmux doubles a passthrough's ESCs). Any other ESC
// aborts the body and is scanned as an escape, as a VT parser would.
func (k *kittyKeyboard) skipStringBody(data []byte, i int) int {
	for i < len(data) {
		switch data[i] {
		case 0x07:
			if k.scan.str == ']' {
				k.scan.str = 0
				return i + 1
			}
		case 0x18, 0x1a:
			k.scan.str = 0
			return i + 1
		case 0x1b:
			if i+1 >= len(data) {
				k.scan.tail = []byte{0x1b}
				return len(data)
			}
			switch data[i+1] {
			case '\\':
				k.scan.str = 0
				return i + 2
			case 0x1b:
				i += 2
				continue
			}
			k.scan.str = 0
			return i
		}
		i++
	}
	return i
}

// handleKittyU processes CSI body + 'u' (progressive enhancement).
// Forms: ? / ?0  (query), =flags;mode (set), >flags (push), <n (pop).
func (k *kittyKeyboard) handleKittyU(body string) []byte {
	if k == nil {
		return nil
	}
	if body == "" {
		// Bare CSI u is DECRC (cursor restore) — not ours.
		return nil
	}
	switch body[0] {
	case '?':
		// Query: CSI ? u  or CSI ? 0 u
		rest := body[1:]
		if rest == "" || rest == "0" {
			return []byte(fmt.Sprintf("\x1b[?%du", k.flags))
		}
		return nil
	case '=':
		// Set: CSI = flags ; mode u
		flags, mode := parseFlagsMode(body[1:])
		k.apply(flags, mode)
		return nil
	case '>':
		// Push: CSI > flags u  (flags default 0)
		flags := 0
		if len(body) > 1 {
			flags, _ = strconv.Atoi(body[1:])
		}
		k.push(flags)
		return nil
	case '<':
		// Pop: CSI < n u  (n default 1)
		n := 1
		if len(body) > 1 {
			if v, err := strconv.Atoi(body[1:]); err == nil && v > 0 {
				n = v
			}
		}
		k.pop(n)
		return nil
	default:
		return nil
	}
}

// altScreenModes is true when a DEC private set/reset body names an
// alternate-screen mode (47, 1047, 1049).
func altScreenModes(body string) bool {
	if !strings.HasPrefix(body, "?") {
		return false
	}
	for _, p := range strings.Split(body[1:], ";") {
		switch strings.TrimSpace(p) {
		case "47", "1047", "1049":
			return true
		}
	}
	return false
}

func parseFlagsMode(s string) (flags, mode int) {
	mode = 1
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 1
	}
	parts := strings.Split(s, ";")
	if len(parts) > 0 {
		flags, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	}
	if len(parts) > 1 {
		if m, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && m > 0 {
			mode = m
		}
	}
	return flags, mode
}

// handleDeviceAttrs replies to primary/secondary DA so apps can finish the
// Kitty support probe (CSI ? u then DA1).
func handleDeviceAttrs(body string) []byte {
	// Primary DA: CSI c / CSI 0 c
	if body == "" || body == "0" {
		// VT220-class with 132-col + color-ish extras (common modern reply).
		return []byte("\x1b[?62;1;2;6;22c")
	}
	// Secondary DA: CSI > c / CSI > 0 c
	if body == ">" || body == ">0" {
		return []byte("\x1b[>0;10;1c")
	}
	return nil
}
