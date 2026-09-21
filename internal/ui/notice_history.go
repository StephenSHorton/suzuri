//go:build windows || darwin

package ui

import "github.com/StephenSHorton/suzuri/internal/chrome"

func openNoticeHistory(apply func(chrome.OpenNotificationsMsg)) {
	items := noticeHistory()
	lines := make([]chrome.NoticeLine, len(items))
	for i, n := range items {
		lines[i] = chrome.NoticeLine{
			Title: n.Title,
			Body:  n.Body,
			When:  n.At.Format("15:04"),
			TabID: n.TabID,
		}
	}
	apply(chrome.OpenNotificationsMsg{Items: lines})
	if onNoticeBell != nil {
		onNoticeBell()
	}
}
