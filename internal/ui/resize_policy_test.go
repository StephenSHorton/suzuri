//go:build windows || darwin

package ui

import (
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

// Regression: alt-screen alone must not permanently block PTY resize (letterbox
// after split). Only recent PTY I/O defers ResizePseudoConsole.
func TestConPtyResizeOKAltScreenWithoutRecentIO(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(100, 40))
	if _, err := term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}
	tab := &tab{term: term}
	tab.alive.Store(true)
	// Stale I/O + titleBusy must still allow resize (Grok spinner lasts forever).
	tab.lastIOUnixNano.Store(time.Now().Add(-5 * time.Second).UnixNano())
	tab.titleBusy.Store(true)
	if !tab.altScreen() {
		t.Fatal("expected alt screen")
	}
	if !tab.conPtyResizeOK() {
		t.Fatal("conPtyResizeOK must allow resize when alt-screen but I/O quiet")
	}
}

func TestConPtyResizeOKBlocksRecentIO(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(100, 40))
	if _, err := term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}
	tab := &tab{term: term}
	tab.alive.Store(true)
	tab.noteIO() // hot stream — host should defer settle
	if tab.conPtyResizeOK() {
		t.Fatal("conPtyResizeOK must block while PTY I/O is recent")
	}
	if !paneHasRecentIO(tab, conPtyIOQuiet) {
		t.Fatal("paneHasRecentIO should be true right after noteIO")
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
