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
	// Clear to tab-bar grey so unpainted cells don't flash pure black.
	_, _ = t.Write([]byte("\x1b[H\x1b[48;2;31;31;31m\x1b[2J\x1b[H\x1b[m"))
	view := m.View()
	view = strings.ReplaceAll(view, "\r\n", "\n")
	view = strings.ReplaceAll(view, "\n", "\r\n")
	_, _ = t.Write([]byte(view))
	return t
}
