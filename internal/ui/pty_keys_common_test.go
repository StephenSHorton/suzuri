//go:build windows || darwin

package ui

import (
	"bytes"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestEncodeArrowAltLeft(t *testing.T) {
	// Option+Left → CSI 1;3D (xterm Alt+Left) for Bubble Tea word motion.
	got := encodeArrow(nil, 'D', false, false, true, false, false)
	want := []byte("\x1b[1;3D")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	// Bare left, app cursor
	got = encodeArrow(nil, 'D', true, false, false, false, false)
	if !bytes.Equal(got, []byte("\x1bOD")) {
		t.Fatalf("app cursor left: %q", got)
	}
}

func TestEncodeMouseButtonSGR(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	// No mouse mode → nil
	if encodeMouseButton(term, 5, 10, 0, true) != nil {
		t.Fatal("expected nil without mouse tracking")
	}
	_, _ = term.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	press := encodeMouseButton(term, 5, 10, 0, true)
	if string(press) != "\x1b[<0;5;10M" {
		t.Fatalf("press: %q", press)
	}
	rel := encodeMouseButton(term, 5, 10, 0, false)
	if string(rel) != "\x1b[<0;5;10m" {
		t.Fatalf("release: %q", rel)
	}
}

func TestEncodeMouseMotionSGR(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	// Press-only (1000): no motion reports.
	_, _ = term.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	if encodeMouseMotion(term, 3, 4, false) != nil {
		t.Fatal("1000-only should not report free motion")
	}
	// Any-event (1003): hover motion with no button → 35.
	_, _ = term.Write([]byte("\x1b[?1003h"))
	got := encodeMouseMotion(term, 3, 4, false)
	if string(got) != "\x1b[<35;3;4M" {
		t.Fatalf("any-event hover: %q", got)
	}
	// Drag motion with left down → 32.
	got = encodeMouseMotion(term, 3, 4, true)
	if string(got) != "\x1b[<32;3;4M" {
		t.Fatalf("drag: %q", got)
	}
	// 1002 only: free motion nil, drag ok.
	term2 := vt10x.New(vt10x.WithSize(80, 24))
	_, _ = term2.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	if encodeMouseMotion(term2, 1, 1, false) != nil {
		t.Fatal("1002 free motion should be nil")
	}
	if string(encodeMouseMotion(term2, 1, 1, true)) != "\x1b[<32;1;1M" {
		t.Fatalf("1002 drag: %q", encodeMouseMotion(term2, 1, 1, true))
	}
}
