//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/internal/config"

// shellMatrixIntensity is how bright persistent shell rain is vs settings/intro
// (quiet backdrop, not a curtain). Multiplied by config ShellMatrixOpacity.
const shellMatrixIntensity = 0.20

// shellMatrixAltScreenIntensity is used under alt-screen TUIs (Grok, vim, …).
// Sparse default-bg cells make 0.20 nearly invisible; slightly higher so the
// underlay still reads while normal shell rain stays quiet.
const shellMatrixAltScreenIntensity = 0.48

// effectiveShellMatrixIntensity is base rain strength × user opacity (0–1).
func effectiveShellMatrixIntensity(cfg config.Config, altScreen bool) float64 {
	base := shellMatrixIntensity
	if altScreen {
		base = shellMatrixAltScreenIntensity
	}
	return base * cfg.ShellMatrixOpacity01()
}
