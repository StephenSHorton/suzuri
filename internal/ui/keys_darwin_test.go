//go:build darwin

package ui

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestPtyKeyFromEbitenCtrlLetters(t *testing.T) {
	// Grok draft undo is Ctrl+Z → C0 SUB (0x1a). Host must emit this on alt-screen.
	b := ptyKeyFromEbiten(nil, nil, ebiten.KeyZ, true, false, false, false)
	if len(b) != 1 || b[0] != 0x1a {
		t.Fatalf("Ctrl+Z = %v want [0x1a]", b)
	}
	// Ctrl+V (Grok cancel) → 0x16
	b = ptyKeyFromEbiten(nil, nil, ebiten.KeyV, true, false, false, false)
	if len(b) != 1 || b[0] != 0x16 {
		t.Fatalf("Ctrl+V = %v want [0x16]", b)
	}
	// Ctrl+U → 0x15
	b = ptyKeyFromEbiten(nil, nil, ebiten.KeyU, true, false, false, false)
	if len(b) != 1 || b[0] != 0x15 {
		t.Fatalf("Ctrl+U = %v want [0x15]", b)
	}
	// High-traffic Grok agent chords (must be C0, not dropped by the host).
	for _, tc := range []struct {
		key  ebiten.Key
		want byte
		name string
	}{
		{ebiten.KeyP, 0x10, "Ctrl+P palette"},
		{ebiten.KeyK, 0x0b, "Ctrl+K scroll/kill"},
		{ebiten.KeyW, 0x17, "Ctrl+W delete-word"},
		{ebiten.KeyO, 0x0f, "Ctrl+O YOLO"},
		{ebiten.KeyN, 0x0e, "Ctrl+N new session"},
		{ebiten.KeyQ, 0x11, "Ctrl+Q quit"},
		{ebiten.KeyS, 0x13, "Ctrl+S sessions"},
		{ebiten.KeyT, 0x14, "Ctrl+T todos"},
		{ebiten.KeyB, 0x02, "Ctrl+B background"},
		{ebiten.KeyL, 0x0c, "Ctrl+L extensions/interject"},
		{ebiten.KeyX, 0x18, "Ctrl+X shortcuts alt"},
	} {
		got := ptyKeyFromEbiten(nil, nil, tc.key, true, false, false, false)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s = %v want [0x%02x]", tc.name, got, tc.want)
		}
	}
	// Super+Z must not be a C0 control from ptyKeyFromEbiten (host maps Cmd+Z separately).
	if b := ptyKeyFromEbiten(nil, nil, ebiten.KeyZ, false, false, false, true); len(b) != 0 {
		t.Fatalf("Super+Z should be nil, got %v", b)
	}
}

func TestEncodeKittyCharCtrlPunct(t *testing.T) {
	// Grok queue pane / settings need CSI-u, not classic C0.
	b := encodeKittyChar(';', false, false, true, false)
	if string(b) != "\x1b[59;5u" { // ';'=59, ctrl mods=5
		t.Fatalf("Ctrl+; = %q want CSI 59;5u", b)
	}
	b = encodeKittyChar(',', false, false, true, false)
	if string(b) != "\x1b[44;5u" {
		t.Fatalf("Ctrl+, = %q", b)
	}
	// Cmd+Shift+Z redo: shift|super → mods 1+(1|8)=10
	b = encodeKittyChar('z', true, false, false, true)
	if string(b) != "\x1b[122;10u" {
		t.Fatalf("Cmd+Shift+Z = %q want CSI 122;10u", b)
	}
	if b := encodeKittyChar('a', false, false, false, false); b != nil {
		t.Fatalf("bare char should be nil, got %q", b)
	}
}
