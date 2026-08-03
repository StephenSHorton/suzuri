package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestNotesEditorLayoutLines(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenNotesMsg{})
	m = r.Model
	for _, ch := range "ab" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	view := m.OverlayView()
	lines := strings.Split(view, "\n")
	t.Logf("overlay height=%d lipgloss=%d", len(lines), lipgloss.Height(view))
	for i, ln := range lines {
		if i > 22 {
			break
		}
		// Show a short prefix (ANSI-heavy).
		end := len(ln)
		if end > 80 {
			end = 80
		}
		t.Logf("%02d %q", i, ln[:end])
	}
	cx, cy, ok := m.NotesCaretCell(80)
	t.Logf("caret cell x=%d y=%d ok=%v cursor=%d text=%q", cx, cy, ok, m.notesCursor, string(m.notesRunes))
	_, wantBodyY0 := notesEditorOverlayRows()
	if !ok || cy != wantBodyY0 {
		t.Fatalf("caret y=%d want bodyY0=%d (first body line for \"ab\")", cy, wantBodyY0)
	}
	// ↑ into title
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyUp})
	m = r.Model
	if m.notesFocus != notesFocusTitle {
		t.Fatalf("up → title focus, got %v", m.notesFocus)
	}
	_, cy, ok = m.NotesCaretCell(80)
	wantTitleY, _ := notesEditorOverlayRows()
	if !ok || cy != wantTitleY {
		t.Fatalf("title caret y=%d want %d", cy, wantTitleY)
	}
}
