//go:build darwin

package ui

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestPtyKeyFromEbitenCtrlZ(t *testing.T) {
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
	// Super+Z must not be a C0 control (clipboard / host chords).
	if b := ptyKeyFromEbiten(nil, nil, ebiten.KeyZ, false, false, false, true); len(b) != 0 {
		t.Fatalf("Super+Z should be nil, got %v", b)
	}
}
