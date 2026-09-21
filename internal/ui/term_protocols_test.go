//go:build windows || darwin

package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

func TestFeedSyncOutputHoldsUntilEnd(t *testing.T) {
	var m termModes
	now := time.Now()
	held := m.feed(now, []byte("\x1b[?2026hHELLO"), 0)
	if len(held.ready) != 0 {
		t.Fatalf("ready during sync: %q", held.ready)
	}
	if !m.syncOn {
		t.Fatal("sync not armed")
	}
	rel := m.feed(now, []byte("!\x1b[?2026lTAIL"), 0)
	if string(rel.ready) != "HELLO!TAIL" {
		t.Fatalf("flush: %q", rel.ready)
	}
	if m.syncOn {
		t.Fatal("sync still on")
	}
}

func TestFeedSyncTimeoutFlushes(t *testing.T) {
	var m termModes
	now := time.Now()
	m.feed(now, []byte("\x1b[?2026hSTUCK"), 0)
	got := m.feed(now.Add(2*time.Second), []byte("X"), 0)
	if string(got.ready) != "STUCKX" {
		t.Fatalf("timeout flush: %q", got.ready)
	}
}

func TestFeedDECRQM(t *testing.T) {
	var m termModes
	m.bracketPaste = true
	res := m.feed(time.Now(), []byte("\x1b[?2026$p\x1b[?2004$p\x1b[?9999$p"), vt10x.ModeWrap)
	s := string(res.replies)
	if !strings.Contains(s, "\x1b[?2026;2$y") {
		t.Fatalf("2026 reply: %q", s)
	}
	if !strings.Contains(s, "\x1b[?2004;1$y") {
		t.Fatalf("2004 reply: %q", s)
	}
	if !strings.Contains(s, "\x1b[?9999;0$y") {
		t.Fatalf("unknown reply: %q", s)
	}
	on := m.feed(time.Now(), []byte("\x1b[?2026h\x1b[?2026$p"), 0)
	if !strings.Contains(string(on.replies), "\x1b[?2026;1$y") {
		t.Fatalf("2026 set reply: %q", on.replies)
	}
}

func TestFeedBracketAndCursorAndVersion(t *testing.T) {
	var m termModes
	res := m.feed(time.Now(), []byte("\x1b[?2004h\x1b[5 q\x1b[>0q"), 0)
	if !m.bracketPaste {
		t.Fatal("2004 not set")
	}
	if m.cursorShape != 5 {
		t.Fatalf("shape %d", m.cursorShape)
	}
	if !strings.Contains(string(res.replies), "Suzuri") {
		t.Fatalf("version: %q", res.replies)
	}
	style, steady := m.shellCursor(0)
	if style != 2 || steady {
		t.Fatalf("bar cursor style=%d steady=%v", style, steady)
	}
	if string(framePaste("a\nb", true)) != "\x1b[200~a\rb\x1b[201~" {
		t.Fatal("bracketed frame")
	}
	if string(framePaste("a\nb", false)) != "a\rb" {
		t.Fatal("raw frame")
	}
}

func TestFeedOSC52(t *testing.T) {
	var m termModes
	// "hi" base64
	res := m.feed(time.Now(), []byte("\x1b]52;c;aGk=\x07NEXT"), 0)
	if res.clipSet == nil || *res.clipSet != "hi" {
		t.Fatalf("clip: %#v", res.clipSet)
	}
	if string(res.ready) != "NEXT" {
		t.Fatalf("ready: %q", res.ready)
	}
	ask := m.feed(time.Now(), []byte("\x1b]52;c;?\x07"), 0)
	if !ask.clipAsk {
		t.Fatal("expected query")
	}
	// Split across chunks.
	var m2 termModes
	part := m2.feed(time.Now(), []byte("\x1b]52;c;aGk="), 0)
	if part.clipSet != nil || len(part.ready) != 0 {
		t.Fatalf("partial leaked: %#v %q", part.clipSet, part.ready)
	}
	done := m2.feed(time.Now(), []byte("\x07Z"), 0)
	if done.clipSet == nil || *done.clipSet != "hi" || string(done.ready) != "Z" {
		t.Fatalf("reassembled: clip=%v ready=%q", done.clipSet, done.ready)
	}
}

