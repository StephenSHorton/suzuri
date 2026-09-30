package ui

import (
	"testing"
	"time"
)

func TestShortTitleStripsGrokSpinner(t *testing.T) {
	cases := []struct{ in, want string }{
		{"⠿ Grok Build", "Grok Build"},
		{"⣿grok", "grok"},
		{"⠋  thinking", "thinking"},
		{"  ⣷  suzuri", "suzuri"},
		{"xargs", "xargs"},
		{"* Grok", "Grok"},
		{"C:\\Users\\foo\\bar", "bar"},
		{"● circle title", "circle title"},
	}
	for _, tc := range cases {
		if got := shortTitle(tc.in); got != tc.want {
			t.Fatalf("shortTitle(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestRestingMeansAliveAndQuiet(t *testing.T) {
	var dead tab
	if dead.resting() {
		t.Fatal("dead pane is not resting")
	}
	var live tab
	live.alive.Store(true)
	if !live.resting() {
		t.Fatal("fresh live pane should rest")
	}
	live.noteIO()
	if live.resting() {
		t.Fatal("recent output is busy, not resting")
	}
	live.lastIOUnixNano.Store(time.Now().Add(-3 * time.Second).UnixNano())
	if !live.resting() {
		t.Fatal("quiet pane should rest again")
	}
	live.titleBusy.Store(true)
	if live.resting() {
		t.Fatal("title spinner keeps the pane busy")
	}
}

func TestPaneBusyFramesAreDots(t *testing.T) {
	want := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if len(paneBusyFrames) != len(want) {
		t.Fatalf("len %d", len(paneBusyFrames))
	}
	for i, f := range paneBusyFrames {
		if f != want[i] || len([]rune(f)) != 1 {
			t.Fatalf("frame %d = %q", i, f)
		}
	}
	got := paneBusyMark()
	for _, f := range paneBusyFrames {
		if got == f {
			return
		}
	}
	t.Fatalf("paneBusyMark %q is not a dots frame", got)
}

func TestIdleLampSitsInTitle(t *testing.T) {
	lamp := idleLampAt(10, 20, 200, 18)
	if !lamp.ok {
		t.Fatal("expected a lamp")
	}
	if lamp.cx-lamp.r < 10 || lamp.cx+lamp.r >= 210 {
		t.Fatalf("lamp x out of strip: %+v", lamp)
	}
	if lamp.cy-lamp.r < 20 || lamp.cy+lamp.r >= 38 {
		t.Fatalf("lamp y out of strip: %+v", lamp)
	}
	if idleLampReserve(lamp) < lamp.r*2 {
		t.Fatal("reserve should clear the ring")
	}
	if idleLampAt(0, 0, 20, 18).ok {
		t.Fatal("narrow strip should skip the ring")
	}
}

func TestTitleReportsBusy(t *testing.T) {
	if !titleReportsBusy("⠋ Grok") {
		t.Fatal("spinner frame should report busy")
	}
	if !titleReportsBusy("⣾ thinking…") {
		t.Fatal("dots2 frame should report busy")
	}
	if titleReportsBusy("⠿ Grok") {
		t.Fatal("static ⠿ is idle badge, not busy")
	}
	if titleReportsBusy("Grok Build") {
		t.Fatal("plain title is not busy")
	}
	if titleReportsBusy("C:\\Users\\foo") {
		t.Fatal("path is not busy")
	}
}
