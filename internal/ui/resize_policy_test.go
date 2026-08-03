//go:build windows || darwin

package ui

import (
	"testing"

	"github.com/hinshun/vt10x"
)

// Regression: alt-screen TUIs (Grok, vim) used to permanently block PTY resize,
// so after a pane split the app kept the full-window cols×rows (letterboxed).
func TestConPtyResizeOKAllowsBusyAltScreen(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(100, 40))
	if _, err := term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}
	tab := &tab{term: term}
	tab.alive.Store(true)
	tab.noteIO() // recent I/O must not block layout resize either
	if !tab.altScreen() {
		t.Fatal("expected alt screen")
	}
	if !tab.conPtyResizeOK() {
		t.Fatal("conPtyResizeOK must allow resize while alt-screen (split reflow)")
	}
}

func TestTabResizeUpdatesLastSize(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(100, 40))
	tab := &tab{term: term, lastCols: 100, lastRows: 40}
	tab.alive.Store(true)
	tab.resize(50, 20)
	if tab.lastCols != 50 || tab.lastRows != 20 {
		t.Fatalf("last size %d×%d want 50×20", tab.lastCols, tab.lastRows)
	}
	c, r := term.Size()
	if c != 50 || r != 20 {
		t.Fatalf("term size %d×%d want 50×20", c, r)
	}
	// No-op when unchanged.
	tab.resize(50, 20)
	if tab.lastCols != 50 || tab.lastRows != 20 {
		t.Fatalf("no-op changed size to %d×%d", tab.lastCols, tab.lastRows)
	}
}
