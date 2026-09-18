//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/internal/config"

// shellMatrixIntensity is how bright persistent shell rain is vs settings/intro
// (quiet backdrop, not a curtain). Multiplied by config ShellMatrixOpacity.
const shellMatrixIntensity = 0.16

// shellMatrixAltScreenIntensity is used under alt-screen TUIs (Grok, vim, …).
const shellMatrixAltScreenIntensity = 0.32

// matrixLoopSpeedMin / Span: always-on rain (intro spawn stays faster).
const (
	matrixLoopSpeedMin  = 0.16
	matrixLoopSpeedSpan = 0.06 // + 0..8 → ~0.16–0.64
	matrixLoopRateDiv   = 5.2
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
