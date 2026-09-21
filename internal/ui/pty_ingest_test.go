//go:build windows || darwin

package ui

import (
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestIngestPTYSharedPipeline(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 8))
	tab := &tab{term: term, sb: newScrollback()}
	var gotPrev, gotNext string
	raw := []byte("\x1b]7878;cwd=/tmp/work\x07\x1b]8;;file:///tmp/work/a\x07alpha\x1b]8;;\x07\r\n")
	res := tab.ingestPTY(raw, ptyHooks{
		OnCwd: func(prev, next string) {
			gotPrev, gotNext = prev, next
		},
	})
	if res.Action != ptyFrame {
		t.Fatalf("action %d", res.Action)
	}
	if gotNext != "/tmp/work" {
		t.Fatalf("cwd %q (prev %q)", gotNext, gotPrev)
	}
	if tab.modes.links == nil || tab.modes.links[0][0] != "file:///tmp/work/a" {
		t.Fatalf("link not stamped: %#v", tab.modes.links)
	}
	screen := snapshotScreenText(term)
	if !strings.Contains(screen[0], "alpha") {
		t.Fatalf("grid: %q", screen[0])
	}
	// A second chunk that is only a sync hold must not be reported as a frame.
	held := tab.ingestPTY([]byte("\x1b[?2026hpartial"), ptyHooks{})
	if held.Action != ptyQuiet {
		t.Fatalf("sync hold action %d ready grid %q", held.Action, snapshotScreenText(term)[0])
	}
}

func TestIngestPTYTitleReachesHost(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 6))
	tab := &tab{term: term, sb: newScrollback()}
	res := tab.ingestPTY([]byte("\x1b]0;workspace\x07"), ptyHooks{})
	if res.Action != ptyFrame || !res.TitleChanged || res.Title != "workspace" {
		t.Fatalf("title result: %+v tab=%q", res, tab.title)
	}
}

func TestLeaveAppDropsHeldSync(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 6))
	tab := &tab{term: term, sb: newScrollback()}
	tab.ingestPTY([]byte("\x1b[?2004h\x1b[?2026hHELD"), ptyHooks{})
	if !tab.modes.bracketPaste || !tab.modes.syncOn {
		t.Fatal("modes not armed")
	}
	tab.modes.leaveApp()
	if tab.modes.bracketPaste || tab.modes.syncOn || len(tab.modes.syncBuf) != 0 {
		t.Fatal("leaveApp left protocol state")
	}
	res := tab.ingestPTY([]byte("after\r\n"), ptyHooks{})
	if res.Action != ptyFrame {
		t.Fatalf("action %d", res.Action)
	}
	screen := snapshotScreenText(term)
	if strings.Contains(screen[0], "HELD") || !strings.Contains(screen[0], "after") {
		t.Fatalf("grid: %q", screen[0])
	}
}
