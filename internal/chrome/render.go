package chrome

import (
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
	// Reset + home so each frame is clean.
	_, _ = t.Write([]byte("\x1b[H\x1b[2J\x1b[m"))
	view := m.View()
	// Bubble Tea views use \n; VT needs \r\n for consistent columns.
	// Also ensure we don't exceed width wildly — lipgloss already wraps-ish.
	_, _ = t.Write([]byte(view))
	return t
}
