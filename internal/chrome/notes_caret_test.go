package chrome

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestNotesEditorLayoutLines(t *testing.T) {
	m := openNotesBody(New(80))
	var r Result
	for _, ch := range "ab" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Cursor at end of "ab" → block sits in the empty cell after 'b' (col textLeft+2).
	// Mid-text: cursor=1 → block sits ON 'b' (col textLeft+1).
	view := m.OverlayView()
	lines := strings.Split(view, "\n")
	_, bodyY0 := notesEditorOverlayRows()
	if bodyY0 >= len(lines) {
		t.Fatalf("no body line at %d (h=%d)", bodyY0, len(lines))
	}
	plainBody := ansi.Strip(lines[bodyY0])
	idx := strings.Index(plainBody, "ab")
	if idx < 0 {
		t.Fatalf("body line has no \"ab\": %q", plainBody)
	}
	// Display column of 'a' (rune-aware).
	textLeft := utf8.RuneCountInString(plainBody[:idx])
	t.Logf("measured textLeft=%d body=%q", textLeft, plainBody)

	cx, cy, ok := m.NotesCaretCell(80)
	if !ok || cy != bodyY0 {
		t.Fatalf("caret y=%d want %d ok=%v", cy, bodyY0, ok)
	}
	// At end of "ab": insertion cell is after 'b'.
	if cx != textLeft+2 {
		t.Fatalf("EOL caret x=%d want %d (measured textLeft=%d, notesBodyTextLeft=%d)",
			cx, textLeft+2, textLeft, notesBodyTextLeft(80))
	}

	// Move cursor onto 'b' (index 1): block should cover 'b'.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyLeft})
	m = r.Model
	cx, cy, ok = m.NotesCaretCell(80)
	if !ok || cx != textLeft+1 {
		t.Fatalf("on 'b' caret x=%d want %d (cursor=%d)", cx, textLeft+1, m.notesCursor)
	}

	// ↑ into title
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyHome})
	m = r.Model
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
	_ = lipgloss.Width
}
