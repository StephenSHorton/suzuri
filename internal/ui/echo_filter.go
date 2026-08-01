package ui

import (
	"strings"

	"github.com/charmbracelet/log"
)

// echoFilter strips the shell's local echo of a just-submitted command from the
// raw PTY stream so only the host command block (pushBlock) shows the text.
//
// Windows PowerShell syntax-highlights the echo with CSI color codes even under
// -NoProfile. Matching is a streaming state machine: skip CSI while reading the
// plaintext command, then drop through the trailing newline. Leading CSI
// (cursor hide, etc.) is suppressed while armed; non-matching plaintext before
// the command is passed through without disarming (avoids giving up on noise).
type echoFilter struct {
	want  []rune // plaintext command to suppress
	pos   int    // matched rune count into want
	phase int    // 0 = match command, 1 = drop through newline
	armed bool
	seen  int // raw bytes consumed while armed (give-up budget)

	// CSI / ESC parser while suppressing
	escState int // 0 none, 1 saw ESC, 2 CSI params, 3 OSC
}

// Max PTY bytes to examine while armed before giving up (don't strand the stream).
const echoFilterGiveUp = 4096

const (
	echoPhaseMatch = 0
	echoPhaseNL    = 1
)

// arm prepares to suppress echo of cmd (may be multi-line). Empty cmd disarms.
func (f *echoFilter) arm(cmd string) {
	*f = echoFilter{} // full reset
	if stringsTrimSpace(cmd) == "" {
		return
	}
	// Normalize to a single logical echo unit. Multi-line: match first line
	// only for now (PS continuation prompts are a separate problem); remaining
	// lines still show once — better than double-printing the primary line.
	norm := strings.ReplaceAll(cmd, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	if i := strings.IndexByte(norm, '\n'); i >= 0 {
		norm = norm[:i]
	}
	norm = strings.TrimRight(norm, " \t")
	if norm == "" {
		return
	}
	f.want = []rune(norm)
	f.armed = true
	f.phase = echoPhaseMatch
	log.Debug("echo filter armed", "cmd", norm)
}

// feed consumes PTY bytes and returns bytes that should reach the VT parser.
func (f *echoFilter) feed(in []byte) []byte {
	if !f.armed || len(f.want) == 0 {
		return in
	}
	if len(in) == 0 {
		return nil
	}

	var out []byte
	for i := 0; i < len(in); i++ {
		b := in[i]

		// Once finished, pass the remainder through.
		if !f.armed {
			out = append(out, in[i:]...)
			break
		}

		f.seen++
		if f.seen > echoFilterGiveUp {
			log.Debug("echo filter give up", "seen", f.seen)
			out = append(out, in[i:]...)
			f.disarm()
			break
		}

		// ESC / CSI / OSC handling while armed:
		//
		// PowerShell wraps local-echo in SGR color CSI — suppress those once we
		// have started matching the command text (pos>0) or are draining the
		// trailing newline (phase NL).
		//
		// BUT: when pos==0 (not yet matching), pass CSI through. Otherwise a
		// Unix `clear` / terminfo wipe that arrives *before* the echoed command
		// (or when echo is off) is swallowed and the VT never clears.
		if f.escState != 0 || b == 0x1b {
			passThrough := f.pos == 0 && f.phase == echoPhaseMatch
			if passThrough {
				out = append(out, b)
				_ = f.consumeEsc(b) // keep parser in sync; bytes already emitted
				continue
			}
			if f.consumeEsc(b) {
				// still inside escape — drop (PS color / cursor hide around echo)
				continue
			}
			// escape finished — drop the final byte too
			continue
		}

		switch f.phase {
		case echoPhaseMatch:
			// CR/LF: while not yet matching, pass through (real prior output).
			// Once matching, CR is end-of-echo noise; LF resets a partial match.
			if b == '\r' {
				if f.pos == 0 {
					out = append(out, b)
				}
				continue
			}
			if b == '\n' {
				if f.pos == 0 {
					out = append(out, b)
					continue
				}
				// Mid-command newline shouldn't happen for single-line; reset.
				f.pos = 0
				continue
			}
			// Other controls: pass when idle, drop/reset when mid-match.
			if b < 0x20 {
				if f.pos == 0 {
					out = append(out, b)
					continue
				}
				f.pos = 0
				continue
			}

			want := f.want[f.pos]
			// Compare as rune; input is UTF-8 — for ASCII commands byte==rune.
			r, n := decodeRuneByte(in, i)
			if n > 1 {
				// Multi-byte rune in stream
				if r == want {
					f.pos++
					i += n - 1
					if f.pos >= len(f.want) {
						f.phase = echoPhaseNL
						f.pos = 0
					}
					continue // suppressed
				}
				// Mismatch: if we haven't started, pass this rune through.
				if f.pos == 0 {
					out = append(out, in[i:i+n]...)
					i += n - 1
					continue
				}
				// Started matching then diverged — pass pending? We already
				// suppressed matched bytes (unrecoverable). Reset and pass current.
				log.Debug("echo filter reset mid-match", "got", string(r), "want", string(want))
				f.pos = 0
				out = append(out, in[i:i+n]...)
				i += n - 1
				continue
			}

			// Single-byte
			if rune(b) == want {
				f.pos++
				if f.pos >= len(f.want) {
					f.phase = echoPhaseNL
					f.pos = 0
				}
				continue // suppress
			}
			if f.pos == 0 {
				// Leading plaintext that isn't our command (real output early).
				out = append(out, b)
				continue
			}
			// Divergence after partial match.
			log.Debug("echo filter reset mid-match", "got", string(b), "want", string(want))
			f.pos = 0
			out = append(out, b)

		case echoPhaseNL:
			// Drop everything until we consume a newline (end of echo line).
			if b == '\n' {
				f.disarm()
				log.Debug("echo filter matched and suppressed")
				continue // drop the newline too
			}
			// Drop CR, trailing CSI already handled above, drop leftover junk
			// on the echo line (should be nothing for PS).
			continue
		}
	}
	return out
}

func (f *echoFilter) disarm() {
	f.armed = false
	f.want = nil
	f.pos = 0
	f.phase = 0
	f.escState = 0
	f.seen = 0
}

// status reports suppressor state for the MCP diag bridge.
func (f *echoFilter) status() (armed bool, cmd string, phase int) {
	if !f.armed {
		return false, "", 0
	}
	return true, string(f.want), f.phase
}

// consumeEsc advances ESC/CSI/OSC state. Returns true if still inside a sequence.
func (f *echoFilter) consumeEsc(b byte) bool {
	switch f.escState {
	case 0:
		if b == 0x1b {
			f.escState = 1
			return true
		}
		return false
	case 1: // after ESC
		switch b {
		case '[':
			f.escState = 2
			return true
		case ']':
			f.escState = 3
			return true
		default:
			// ESC X single-byte (or charset) — done after this byte
			f.escState = 0
			return false
		}
	case 2: // CSI ... final byte 0x40-0x7e
		if b >= 0x40 && b <= 0x7e {
			f.escState = 0
			return false
		}
		return true
	case 3: // OSC ... BEL or ST
		if b == 0x07 {
			f.escState = 0
			return false
		}
		// ST is ESC\ — if we see ESC, go to state 1 and let next iter handle;
		// but ST end is ESC \ : handle ESC inside OSC by staying, next byte \\.
		if b == 0x1b {
			f.escState = 4
			return true
		}
		return true
	case 4: // OSC almost ST: expect '\'
		f.escState = 0
		return false
	default:
		f.escState = 0
		return false
	}
}

// decodeRuneByte decodes one UTF-8 rune starting at i, or one byte on error.
func decodeRuneByte(in []byte, i int) (rune, int) {
	if i >= len(in) {
		return 0, 0
	}
	b := in[i]
	if b < 0x80 {
		return rune(b), 1
	}
	// Minimal UTF-8 decode (2-3 byte); enough for command lines we care about.
	if b&0xe0 == 0xc0 && i+1 < len(in) {
		return rune(b&0x1f)<<6 | rune(in[i+1]&0x3f), 2
	}
	if b&0xf0 == 0xe0 && i+2 < len(in) {
		return rune(b&0x0f)<<12 | rune(in[i+1]&0x3f)<<6 | rune(in[i+2]&0x3f), 3
	}
	return rune(b), 1
}
