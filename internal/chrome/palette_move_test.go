package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTabMoveCommandsSkipActiveTab(t *testing.T) {
	tabs := []Tab{{ID: 10, Title: "api"}, {ID: 11, Title: "web"}, {ID: 12, Title: ""}}
	cmds := TabMoveCommands(tabs, 1)
	var titles []string
	for _, c := range cmds {
		titles = append(titles, c.Title)
		if c.TabID == 11 {
			t.Fatalf("active tab offered as a target: %q", c.Title)
		}
	}
	got := strings.Join(titles, "|")
	want := "Move pane to tab: 1 api|Move pane to tab: 3 shell|Merge tab into: 1 api|Merge tab into: 3 shell"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if TabMoveCommands(tabs[:1], 0) != nil {
		t.Fatal("one tab: nothing to move to")
	}
}

func TestPaletteMergeReturnsTargetTabID(t *testing.T) {
	m := New(100)
	m.Tabs = []Tab{{ID: 4, Title: "logs"}, {ID: 9, Title: "editor"}}
	m.Active = 1
	m.activatePalette()
	for _, r := range "merge tab into" {
		m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	act, _, _, tabID := m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyEnter})
	if act != ActionMergeTabInto || tabID != 4 {
		t.Fatalf("act=%v tabID=%d", act, tabID)
	}
}

func TestPaletteFindsMovePaneCommands(t *testing.T) {
	m := New(100)
	m.Tabs = []Tab{{ID: 0, Title: "a"}, {ID: 5, Title: "b"}}
	m.activatePalette()
	for _, r := range "move pane" {
		m.handlePaletteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	seen := map[HostAction]bool{}
	for _, it := range m.palView {
		seen[it.action] = true
	}
	for _, a := range []HostAction{ActionMovePaneToTab, ActionMovePaneToNewTab, ActionMovePanePrevTab, ActionMovePaneNextTab} {
		if !seen[a] {
			t.Fatalf("palette search for \"move pane\" missing action %v", a)
		}
	}
}

func TestPaletteFollowsTabsWhileOpen(t *testing.T) {
	m := New(100)
	m.Tabs = []Tab{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}, {ID: 3, Title: "c"}}
	m.Active = 0
	m.activatePalette()
	r := m.UpdateChrome(SyncTabsMsg{Tabs: []Tab{{ID: 1, Title: "a"}, {ID: 3, Title: "c"}}, Active: 0})
	m = r.Model
	for _, it := range m.palAll {
		if it.tabID == 2 {
			t.Fatalf("closed tab still offered: %q", it.title)
		}
	}
	if !m.PaletteOpen {
		t.Fatal("palette should stay open")
	}
}
