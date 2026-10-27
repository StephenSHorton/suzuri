//go:build windows || darwin

package ui

import (
	"fmt"
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
	if !tab.modes.bracketPaste || tab.modes.syncOn || len(tab.modes.syncBuf) != 0 {
		t.Fatal("leaveApp should drop sync output and keep the shell paste mode")
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

// claudeInlineFrame is what Claude Code's inline renderer paints on the main
// screen: transcript rows in a DECSTBM region at the top and its live footer
// (input box, status line, mode line) on fixed rows below, all placed by CUP.
func claudeInlineFrame(cols, rows int, transcript []string) []byte {
	footer := []string{
		strings.Repeat("─", cols-9) + " chat-2 ─",
		"❯ Woah it",
		strings.Repeat("─", cols),
		"  Opus 5.5 ctx ░░░░ 45% │ $1337 ⟲19m ↑ v2.1.311",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle)",
	}
	content := rows - len(footer)
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[?2026h\x1b[1;%dr", content)
	for i := 0; i < content; i++ {
		line := ""
		if j := len(transcript) - content + i; j >= 0 {
			line = transcript[j]
		}
		fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[0m\x1b[K", i+1, line)
	}
	for i, line := range footer {
		fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[0m\x1b[K", content+i+1, line)
	}
	b.WriteString("\x1b[?2026l")
	return []byte(b.String())
}

// claudeResizeWipe is Claude Code's inline resize handler for a width change
// or a row shrink: reset the region, erase the screen and the saved lines,
// home. Later frames replay its transcript into scrollback.
func claudeResizeWipe(rows int) []byte {
	return []byte(fmt.Sprintf("\x1b[r\x1b[2J\x1b[3J\x1b[H\x1b[1;%dr", rows-5))
}

func historyHas(sb *scrollback, sub string) bool {
	for _, hl := range sb.lines {
		if strings.Contains(hl.text, sub) {
			return true
		}
	}
	return false
}

func TestClaudeResizeWipeDoesNotStrandFooter(t *testing.T) {
	var transcript []string
	for i := 0; i < 80; i++ {
		transcript = append(transcript, fmt.Sprintf("transcript line %02d", i))
	}
	cases := []struct {
		name        string
		from, to    [2]int
		frameBefore bool
	}{
		// Width change (field trail 18:50:28, 107x32 -> 53x32).
		{name: "width", from: [2]int{107, 32}, to: [2]int{53, 32}},
		// Row shrink (18:58:23, 107x65 -> 107x32). A frame lands between the
		// resize and the wipe, so the wipe is diffed against the shrunk grid.
		{name: "rowShrink", from: [2]int{107, 65}, to: [2]int{107, 32}, frameBefore: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := vt10x.New(vt10x.WithSize(tc.from[0], tc.from[1]))
			tb := &tab{term: term, sb: newScrollback()}
			tb.ingestPTY(claudeInlineFrame(tc.from[0], tc.from[1], transcript), ptyHooks{})
			tb.resize(tc.to[0], tc.to[1])
			if tc.frameBefore {
				tb.ingestPTY(claudeInlineFrame(tc.from[0], tc.from[1], transcript), ptyHooks{})
			}
			tb.ingestPTY(claudeResizeWipe(tc.to[1]), ptyHooks{})
			tb.ingestPTY(claudeInlineFrame(tc.to[0], tc.to[1], transcript), ptyHooks{})
			if historyHas(tb.sb, "bypass permissions") || historyHas(tb.sb, "chat-2") {
				var got []string
				for _, hl := range tb.sb.lines {
					got = append(got, hl.text)
				}
				t.Fatalf("live footer stranded in history after the CSI 3J wipe:\n%s", strings.Join(got, "\n"))
			}
			live := strings.Join(snapshotScreenText(term), "\n")
			if strings.Count(live, "bypass permissions") != 1 {
				t.Fatalf("live screen should hold one footer:\n%s", live)
			}
		})
	}
}

func TestShellClearKeepsScreenInHistory(t *testing.T) {
	// What was on screen stays reachable after a shell clear, CSI 3J or not.
	for _, c := range []struct{ name, seq string }{
		{"zsh Ctrl+L", "\x1b[H\x1b[2J"},
		{"macOS clear", "\x1b[3J\x1b[H\x1b[2J"},
		{"Linux clear", "\x1b[H\x1b[2J\x1b[3J"},
	} {
		term := vt10x.New(vt10x.WithSize(40, 6))
		tb := &tab{term: term, sb: newScrollback()}
		tb.ingestPTY([]byte("$ make\r\nbuild ok\r\n$ "), ptyHooks{})
		tb.ingestPTY([]byte(c.seq), ptyHooks{})
		if !historyHas(tb.sb, "build ok") {
			t.Errorf("%s should keep the screen in history: %+v", c.name, tb.sb.lines)
		}
	}
}
