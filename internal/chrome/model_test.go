package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestViewHasTabs(t *testing.T) {
	m := New(80)
	m.Tabs = []Tab{{ID: 0, Title: "alpha"}, {ID: 1, Title: "beta"}}
	m.Active = 1
	v := m.View()
	if !strings.Contains(v, "beta") {
		t.Fatalf("view missing tab title: %q", v)
	}
}

func TestNewTabAction(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{}, Alt: false})
	// ctrl+shift+t via string form
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlT}) // may not match
	_ = r
	// Use OpenPalette + enter on first item
	m = New(80)
	r = m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	if !m.PaletteOpen {
		t.Fatal("palette should open")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionNewTab {
		t.Fatalf("action=%v want NewTab", r.Action)
	}
}
