//go:build windows || darwin

package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

func newSubmitTestTab() *tab {
	return &tab{
		alive:   atomicBool(true),
		term:    vt10x.New(vt10x.WithSize(120, 8)),
		sb:      newScrollback(),
		writeCh: make(chan []byte, 16),
	}
}

// queuedWrites drains what the tab handed its PTY writer. A nil item is a
// writer gap.
func queuedWrites(t *tab) [][]byte {
	var got [][]byte
	for {
		select {
		case b := <-t.writeCh:
			got = append(got, b)
		default:
			return got
		}
	}
}

func quoteWrites(ws [][]byte) []string {
	out := make([]string, len(ws))
	for i, b := range ws {
		if b == nil {
			out[i] = "<gap>"
		} else {
			out[i] = string(b)
		}
	}
	return out
}

// The shared Warp-bar / Windows MCP path. Claude Code on the alt screen with
// paste mode on gets the line bracketed, a gap, then Enter on its own.
func TestSendBarPayloadAltScreenProgram(t *testing.T) {
	pane := newSubmitTestTab()
	pane.ingestPTY([]byte("\x1b[?1049h\x1b[?2004h"), ptyHooks{})
	if !pane.altScreen() || !pane.modes.bracketPaste {
		t.Fatalf("setup alt=%v paste=%v", pane.altScreen(), pane.modes.bracketPaste)
	}
	line := strings.Repeat("x", 70)
	sendBarPayload(pane, line)
	got := quoteWrites(queuedWrites(pane))
	want := []string{"\x1b[200~" + line + "\x1b[201~", "<gap>", "\r"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("writes %q want %q", got, want)
	}
}

// An inline program (foreground child, no alt screen) without paste mode
// still gets text and Enter apart (macOS MCP submits reach this).
func TestSendBarPayloadInlineProgramUnbracketed(t *testing.T) {
	pane := newSubmitTestTab()
	pane.forceChild = true
	line := strings.Repeat("y", 70)
	sendBarPayload(pane, line)
	got := quoteWrites(queuedWrites(pane))
	want := []string{line, "<gap>", "\r"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("writes %q want %q", got, want)
	}
}

// The shell keeps one write and is never bracketed, even with the prompt's
// paste mode on.
func TestSendBarPayloadShellSingleWrite(t *testing.T) {
	pane := newSubmitTestTab()
	pane.ingestPTY([]byte("\x1b[?2004h"), ptyHooks{})
	if !pane.modes.bracketPaste {
		t.Fatal("setup: paste mode off")
	}
	sendBarPayload(pane, "ls\nl")
	got := quoteWrites(queuedWrites(pane))
	if len(got) != 1 || got[0] != "ls\rl\r" {
		t.Fatalf("writes %q", got)
	}
}

type writeLog struct{ ev []string }

func (w *writeLog) Write(p []byte) (int, error) {
	w.ev = append(w.ev, "write "+string(p))
	return len(p), nil
}

// The writer, not the UI thread, sleeps between text and Enter, in order
// with the writes around it.
func TestDrainWritesSleepsBeforeProgramEnter(t *testing.T) {
	var log writeLog
	prev := ptyGapSleep
	ptyGapSleep = func(d time.Duration) { log.ev = append(log.ev, "sleep "+d.String()) }
	defer func() { ptyGapSleep = prev }()

	pane := newSubmitTestTab()
	pane.forceChild = true
	sendBarPayload(pane, "hello")
	pane.sendKey([]byte("z")) // typed right after the submit
	close(pane.writeCh)
	pane.drainWrites(&log)
	want := []string{"write hello", "sleep " + submitEnterGap.String(), "write \r", "write z"}
	if strings.Join(log.ev, "|") != strings.Join(want, "|") {
		t.Fatalf("writer %q want %q", log.ev, want)
	}
	if submitEnterGap < 10*time.Millisecond {
		t.Fatalf("gap %v shorter than Claude Code's own 10ms", submitEnterGap)
	}
}
