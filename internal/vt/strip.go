package vt

import "bytes"

// StripCSI removes common ANSI/VT escape sequences so a naïve UI can show
// readable text before a full cell grid exists.
//
// This is intentionally incomplete — a real parser lands in internal/vt next.
func StripCSI(in []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(in))
	i := 0
	for i < len(in) {
		if in[i] != 0x1b {
			// Skip other C0 controls except tab/LF/CR.
			c := in[i]
			if c == '\n' || c == '\r' || c == '\t' || c >= 0x20 {
				out.WriteByte(c)
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
