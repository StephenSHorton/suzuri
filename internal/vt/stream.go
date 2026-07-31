package vt

import (
	"unicode/utf8"
)

// Stream strips VT sequences and decodes UTF-8 across chunk boundaries so
// split multi-byte runes and partial CSI/OSC never surface as U+FFFD tofu
// or stray '[' characters next to the cursor.
type Stream struct {
	// hold incomplete UTF-8 or incomplete ESC sequence between Write calls
	buf []byte
}

// Write accepts the next ConPTY chunk and returns decoded plain text ready
// for the soft line buffer (may be empty if everything was held back).
func (s *Stream) Write(p []byte) string {
	if len(p) == 0 {
		return ""
	}
	s.buf = append(s.buf, p...)
	out, rest := s.consume(s.buf)
	// Keep only a reasonable hangover (incomplete ESC / UTF-8).
	if len(rest) > 256 {
		// Pathological: drop overflow so we don't grow forever.
		rest = rest[len(rest)-64:]
	}
	s.buf = append(s.buf[:0], rest...)
	return out
}

// Flush emits any held bytes as best-effort text (e.g. on session end).
func (s *Stream) Flush() string {
	if len(s.buf) == 0 {
		return ""
	}
	// Best effort: strip what we can, then UTF-8 with replacement filtered.
	b := StripCSI(s.buf)
	s.buf = s.buf[:0]
	return filterTofu(string(b))
}

func (s *Stream) consume(in []byte) (text string, rest []byte) {
	var out []byte
	i := 0
	for i < len(in) {
		// Escape sequence?
		if in[i] == 0x1b {
			n, complete := escLen(in[i:])
			if !complete {
				// Need more bytes.
				return string(out), in[i:]
			}
			i += n
			continue
		}

		c := in[i]
		// C0 we care about
		switch c {
		case '\n', '\r', '\t', '\b', 0x7f:
			out = append(out, c)
			i++
			continue
		}
		if c < 0x20 {
			// drop other controls
			i++
			continue
		}

		// UTF-8 rune
		if c < 0x80 {
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRune(in[i:])
		if r == utf8.RuneError && size == 1 {
			// Either invalid or incomplete multi-byte.
			if !utf8.FullRune(in[i:]) {
				return string(out), in[i:]
			}
			// Invalid byte — skip rather than emit U+FFFD tofu.
			i++
			continue
		}
		if r == utf8.RuneError {
			i += size
			continue
		}
		// Skip other non-characters / BOM if they appear mid-stream
		if r == 0xFEFF {
			i += size
			continue
		}
		out = append(out, in[i:i+size]...)
		i += size
	}
	return string(out), nil
}

// escLen returns how many bytes the ESC sequence at p occupies, and whether
// it is complete. p[0] must be ESC.
func escLen(p []byte) (n int, complete bool) {
	if len(p) < 2 {
		return 0, false
	}
	switch p[1] {
	case '[': // CSI: ESC [ ... finalbyte 0x40-0x7E
		i := 2
		for i < len(p) {
			c := p[i]
			i++
			if c >= 0x40 && c <= 0x7e {
				return i, true
			}
		}
		return 0, false
	case ']': // OSC: ESC ] ... BEL or ST (ESC \)
		i := 2
		for i < len(p) {
			if p[i] == 0x07 {
				return i + 1, true
			}
			if p[i] == 0x1b {
				if i+1 >= len(p) {
					return 0, false
				}
				if p[i+1] == '\\' {
					return i + 2, true
				}
			}
			i++
		}
		return 0, false
	case 'P', 'X', '^', '_': // DCS/SOS/PM/APC ... ST
		i := 2
		for i < len(p) {
			if p[i] == 0x1b {
				if i+1 >= len(p) {
					return 0, false
				}
				if p[i+1] == '\\' {
					return i + 2, true
				}
			}
			i++
		}
		return 0, false
	case '(':
		// ESC ( X
		if len(p) < 3 {
			return 0, false
		}
		return 3, true
	default:
		// ESC + one intermediate
		return 2, true
	}
}

func filterTofu(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r == utf8.RuneError || r == 0xFFFD {
			continue
		}
		b = append(b, string(r)...)
	}
	return string(b)
}
