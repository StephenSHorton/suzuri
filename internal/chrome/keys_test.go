package chrome

import "testing"

func TestKeyLabelsASCIIDefault(t *testing.T) {
	SetKeyGlyphSupport(false, false)
	if got := KeyCtrlShift("T"); got != "Ctrl+Shift+T" {
		t.Fatalf("got %q", got)
	}
	if got := KeyShift("Enter"); got != "Shift+Enter" {
		t.Fatalf("got %q", got)
	}
	if got := KeyUpDown(); got != "Up / Down" {
		t.Fatalf("got %q", got)
	}
}

func TestKeyLabelsFancy(t *testing.T) {
	SetKeyGlyphSupport(true, true)
	if got := KeyCtrlShift("T"); got != "⌃⇧T" {
		t.Fatalf("got %q", got)
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
