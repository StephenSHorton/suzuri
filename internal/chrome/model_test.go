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
	if !strings.Contains(v, "alpha") {
		t.Fatalf("view missing inactive tab: %q", v)
	}
	// Brand mark should appear.
	if !strings.Contains(v, "硯") {
		t.Fatalf("view missing brand: %q", v)
	}
}

func TestTabBoundsMatchLayout(t *testing.T) {
	m := New(100)
	m.Tabs = []Tab{{ID: 0, Title: "one"}, {ID: 1, Title: "two"}, {ID: 2, Title: "three"}}
	m.Active = 0
	bounds := m.TabBounds()
	if len(bounds) != 3 {
		t.Fatalf("bounds len=%d", len(bounds))
	}
	// Bounds should be non-empty and non-overlapping in order.
	for i, b := range bounds {
		if b[1] <= b[0] {
			t.Fatalf("tab %d empty bound %v", i, b)
		}
		if i > 0 && b[0] < bounds[i-1][1] {
			t.Fatalf("tab %d overlaps prev: %v vs %v", i, b, bounds[i-1])
		}
	}
}

func TestNewTabAction(t *testing.T) {
	m := New(80)
	r := m.UpdateChrome(OpenPaletteMsg{})
	m = r.Model
	if !m.PaletteOpen {
		t.Fatal("palette should open")
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyEnter})
	if r.Action != ActionNewTab {
		t.Fatalf("action=%v want NewTab", r.Action)
	}
}

func TestRenderToTerm(t *testing.T) {
	m := New(60)
	m.Tabs = []Tab{{ID: 0, Title: "shell"}}
	m.Active = 0
	term := RenderToTerm(m, 60)
	cols, rows := term.Size()
	if cols < 20 || rows < 2 {
		t.Fatalf("size %d×%d", cols, rows)
	}
	// First row should have some non-empty cell (brand or tab text).
	found := false
	for x := 0; x < cols; x++ {
		if term.Cell(x, 0).Char != 0 && term.Cell(x, 0).Char != ' ' {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("chrome row 0 empty after render")
	}
}
