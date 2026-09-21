//go:build windows || darwin

package ui

const updateNoticeID = "suzuri-update"

// offerUpdateNotice shows a card that stays until the user installs or
// dismisses it. A left click installs. A right click skips this version
// for the rest of the session.
func offerUpdateNotice(version string, toast func(string)) {
	if version == "" {
		return
	}
	postDeskNote(deskNote{
		ID:    updateNoticeID,
		Title: "Update to v" + version,
		Body:  "Click to install · right-click to skip",
		Sound: "info",
		Never: true,
		Focus: false,
		OnActivate: func() {
			applyPendingUpdate(toast)
		},
		OnDismiss: markUpdateLater,
	}, false, true)
}

func sampleUpdateNote(version string) deskNote {
	title := "Update available"
	if version != "" {
		title = "Update to v" + version
	}
	return deskNote{
		ID:    updateNoticeID,
		Title: title,
		Body:  "Click to install · right-click to skip",
		Sound: "info",
		Never: true,
		Focus: false,
	}
}
