//go:build windows || darwin

package ui

import (
	"testing"

	"github.com/hinshun/vt10x"
)

func newKittyTestTab() *tab {
	return &tab{term: vt10x.New(vt10x.WithSize(40, 8)), sb: newScrollback()}
}

// The spec keeps one flags stack per screen. An app that pushes on the alt
// screen and leaves without popping must not leak its flags onto the shell.
func TestKittyAltScreenHasOwnStack(t *testing.T) {
	tab := newKittyTestTab()
	tab.ingestPTY([]byte("\x1b[>1u"), ptyHooks{}) // main screen app
	tab.ingestPTY([]byte("\x1b[?1049h\x1b[>13u"), ptyHooks{})
	if tab.kitty.flags != 13 {
		t.Fatalf("alt flags=%d want 13", tab.kitty.flags)
	}
	tab.ingestPTY([]byte("\x1b[?1049l"), ptyHooks{}) // left without popping
	if tab.kitty.flags != 1 {
		t.Fatalf("main flags after leaving alt=%d want 1", tab.kitty.flags)
	}
	// Re-entering alt starts from a clean alt stack.
	tab.ingestPTY([]byte("\x1b[?1049h"), ptyHooks{})
	if tab.kitty.flags != 0 {
		t.Fatalf("fresh alt flags=%d want 0", tab.kitty.flags)
	}
	tab.ingestPTY([]byte("\x1b[?1049l\x1b[<u"), ptyHooks{})
	if tab.kitty.flags != 0 || tab.kitty.active() {
		t.Fatalf("main after pop flags=%d", tab.kitty.flags)
	}
}

// RIS (ESC c) resets the terminal, keyboard flags and stacks included.
func TestKittyResetOnRIS(t *testing.T) {
	tab := newKittyTestTab()
	tab.ingestPTY([]byte("\x1b[>1u\x1b[>5u"), ptyHooks{})
	tab.ingestPTY([]byte("\x1bc"), ptyHooks{})
	if tab.kitty.flags != 0 || len(tab.kitty.stack) != 0 {
		t.Fatalf("after RIS flags=%d stack=%v", tab.kitty.flags, tab.kitty.stack)
	}
	// A pop after the reset has nothing stale to restore.
	tab.ingestPTY([]byte("\x1b[<u"), ptyHooks{})
	if tab.kitty.flags != 0 {
		t.Fatalf("pop after RIS flags=%d", tab.kitty.flags)
	}
}

// A read can end right after the ESC of an RIS. Through ingestPTY the mode
// tracker already holds that ESC; the scanner holds it too when called on
// raw reads.
func TestKittyRISSplitAcrossReads(t *testing.T) {
	tab := newKittyTestTab()
	tab.ingestPTY([]byte("\x1b[>1u"), ptyHooks{})
	tab.ingestPTY([]byte("\x1b]9;4;1;50\x07abc\x1b"), ptyHooks{})
	tab.ingestPTY([]byte("c"), ptyHooks{})
	if tab.kitty.flags != 0 || len(tab.kitty.stack) != 0 {
		t.Fatalf("ingest split RIS flags=%d stack=%v", tab.kitty.flags, tab.kitty.stack)
	}

	var kk kittyKeyboard
	kk.consumeHostQueries([]byte("\x1b[>1u"))
	kk.consumeHostQueries([]byte("abc\x1b"))
	kk.consumeHostQueries([]byte("c"))
	if kk.flags != 0 || len(kk.stack) != 0 {
		t.Fatalf("raw split RIS flags=%d stack=%v", kk.flags, kk.stack)
	}
}

// tmux passthrough wraps an inner terminal's bytes in a DCS and doubles its
// ESCs. Neither an inner RIS nor an inner push is ours, in one read or split.
func TestKittyIgnoresStringBodies(t *testing.T) {
	cases := []struct {
		name  string
		reads []string
	}{
		{"ris", []string{"\x1bPtmux;\x1b\x1bc\x1b\\"}},
		{"ris split at ESC", []string{"\x1bPtmux;\x1b", "\x1bc\x1b\\"}},
		{"push", []string{"\x1bPtmux;\x1b\x1b[>13u\x1b\\"}},
		{"push split before ST", []string{"\x1bPtmux;\x1b\x1b[>13u", "\x1b\\"}},
		{"push in APC", []string{"\x1b_Ga=q;\x1b\x1b[>13u", "AAAA\x1b\\"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab := newKittyTestTab()
			tab.ingestPTY([]byte("\x1b[>1u"), ptyHooks{})
			for _, r := range c.reads {
				tab.ingestPTY([]byte(r), ptyHooks{})
			}
			if tab.kitty.flags != 1 || len(tab.kitty.stack) != 1 {
				t.Fatalf("flags=%d stack=%v want 1 [0]", tab.kitty.flags, tab.kitty.stack)
			}
			// The body ended: what follows is ours again.
			tab.ingestPTY([]byte("\x1b[<u"), ptyHooks{})
			if tab.kitty.flags != 0 {
				t.Fatalf("pop after body flags=%d", tab.kitty.flags)
			}
		})
	}
}

// A body whose ST is split across reads ends there, and what follows is
// scanned again. So is a CSI cut before its final byte.
func TestKittyStringEndSplitAcrossReads(t *testing.T) {
	tab := newKittyTestTab()
	tab.ingestPTY([]byte("\x1b]0;title\x1b"), ptyHooks{})
	tab.ingestPTY([]byte("\\\x1b[>1u"), ptyHooks{})
	if tab.kitty.flags != 1 {
		t.Fatalf("push after split ST flags=%d", tab.kitty.flags)
	}
	tab.ingestPTY([]byte("\x1b[<"), ptyHooks{})
	tab.ingestPTY([]byte("u"), ptyHooks{})
	if tab.kitty.flags != 0 {
		t.Fatalf("split pop flags=%d", tab.kitty.flags)
	}
}
