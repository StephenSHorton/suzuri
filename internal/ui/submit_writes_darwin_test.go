//go:build darwin

package ui

import (
	"strings"
	"testing"
)

// MCP submit on macOS goes through the same split as the bar: a long line to
// Claude Code on the alt screen must not reach the PTY as one "line\r".
func TestMacSubmitOnUIThreadSplitsProgramSubmit(t *testing.T) {
	pane := newSubmitTestTab()
	pane.id = 4
	pane.ingestPTY([]byte("\x1b[?1049h\x1b[?2004h"), ptyHooks{})
	u := &macUI{tabs: []*tab{pane}, cols: 120, rows: 8}
	line := strings.Repeat("m", 80)
	u.submitOnUIThread(4, line, nil)
	got := quoteWrites(queuedWrites(pane))
	want := []string{"\x1b[200~" + line + "\x1b[201~", "<gap>", "\r"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("writes %q want %q", got, want)
	}
}

// The shell still gets the one write its echo filter expects.
func TestMacSubmitOnUIThreadShellSingleWrite(t *testing.T) {
	pane := newSubmitTestTab()
	pane.id = 4
	pane.ingestPTY([]byte("\x1b[?2004h"), ptyHooks{})
	u := &macUI{tabs: []*tab{pane}, cols: 120, rows: 8}
	u.submitOnUIThread(4, "echo hi", nil)
	got := quoteWrites(queuedWrites(pane))
	if len(got) != 1 || got[0] != "echo hi\r" {
		t.Fatalf("writes %q", got)
	}
}
