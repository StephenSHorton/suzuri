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
