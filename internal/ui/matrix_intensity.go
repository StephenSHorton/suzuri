//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/internal/config"

// shellMatrixIntensity is how bright persistent shell rain is vs settings/intro
// (quiet backdrop, not a curtain). Multiplied by config ShellMatrixOpacity.
const shellMatrixIntensity = 0.10

// shellMatrixAltScreenIntensity is used under alt-screen TUIs (Grok, vim, …).
// Keep this well below conversation text so empty TUI cells don't glow.
const shellMatrixAltScreenIntensity = 0.16

// matrixLoopSpeedMin / Span: cells/frame-ish for always-on rain (slower than intro).
const (
	matrixLoopSpeedMin  = 0.07
	matrixLoopSpeedSpan = 0.025 // + 0..8 → ~0.07–0.27
	matrixLoopRateDiv   = 9.0
)

// effectiveShellMatrixIntensity is base rain strength × user opacity (0–1).
func effectiveShellMatrixIntensity(cfg config.Config, altScreen bool) float64 {
	base := shellMatrixIntensity
	if altScreen {
		base = shellMatrixAltScreenIntensity
	}
	return base * cfg.ShellMatrixOpacity01()
}

// settingsAmbientShowcaseIntensity is brighter than quiet shell ambient so the
// chosen Ambient style reads clearly behind the settings card (matte + modal).
func settingsAmbientShowcaseIntensity(cfg config.Config) float64 {
	// ~3.5× quiet shell rain, clamped.
	v := effectiveShellMatrixIntensity(cfg, false) * 3.5
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}
