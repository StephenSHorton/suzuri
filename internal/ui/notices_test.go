//go:build windows || darwin

package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/suzuri/internal/config"
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

func TestActivateNoticeRevealsSourcePane(t *testing.T) {
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
	var got int
	prev := revealNoticePane
	revealNoticePane = func(id int) { got = id }
	defer func() { revealNoticePane = prev }()
	postDeskNote(deskNote{Title: "done", Focus: true, tab: &tab{id: 7}, Expire: time.Second}, false, true)
	activateNotice(0)
	if got != 7 || noticeCount() != 0 {
		t.Fatalf("got %d count %d", got, noticeCount())
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

func TestNoticeStackAndPanelOrigins(t *testing.T) {
	// Resting bottom-left matches the original card geometry.
	w, h, slots := noticeStackLayout(2, 0, 0)
	if w != noticeMargin+noticeCardW+noticeSlidePad || h != noticeMargin+2*(noticeCardH+noticeGap) {
		t.Fatalf("size %d×%d", w, h)
	}
	if slots[0].X != noticeMargin || slots[0].Y != h-noticeMargin-noticeCardH {
		t.Fatalf("oldest %+v", slots[0])
	}
	if slots[1].Y >= slots[0].Y {
		t.Fatalf("newest should stack upward: %+v", slots)
	}
	// Entrance still slides in from the left.
	_, _, slid := noticeStackLayout(1, 0, noticeSlideMax)
	if slid[0].X != noticeMargin-noticeSlideMax {
		t.Fatalf("slide x %d", slid[0].X)
	}

	visW, visH := 1440, 900
	panelW, panelH := 400, 200
	for _, id := range []string{
		"bottom-left", "bottom-center", "bottom-right",
		"center-left", "center", "center-right",
		"top-left", "top-center", "top-right",
	} {
		a := config.NoticePositionIndex(id)
		_, _, slots = noticeStackLayout(1, a, 0)
		right := slots[0].X + noticeCardW
		switch noticeColumn(a) {
		case 0:
			if slots[0].X != noticeMargin {
				t.Fatalf("%s x %d", id, slots[0].X)
			}
		case 1:
			bw, _, _ := noticeStackLayout(1, a, 0)
			if slots[0].X != (bw-noticeCardW)/2 {
				t.Fatalf("%s center x %d", id, slots[0].X)
			}
		case 2:
			bw, _, _ := noticeStackLayout(1, a, 0)
			if right != bw-noticeMargin {
				t.Fatalf("%s right %d bw %d", id, right, bw)
			}
		}
		x, y := noticePanelOrigin(0, 0, visW, visH, panelW, panelH, noticeScreenInset, a)
		switch noticeColumn(a) {
		case 0:
			if x != noticeScreenInset {
				t.Fatalf("%s panel x %d", id, x)
			}
		case 1:
			if x != (visW-panelW)/2 {
				t.Fatalf("%s panel x %d", id, x)
			}
		case 2:
			if x != visW-panelW-noticeScreenInset {
				t.Fatalf("%s panel x %d", id, x)
			}
		}
		switch noticeRow(a) {
		case 0:
			if y != noticeScreenInset {
				t.Fatalf("%s panel y %d", id, y)
			}
		case 1:
			if y != (visH-panelH)/2 {
				t.Fatalf("%s panel y %d", id, y)
			}
		case 2:
			if y != visH-panelH-noticeScreenInset {
				t.Fatalf("%s panel y %d", id, y)
			}
		}
	}
}

func TestHostToastReplacesFamilyAndClassifies(t *testing.T) {
	noticeMu.Lock()
	noticeLive = nil
	noticeHist = nil
	noticeMu.Unlock()

	postHostToast("caffeine on · sleep prevented")
	postHostToast("caffeine off")
	if noticeCount() != 1 {
		t.Fatalf("caffeine stacked, count %d", noticeCount())
	}
	noticeMu.Lock()
	got := noticeLive[0].deskNote
	noticeMu.Unlock()
	if got.ID != "host-caffeine" || got.Title != "caffeine off" || got.Sound != "info" {
		t.Fatalf("caffeine card %+v", got)
	}

	postHostToast("split failed")
	postHostToast("font 14px")
	postHostToast("clipboard empty")
	if noticeCount() != 4 {
		t.Fatalf("count %d", noticeCount())
	}
	noticeMu.Lock()
	var failed, font, empty deskNote
	for _, n := range noticeLive {
		switch n.ID {
		case "host-layout":
			failed = n.deskNote
		case "host-font":
			font = n.deskNote
		case "":
			if n.Title == "clipboard empty" {
				empty = n.deskNote
			}
		}
	}
	noticeMu.Unlock()
	if failed.Sound != "error" || failed.Title != "split failed" {
		t.Fatalf("split %+v", failed)
	}
	if font.Sound != "info" || font.ID != "host-font" {
		t.Fatalf("font %+v", font)
	}
	if empty.Sound != "warn" {
		t.Fatalf("empty %+v", empty)
	}
	postHostToast("")
	if noticeCount() != 4 {
		t.Fatal("empty toast posted")
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

func TestCommandFinishOSC(t *testing.T) {
	var m termModes
	res := m.feed(time.Now(), []byte("\x1b]7879;done;0;Z28gdGVzdA==\x07"), 0)
	if len(res.notes) != 1 || res.notes[0].Title != "go test" || res.notes[0].Body != "succeeded" {
		t.Fatalf("success: %+v", res.notes)
	}
	if res.notes[0].Occasion != "unfocused" || res.notes[0].Sound != "info" {
		t.Fatalf("gate: %+v", res.notes[0])
	}
	fail := m.feed(time.Now().Add(time.Second), []byte("\x1b]133;E;make\x1b\\\x1b]133;D;2\x1b\\"), 0)
	if len(fail.notes) != 1 || fail.notes[0].Title != "make" || fail.notes[0].Body != "failed (exit 2)" {
		t.Fatalf("fail: %+v", fail.notes)
	}
	if fail.notes[0].Sound != "error" {
		t.Fatalf("sound: %s", fail.notes[0].Sound)
	}
	var both termModes
	dup := both.feed(time.Now(), []byte("\x1b]133;D;0\x1b\\\x1b]7879;done;0;bHM=\x1b\\"), 0)
	if len(dup.notes) != 1 || dup.notes[0].Title != "ls" || dup.notes[0].Body != "succeeded" {
		t.Fatalf("dedupe: %+v", dup.notes)
	}
	again := both.feed(time.Now(), []byte("\x1b]133;D;0\x1b\\"), 0)
	if len(again.notes) != 0 {
		t.Fatalf("second card: %+v", again.notes)
	}
	img := m.feed(time.Now(), []byte("\x1b]1337;File=inline=1:QQ==\x07"), 0)
	if !strings.Contains(string(img.ready), "1337;File=") {
		t.Fatalf("1337 swallowed: %q", img.ready)
	}
}

func TestOSC99PendingAndTextCapped(t *testing.T) {
	var m termModes
	for i := 0; i < 20; i++ {
		id := "job" + strconv.Itoa(i)
		payload := []byte("\x1b]99;i=" + id + ":d=0;" + strings.Repeat("T", 500) + "\x1b\\")
		m.feed(time.Now(), payload, 0)
	}
	if len(m.osc99) > maxOSC99Pending {
		t.Fatalf("osc99 map %d", len(m.osc99))
	}
	for _, b := range m.osc99 {
		if len([]rune(b.title)) > 180 {
			t.Fatalf("title runes %d", len([]rune(b.title)))
		}
	}
}

func TestNoticeRenderCacheReusesPix(t *testing.T) {
	noticeMu.Lock()
	noticeLive = []liveNote{{
		deskNote: deskNote{Title: "cached", Body: "card"},
		born:     time.Now().Add(-time.Second),
	}}
	noticeCache = noticePixCache{}
	noticeMu.Unlock()
	defer func() {
		noticeMu.Lock()
		noticeLive = nil
		noticeCache = noticePixCache{}
		noticeMu.Unlock()
	}()
	now := time.Now()
	pix1, _, w1, h1, _, _ := renderNotices(now)
	pix2, _, w2, h2, _, _ := renderNotices(now)
	if len(pix1) == 0 || w1 != w2 || h1 != h2 {
		t.Fatalf("render size %d×%d vs %d×%d pix=%d", w1, h1, w2, h2, len(pix1))
	}
	if &pix1[0] != &pix2[0] {
		t.Fatal("expected cached notice pixels on a still card")
	}
}

func TestCommandFinishHiddenWhileWatching(t *testing.T) {
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
	var m termModes
	res := m.feed(time.Now(), []byte("\x1b]7879;done;1;ZmFpbA==\x07"), 0)
	if len(res.notes) != 1 || res.notes[0].Body != "failed (exit 1)" {
		t.Fatal(res.notes)
	}
	postDeskNote(res.notes[0], true, true)
	if noticeCount() != 0 {
		t.Fatal("card while the pane is watched")
	}
	postDeskNote(res.notes[0], false, true)
	if noticeCount() != 1 {
		t.Fatal("missing card for a background pane")
	}
	noticeMu.Lock()
	noticeLive = nil
	noticeMu.Unlock()
}
