//go:build windows

package ui

import (
	"testing"

	"github.com/hinshun/vt10x"
)

func TestVT10xAltScreenMode(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	if _, err := term.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}
	if term.Mode()&vt10x.ModeAltScreen == 0 {
		t.Fatal("expected ModeAltScreen after ?1049h")
	}
	if _, err := term.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	if term.Mode()&vt10x.ModeAltScreen != 0 {
		t.Fatal("expected main screen after ?1049l")
	}
}

func TestLiveExtentFullOnAltScreen(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 12))
	_, _ = term.Write([]byte("\x1b[?1049h"))
	if n := liveExtent(term); n != 12 {
		t.Fatalf("alt liveExtent=%d want 12", n)
	}
}
