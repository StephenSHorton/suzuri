//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/assets"

func playNoticeSound(name string, urgency int) {
	if name == "silent" {
		return
	}
	wav := assets.EditorSuccessWAV
	switch name {
	case "error":
		wav = assets.EditorFailWAV
	case "warn", "warning", "question":
		wav = assets.EditorPublishedWAV
	default:
		if urgency >= 2 {
			wav = assets.EditorFailWAV
		}
	}
	playWAV(wav)
}
