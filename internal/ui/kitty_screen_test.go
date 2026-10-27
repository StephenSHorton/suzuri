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
