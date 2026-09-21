//go:build windows || darwin

package ui

import (
	"time"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// hitFrameButton returns 0 close, 1 minimize, 2 zoom, or -1.
// Visual order matches the strip: mac close/min/zoom, windows min/zoom/close.
func hitFrameButton(m chrome.Model, cellX int) int {
	if cellX < 0 {
		return -1
	}
	b := m.FrameButtonBounds()
	for i, span := range b {
		if span[1] > span[0] && cellX >= span[0] && cellX < span[1] {
			return i
		}
	}
	return -1
}

func frameActionForHit(frame chrome.Frame, hit int) chrome.FrameButton {
	if frame == chrome.FrameWindows {
		switch hit {
		case 0:
			return chrome.FrameMinimize
		case 1:
			return chrome.FrameZoom
		default:
			return chrome.FrameClose
		}
	}
	return chrome.FrameButton(hit)
}

type frameDrag struct {
	on      bool
	startSX int
	startSY int
	startWX int
	startWY int
	lastHit time.Time
	lastX   int
}
