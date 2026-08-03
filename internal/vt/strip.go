package vt

import "bytes"

// StripOSC8Hyperlinks removes OSC 8 hyperlink wrappers, keeping display text.
//
// Form: ESC ] 8 ; params ; uri BEL/ST  …visible…  ESC ] 8 ; ; BEL/ST
//
// vt10x ignores OSC 8 but can mishandle long URIs (buf cap 256) and leave
// odd blank runs in some markdown renderers. Stripping wrappers before VT
// keeps the link label glyphs intact.
func StripOSC8Hyperlinks(in []byte) []byte {
	if len(in) == 0 || !bytes.Contains(in, []byte{0x1b, ']', '8'}) {
		return in
	}
	out := make([]byte, 0, len(in))
	i := 0
	for i < len(in) {
		if in[i] == 0x1b && i+2 < len(in) && in[i+1] == ']' && in[i+2] == '8' {
			// Consume OSC 8 through BEL or ST.
			j := i + 3
			for j < len(in) {
				if in[j] == 0x07 {
					j++
					break
				}
				if in[j] == 0x1b && j+1 < len(in) && in[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}

// StripCSI removes common ANSI/VT escape sequences so a naïve UI can show
// readable text before a full cell grid exists.
//
// Preserves C0 controls the soft display understands: BS, TAB, LF, CR, DEL.
//
// This is intentionally incomplete — a real parser lands in internal/vt next.
func StripCSI(in []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(in))
	i := 0
	for i < len(in) {
		if in[i] != 0x1b {
			c := in[i]
			switch c {
			case '\n', '\r', '\t', '\b', 0x7f:
				out.WriteByte(c)
			default:
				if c >= 0x20 {
					out.WriteByte(c)
				}
				// drop other C0 (BEL, etc.)
			}
			i++
			continue
		}
		// ESC
		i++
		if i >= len(in) {
			break
		}
		switch in[i] {
		case '[': // CSI ... cmd
			i++
			for i < len(in) {
				c := in[i]
				i++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
		case ']': // OSC ... BEL or ST
			i++
			for i < len(in) {
				if in[i] == 0x07 { // BEL
					i++
					break
				}
				if in[i] == 0x1b && i+1 < len(in) && in[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case '(':
			// charset designate — skip ESC ( X
			i++
			if i < len(in) {
				i++
			}
		default:
			// ESC X single intermediate — skip one byte
			i++
		}
	}
	return out.Bytes()
}
