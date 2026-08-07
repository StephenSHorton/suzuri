package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/hinshun/vt10x"
)

// RenderToTerm writes the tab strip View into a mini vt10x grid.
func RenderToTerm(m Model, cols int) vt10x.Terminal {
	return renderString(m.StripView(), cols, m.RowCount())
}

// RenderOverlayToTerm writes the floating palette/settings card.
// Row count follows the actual Lip Gloss height so tall views (settings +
// help caption / large workspace) are not clipped by a fixed estimate.
func RenderOverlayToTerm(m Model, cols int) vt10x.Terminal {
	view := m.OverlayView()
	rows := lipgloss.Height(view)
	if rows < 2 {
		rows = 2
	}
	// Cap relative to terminal height when known; allow tall workspace modals.
	maxRows := 48
	if m.Height > 0 {
		// leave room for tab strip; never exceed ~90% of host rows
		capH := int(float64(m.Height) * 0.9)
		if capH > maxRows {
			maxRows = capH
		}
	}
	if maxRows > 80 {
		maxRows = 80
	}
	if rows > maxRows {
		rows = maxRows
	}
	return renderString(view, cols, rows)
}

func renderString(view string, cols, rows int) vt10x.Terminal {
	if cols < 20 {
		cols = 20
	}
	if rows < 1 {
		rows = 1
	}
	t := vt10x.New(vt10x.WithSize(cols, rows))
	// Clear to default (black) — host treats default-bg empty cells as transparent
	// so only the dialog card is painted; dimmed shell shows through around it.
	_, _ = t.Write([]byte("\x1b[H\x1b[0m\x1b[2J\x1b[H"))
	writeView(t, view)
	return t
}

func writeView(t vt10x.Terminal, view string) {
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
			if chunk == "\n" {
				_, _ = t.Write([]byte("\r\n"))
			} else {
				_, _ = t.Write([]byte(chunk))
			}
			continue
		}

		_, _ = t.Write([]byte(seq))
		for w := 1; w < width; w++ {
			_, _ = t.Write([]byte{' '})
		}
	}
}
