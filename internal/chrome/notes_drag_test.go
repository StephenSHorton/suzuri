package chrome

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNotesClickAndDragSelect(t *testing.T) {
	m := openNotesBody(New(80))
	var r Result
	for _, ch := range "hello world" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Caret at end.
	if m.notesCursor != len([]rune("hello world")) {
		t.Fatalf("cursor=%d", m.notesCursor)
	}
	// Click near start of body text.
	textLeft := notesBodyTextLeft(80)
	_, bodyY0 := notesEditorOverlayRows()
	r = m.UpdateChrome(NotesClickMsg{CellX: textLeft, CellY: bodyY0, Cols: 80})
	m = r.Model
	if !r.StartNotesDrag {
		t.Fatal("body click should start drag")
	}
	if m.notesHasSel() {
		t.Fatal("click alone should clear selection")
	}
	// Drag to the right a few cells.
	r = m.UpdateChrome(NotesDragMsg{CellX: textLeft + 5, CellY: bodyY0, Cols: 80})
	m = r.Model
	if !m.notesHasSel() {
		t.Fatal("drag should create selection")
	}
	sel := m.NotesSelectedText()
	if sel == "" {
		t.Fatal("expected non-empty selection")
	}
	// Delete selection.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyBackspace})
	m = r.Model
	if m.notesHasSel() {
		t.Fatal("backspace should clear selection")
	}
	if string(m.notesRunes) == "hello world" {
		t.Fatalf("expected deletion, got %q", string(m.notesRunes))
	}
}

func TestNotesWordMoveAndDelete(t *testing.T) {
	m := openNotesBody(New(80))
	var r Result
	for _, ch := range "one two three" {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = r.Model
	}
	// Option/alt+left → word jump.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	m = r.Model
	if string(m.notesRunes[m.notesCursor:]) != "three" {
		t.Fatalf("alt+left → before three, got cursor=%d text after=%q",
			m.notesCursor, string(m.notesRunes[m.notesCursor:]))
	}
	// Ctrl+left another word.
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	m = r.Model
	if string(m.notesRunes[m.notesCursor:]) != "two three" {
		t.Fatalf("ctrl+left → before two, got %q", string(m.notesRunes[m.notesCursor:]))
	}
	// Delete word forward ("two ").
	m.NotesDeleteWord(1)
	if string(m.notesRunes) != "one three" {
		t.Fatalf("delete word forward: got %q", string(m.notesRunes))
	}
}
