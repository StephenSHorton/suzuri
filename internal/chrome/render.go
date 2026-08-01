package chrome

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/hinshun/vt10x"
)

// RenderToTerm writes the Charm View() into a small vt10x grid so the host
// can paint chrome with the same cell path as the shell (ANSI from Lip Gloss).
func RenderToTerm(m Model, cols int) vt10x.Terminal {
	if cols < 20 {
		cols = 20
	}
	rows := m.RowCount()
	if rows < TabStripRows() {
		rows = TabStripRows()
	}
	t := vt10x.New(vt10x.WithSize(cols, rows))
	// Clear to bar void so unpainted cells match the neon strip.
	_, _ = t.Write([]byte("\x1b[H\x1b[48;2;18;16;28m\x1b[2J\x1b[H\x1b[m"))
	writeView(t, m.View())
	return t
}

// writeView feeds a Lip Gloss View into vt10x with correct cell columns.
//
// Lip Gloss measures CJK/emoji as double-width, but the raw string still has
// one rune — a real terminal advances two columns; vt10x advances one. Without
// padding, every cell after a wide rune on that row slides left, so tab top/bottom
// borders no longer line up with the middle row (horizontal walls look like
// they “float”).
func writeView(t vt10x.Terminal, view string) {
	// ansi.DecodeSequence has panicked on bad parser state in the past —
	// never take down the host paint path for chrome.
	defer func() { _ = recover() }()

	view = strings.ReplaceAll(view, "\r\n", "\n")
	view = strings.ReplaceAll(view, "\r", "\n")

	p := ansi.NewParser()
	var state byte
	for len(view) > 0 {
		seq, width, n, newState := ansi.DecodeSequence(view, state, p)
		state = newState
		if n == 0 {
			break
		}
		chunk := view[:n]
		view = view[n:]

		if width <= 0 {
			// Escape / control: pass through (newlines → CRLF for VT columns).
			if chunk == "\n" {
				_, _ = t.Write([]byte("\r\n"))
			} else {
				_, _ = t.Write([]byte(chunk))
			}
			continue
		}

		_, _ = t.Write([]byte(seq))
		// Double-width grapheme: occupy the second column so the rest of the
		// row stays aligned with Lip Gloss’s layout.
		for w := 1; w < width; w++ {
			_, _ = t.Write([]byte{' '})
		}
	}
}
