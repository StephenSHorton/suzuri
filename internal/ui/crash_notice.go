//go:build windows || darwin

package ui

import "sync"

const crashNoticeID = "suzuri-crash"

var (
	crashNoticeMu    sync.Mutex
	crashNoticeTitle string
	crashNoticeBody  string
	crashNoticeDir   string
)

// SetStartupCrashNotice queues the "suzuri closed unexpectedly" card for
// when the window comes up; a click opens dir (where the log and dump are).
// Call before Run.
func SetStartupCrashNotice(title, body, dir string) {
	crashNoticeMu.Lock()
	crashNoticeTitle, crashNoticeBody, crashNoticeDir = title, body, dir
	crashNoticeMu.Unlock()
}

// showStartupCrashNotice posts the queued crash card once. It stays until
// right-clicked away.
func showStartupCrashNotice() {
	crashNoticeMu.Lock()
	title, body, dir := crashNoticeTitle, crashNoticeBody, crashNoticeDir
	crashNoticeTitle, crashNoticeBody, crashNoticeDir = "", "", ""
	crashNoticeMu.Unlock()
	if title == "" {
		return
	}
	postDeskNote(deskNote{
		ID:    crashNoticeID,
		Title: title,
		Body:  body,
		Sound: "error",
		Icon:  "error",
		Never: true,
		OnActivate: func() {
			if dir != "" {
				openURLInBrowser(dir) // open/start: Finder or Explorer
			}
		},
	}, false, true)
}
