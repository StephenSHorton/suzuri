package ui

import (
	"fmt"
	"time"

	"github.com/StephenSHorton/suzuri/internal/caffeine"
	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// applyCaffeineAction runs a palette/strip caffeine host action.
// Returns a short toast (may be empty) and whether chrome should refresh.
func applyCaffeineAction(m *caffeine.Manager, act chrome.HostAction, minutes int) (toast string, ok bool) {
	if m == nil {
		return "", false
	}
	switch act {
	case chrome.ActionCaffeineToggle:
		on := m.Toggle()
		if on {
			return "caffeine on · sleep prevented", true
		}
		return "caffeine off", true
	case chrome.ActionCaffeineFor:
		d := time.Duration(minutes) * time.Minute
		if minutes <= 0 {
			d = 0
		}
		if err := m.Activate(d); err != nil {
			return "caffeine failed: " + err.Error(), true
		}
		if minutes <= 0 {
			return "caffeine on · until toggled off", true
		}
		if minutes >= 60 && minutes%60 == 0 {
			return fmt.Sprintf("caffeine on · %dh", minutes/60), true
		}
		return fmt.Sprintf("caffeine on · %dm", minutes), true
	case chrome.ActionCaffeineOff:
		if !m.Active() {
			return "caffeine already off", true
		}
		m.Deactivate()
		return "caffeine off", true
	default:
		return "", false
	}
}

// syncCaffeineChrome pushes manager state into the strip coffee chip.
func syncCaffeineChrome(model chrome.Model, m *caffeine.Manager) chrome.Model {
	if m == nil {
		return model.UpdateChrome(chrome.SyncCaffeineMsg{}).Model
	}
	active := m.Active()
	hint := ""
	if active {
		hint = m.StripLabel()
	}
	return model.UpdateChrome(chrome.SyncCaffeineMsg{
		Active: active,
		Hint:   hint,
	}).Model
}

// caffeineTick expires timed activations. toast is non-empty when it turned off.
func caffeineTick(m *caffeine.Manager) (toast string) {
	if m == nil {
		return ""
	}
	if m.Tick() {
		return "caffeine off · timer ended"
	}
	return ""
}
