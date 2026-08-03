package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestEncodeMouseWheelArrows(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	// No mouse tracking → arrows.
	up := encodeMouseWheel(term, 1, 1, 2)
	if !bytes.Equal(up, []byte("\x1b[A\x1b[A")) {
		t.Fatalf("up arrows: %q", up)
	}
	down := encodeMouseWheel(term, 1, 1, -3)
	if !bytes.Equal(down, []byte("\x1b[B\x1b[B\x1b[B")) {
		t.Fatalf("down arrows: %q", down)
	}
	if encodeMouseWheel(term, 1, 1, 0) != nil {
		t.Fatal("zero steps should be nil")
	}
}

func TestEncodeMouseWheelSGR(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	// Enable button + SGR reporting (as Bubble Tea / Grok typically do).
	_, _ = term.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	if !mouseTracking(term) || !mouseSGR(term) {
		t.Fatalf("expected mouse tracking+SGR mode=%#x", term.Mode())
	}
	up := encodeMouseWheel(term, 10, 5, 1)
	if string(up) != "\x1b[<64;10;5M" {
		t.Fatalf("SGR wheel up: %q", up)
	}
	down := encodeMouseWheel(term, 3, 7, -1)
	if string(down) != "\x1b[<65;3;7M" {
		t.Fatalf("SGR wheel down: %q", down)
	}
}

func TestEncodeMouseWheelCap(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(40, 12))
	out := encodeMouseWheel(term, 1, 1, 100)
	// Cap at 32 arrows.
	if n := strings.Count(string(out), "\x1b[A"); n != 32 {
		t.Fatalf("expected 32 up arrows, got %d (len=%d)", n, len(out))
	}
}
