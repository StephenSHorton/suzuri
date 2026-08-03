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
type kittyKeyboard struct {
	flags int
	stack []int
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
func (k *kittyKeyboard) consumeHostQueries(data []byte) []byte {
	if k == nil || len(data) == 0 {
		return nil
	}
	var out []byte
	i := 0
	for i < len(data) {
		if data[i] != 0x1b {
			i++
			continue
		}
		if i+1 >= len(data) || data[i+1] != '[' {
			i++
			continue
		}
		// CSI ... final
		j := i + 2
		for j < len(data) && (data[j] < 0x40 || data[j] > 0x7e) {
			j++
		}
		if j >= len(data) {
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
		}
	}
	return out
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
