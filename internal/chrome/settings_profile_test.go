package chrome_test

import (
	"testing"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSettingsProfileCycle(t *testing.T) {
	m := chrome.New(80)
	cfg := config.Default()
	r := m.UpdateChrome(chrome.OpenSettingsMsg{Config: cfg})
	m = r.Model
	// Look → Shell → Session, then down onto Profile.
	for i := 0; i < 2; i++ {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyTab})
		m = r.Model
	}
	r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyDown})
	m = r.Model
	// Cycle profiles both ways
	for i := 0; i < 6; i++ {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyRight})
		m = r.Model
		if r.Action != chrome.ActionSettingsPreview {
			t.Fatalf("nudge action=%v", r.Action)
		}
		_ = r.Settings
		// Simulate host applyConfigLive SyncConfigMsg
		r2 := m.UpdateChrome(chrome.SyncConfigMsg{Config: r.Settings})
		m = r2.Model
		if !m.SettingsOpen {
			t.Fatal("settings closed after SyncConfigMsg")
		}
		// render should not panic
		_ = m.OverlayView()
	}
	for i := 0; i < 6; i++ {
		r = m.UpdateChrome(tea.KeyMsg{Type: tea.KeyLeft})
		m = r.Model
		_ = m.OverlayView()
	}
}
