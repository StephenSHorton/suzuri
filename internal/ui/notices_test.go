//go:build windows || darwin

package ui

import (
	"strings"
	"testing"
	"time"
)

func TestOSC99SimpleAndChunked(t *testing.T) {
	var m termModes
	one := m.feed(time.Now(), []byte("\x1b]99;;Hello world\x1b\\"), 0)
	if len(one.notes) != 1 || one.notes[0].Title != "Hello world" {
		t.Fatalf("simple: %+v", one.notes)
	}
	var m2 termModes
	held := m2.feed(time.Now(), []byte("\x1b]99;i=job:d=0;Build\x1b\\"), 0)
	if len(held.notes) != 0 {
		t.Fatalf("early show: %+v", held.notes)
	}
	done := m2.feed(time.Now(), []byte("\x1b]99;i=job:p=body;3 targets\x1b\\"), 0)
	if len(done.notes) != 1 || done.notes[0].Title != "Build" || done.notes[0].Body != "3 targets" {
		t.Fatalf("chunked: %+v", done.notes)
	}
}

func TestOSC99Base64QueryAndClose(t *testing.T) {
	var m termModes
	// "ok" base64
	res := m.feed(time.Now(), []byte("\x1b]99;i=a:e=1;b2s=\x1b\\"), 0)
	if len(res.notes) != 1 || res.notes[0].Title != "ok" {
		t.Fatalf("b64: %+v", res.notes)
	}
	q := m.feed(time.Now(), []byte("\x1b]99;i=probe:p=?;\x1b\\"), 0)
	if !strings.Contains(string(q.replies), "p=?") || !strings.Contains(string(q.replies), "p=title,body") {
		t.Fatalf("query: %q", q.replies)
	}
	c := m.feed(time.Now(), []byte("\x1b]99;i=a:p=close;\x1b\\"), 0)
	if len(c.closedIDs) != 1 || c.closedIDs[0] != "a" {
		t.Fatalf("close: %+v", c.closedIDs)
	}
}

func TestOSC9And777StillNotify(t *testing.T) {
	var m termModes
	a := m.feed(time.Now(), []byte("\x1b]9;task done\x07"), 0)
	if len(a.notes) != 1 || a.notes[0].Body != "task done" {
		t.Fatalf("osc9: %+v", a.notes)
	}
	b := m.feed(time.Now(), []byte("\x1b]777;notify;Ship;uploaded\x07"), 0)
	if len(b.notes) != 1 || b.notes[0].Title != "Ship" || b.notes[0].Body != "uploaded" {
		t.Fatalf("osc777: %+v", b.notes)
	}
}

func TestUpdateNoticeStaysUntilDismiss(t *testing.T) {
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
	var activated, dismissed bool
	n := sampleUpdateNote("9.9.9")
	n.OnActivate = func() { activated = true }
	n.OnDismiss = func() { dismissed = true }
	postDeskNote(n, false, true)
	tickNotices(time.Now().Add(24 * time.Hour))
	if noticeCount() != 1 {
		t.Fatalf("permanent card expired, count %d", noticeCount())
	}
	dismissNoticeAt(0)
	if !dismissed || activated || noticeCount() != 0 {
		t.Fatalf("dismiss activated=%v dismissed=%v count=%d", activated, dismissed, noticeCount())
	}
	postDeskNote(n, false, true)
	activateNotice(0)
	if !activated || noticeCount() != 0 {
		t.Fatalf("activate activated=%v count=%d", activated, noticeCount())
	}
}

func TestNoticeOccasionAndSound(t *testing.T) {
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
	postDeskNote(deskNote{Title: "skip", Occasion: "unfocused"}, true, true)
	if len(aliveDeskIDs()) != 0 && noticeCount() != 0 {
		t.Fatal("showed while focused")
	}
	postDeskNote(deskNote{ID: "z", Title: "show", Sound: "silent"}, false, true)
	if noticeCount() != 1 {
		t.Fatalf("count %d", noticeCount())
	}
	closeDeskNote("z")
	if noticeCount() != 0 {
		t.Fatal("not closed")
	}
}
