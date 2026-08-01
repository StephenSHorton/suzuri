package chrome

import (
	"strings"

	"github.com/hinshun/vt10x"
)

// RenderToTerm writes the Charm View() into a small vt10x grid so the host
// can paint chrome with the same cell path as the shell (ANSI from Lip Gloss).
func RenderToTerm(m Model, cols int) vt10x.Terminal {
	if cols < 20 {
		cols = 20
	}
	rows := m.RowCount()
	if rows < 2 {
		rows = 2
	}
	t := vt10x.New(vt10x.WithSize(cols, rows))
	// Reset + home; set default dark bg so unstyled cells aren't pure black gaps.
	_, _ = t.Write([]byte("\x1b[H\x1b[2J\x1b[m\x1b[48;2;15;15;20m\x1b[2J"))
	view := m.View()
	// Bubble Tea / Lip Gloss use \n; convert to \r\n so columns stay aligned.
	view = strings.ReplaceAll(view, "\r\n", "\n")
	view = strings.ReplaceAll(view, "\n", "\r\n")
	_, _ = t.Write([]byte(view))
	return t
}
