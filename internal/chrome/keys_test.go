package chrome

import (
	"runtime"
	"testing"
)

func TestKeyLabelsASCIIDefault(t *testing.T) {
	SetKeyGlyphSupport(false, false)
	wantShift := "Ctrl+Shift+T"
	if runtime.GOOS == "darwin" {
		wantShift = "Cmd+Shift+T"
	}
	if got := KeyCtrlShift("T"); got != wantShift {
		t.Fatalf("got %q want %q", got, wantShift)
	}
	if got := KeyShift("Enter"); got != "Shift+Enter" {
		t.Fatalf("got %q", got)
	}
	if got := KeyUpDown(); got != "Up / Down" {
		t.Fatalf("got %q", got)
	}
	wantPrimary := "Ctrl+K"
	if runtime.GOOS == "darwin" {
		wantPrimary = "Cmd+K"
	}
	if got := KeyCtrl("K"); got != wantPrimary {
		t.Fatalf("KeyCtrl got %q want %q", got, wantPrimary)
	}
}

func TestKeyLabelsFancy(t *testing.T) {
	SetKeyGlyphSupport(true, true)
	wantShift := "⌃⇧T"
	if runtime.GOOS == "darwin" {
		wantShift = "⌘⇧T"
	}
	if got := KeyCtrlShift("T"); got != wantShift {
		t.Fatalf("got %q want %q", got, wantShift)
	}
	if got := KeyShift("Enter"); got != "⇧Enter" {
		t.Fatalf("got %q", got)
	}
	if got := KeyUpDown(); got != "↑ / ↓" {
		t.Fatalf("got %q", got)
	}
	// reset default for other tests
	SetKeyGlyphSupport(false, false)
}
