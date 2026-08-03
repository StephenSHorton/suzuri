package chrome

import tea "github.com/charmbracelet/bubbletea"

// openNotesBody opens notes and lands in the body editor (skips blank-note title).
func openNotesBody(m Model) Model {
	r := m.UpdateChrome(OpenNotesMsg{})
	m = r.Model
	if m.notesFocus == notesFocusTitle {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
		m = r.Model
	}
	return m
}
