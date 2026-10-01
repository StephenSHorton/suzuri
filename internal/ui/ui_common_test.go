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

func TestReportedAgentStateOverridesGuess(t *testing.T) {
	var live tab
	live.alive.Store(true)
	live.titleBusy.Store(true)
	live.noteIO()
	if !live.setAgentState("idle") {
		t.Fatal("idle should be accepted")
	}
	if !live.resting() || live.busy() || live.blocked() {
		t.Fatal("reported idle rests even while the title spinner and recent output say working")
	}
	if live.activity() != "idle" || live.agentState() != "idle" {
		t.Fatalf("state=%q activity=%q", live.agentState(), live.activity())
	}
	if !live.setAgentState("working") || !live.busy() || live.resting() {
		t.Fatal("reported working stays busy after the pane goes quiet")
	}
	live.lastIOUnixNano.Store(0)
	live.titleBusy.Store(false)
	if !live.busy() {
		t.Fatal("reported working does not need PTY output")
	}
	if !live.setAgentState("blocked") || !live.blocked() || live.busy() || live.resting() {
		t.Fatal("blocked is waiting on a person, not a spinner and not the idle ring")
	}
	if live.activity() != "blocked" {
		t.Fatalf("activity=%q", live.activity())
	}
	if !live.setAgentState("done") || !live.resting() || live.agentState() != "done" {
		t.Fatal("done is idle that has not been looked at; the pane rests")
	}
	live.titleBusy.Store(true)
	if !live.setAgentState("clear") {
		t.Fatal("clear")
	}
	if live.agentState() != "" || live.resting() || !live.busy() {
		t.Fatal("clear restores the title and PTY guess")
	}
	if live.setAgentState("waiting") {
		t.Fatal("waiting is a workspace presence code, not a pane lifecycle")
	}
}

func TestPaneBusyFramesAreDots(t *testing.T) {
	want := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	got := paneBusyMark()
	if len([]rune(got)) != 1 {
		t.Fatalf("frame %q is not one column", got)
	}
	for _, f := range want {
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