func TestFeedOSC8AndNotifyProgress(t *testing.T) {
	var m termModes
	raw := []byte("\x1b]8;;file:///tmp/a\x07alpha.txt\x1b]8;;\x07")
	res := m.feed(time.Now(), raw, 0)
	if string(res.ready) != "alpha.txt" {
		t.Fatalf("text: %q", res.ready)
	}
	if len(res.spans) != 1 || res.spans[0].url != "file:///tmp/a" || res.spans[0].text != "alpha.txt" {
		t.Fatalf("span: %#v", res.spans)
	}
	note := m.feed(time.Now(), []byte("\x1b]9;build finished\x07"), 0)
	if len(note.notes) != 1 || note.notes[0].Body != "build finished" {
		t.Fatalf("notify: %+v", note.notes)
	}
	m.feed(time.Now(), []byte("\x1b]9;4;1;40\x07"), 0)
	if m.progress.kind != 1 || m.progress.pct != 40 {
		t.Fatalf("progress: %+v", m.progress)
	}
	m.feed(time.Now(), []byte("\x1b]777;notify;Done;shipped\x07"), 0)
}

func TestOSC8StampSurvivesScroll(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 8))
	var m termModes
	raw := []byte("\x1b]8;;file:///tmp/a\x07alpha.txt\x1b]8;;\x07\r\n")
	res := m.feed(time.Now(), raw, 0)
	before := snapshotScreenText(term)
	_, _ = term.Write(res.ready)
	m.observe(before, snapshotScreenText(term), res.spans)
	if got := m.links[0][0]; got != "file:///tmp/a" {
		t.Fatalf("link at origin: %q grid=%v", got, m.links[0])
	}

	// Distinct rows, then two scrolls off the bottom. The link on row 2
	// should land on row 0.
	term = vt10x.New(vt10x.WithSize(20, 8))
	m.clearLinks()
	_, _ = term.Write([]byte("r0\r\nr1\r\nr2\r\nr3\r\nr4\r\nr5\r\nr6\r\nr7"))
	m.ensure(8, 20)
	m.links[2][0] = "file:///tmp/a"
	before = snapshotScreenText(term)
	_, _ = term.Write([]byte("\r\nA\r\nB"))
	m.observe(before, snapshotScreenText(term), nil)
	if m.links[0][0] != "file:///tmp/a" {
		t.Fatalf("link did not follow scroll: row0=%q row2=%q", m.links[0][0], m.links[2][0])
	}
}

func TestFindLinksUsesCellLink(t *testing.T) {
	row := make([]cellPix, 12)
	for i, r := range []rune("README.md") {
		row[i] = cellPix{Ch: r, Link: "file:///tmp/README.md"}
	}
	spans := findLinksInGrid([][]cellPix{row})
	if len(spans) != 1 || spans[0].url != "file:///tmp/README.md" || spans[0].x1 != 9 {
		t.Fatalf("spans: %#v", spans)
	}
}

func TestFeedPassesThroughTitleAndCwd(t *testing.T) {
	var m termModes
	raw := []byte("\x1b]0;title\x07\x1b]7878;cwd=/tmp\x07x")
	res := m.feed(time.Now(), raw, 0)
	if string(res.ready) != string(raw) {
		t.Fatalf("dropped host OSC: %q", res.ready)
	}
}

func TestFocusArmDoesNotRequireModeYet(t *testing.T) {
	var m termModes
	res := m.feed(time.Now(), []byte("\x1b[?1004h"), 0)
	if !res.armFocus {
		t.Fatal("expected focus arm")
	}
	if !strings.Contains(string(res.ready), "\x1b[?1004h") {
		t.Fatalf("1004 should pass through: %q", res.ready)
	}
}
