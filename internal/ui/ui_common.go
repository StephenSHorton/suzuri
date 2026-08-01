//go:build windows || darwin

package ui

import "time"

const (
	appTitle = "suzuri（硯）"

	// Hard caps so a bad metric cannot ask PTY/VT for a multi-megagrid.
	maxTermCols = 400
	maxTermRows = 200

	// Smooth opacity pulse (sine), not a hard on/off blink.
	cursorBlinkPeriod = 1200 * time.Millisecond
	cursorBlinkTick   = 40 * time.Millisecond // ~25 fps for soft fade

	// Startup rain: spawn new streams for this long, then let them fall off.
	matrixIntroSpawn = 2 * time.Second
	// Safety cap so wind-down cannot run forever on a stuck paint path.
	matrixIntroMaxTotal = 12 * time.Second

	cellW = 9
	cellH = 18

	maxTabs        = 16
	tabBarFallback = 36 // used before first paint measures font
)
