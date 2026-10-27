//go:build darwin

package ui

import (
	"strings"
	"testing"
)

// Partial wiring: Update's handleTextInput and the flush after it run for
// real against an alt-screen pane. handleKeys reads ebiten key state, so the
// Enter it would hold is held by hand here.
func TestTickKeysHeldEnterFollowsTickRunes(t *testing.T) {
	prev := tickInputChars
	tickInputChars = func(dst []rune) []rune { return append(dst, 'a', 'b', 'c') }
	defer func() { tickInputChars = prev }()

	pane := newSubmitTestTab()
	pane.ingestPTY([]byte("\x1b[?1049h"), ptyHooks{})
	u := &macUI{tabs: []*tab{pane}, cols: 120, rows: 8}
	u.tickKeys.hold([]byte{'\r'}) // handleKeys: Enter this tick
	u.handleTextInput()
	u.tickKeys.flush(u.sendKey)
	got := strings.Join(quoteWrites(queuedWrites(pane)), "")
	if got != "abc\r" {
		t.Fatalf("pty saw %q want %q", got, "abc\r")
	}
}
