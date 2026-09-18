package chrome

import (
	"strings"
	"testing"
	"unicode"
)

func TestTabBusyMarkASCIIHasNoTofu(t *testing.T) {
	prev := tabGlyphs()
	t.Cleanup(func() { SetTabStateGlyphs(prev) })
	SetTabStateGlyphs(TabGlyphsASCII)
	for _, alt := range []bool{false, true} {
		m := TabBusyMark(alt)
		if m == "" {
			t.Fatalf("alt=%v empty mark", alt)
		}
		if strings.ContainsRune(m, '\u25CC') { // dotted circle — missing on many Windows mono faces
			t.Fatalf("alt=%v used dotted circle %q", alt, m)
		}
		for _, r := range m {
			if r == ' ' {
				continue
			}
			if r > unicode.MaxASCII {
				t.Fatalf("ASCII pack must stay ASCII, got %q", m)
			}
		}
	}
}

func TestTabBusyMarkBrailleUsesSpinnerNotDottedCircle(t *testing.T) {
	prev := tabGlyphs()
	t.Cleanup(func() { SetTabStateGlyphs(prev) })
	SetTabStateGlyphs(TabGlyphsBraille)
	m := TabBusyMark(true)
	if strings.ContainsRune(m, '\u25CC') {
		t.Fatalf("braille pack used dotted circle %q", m)
	}
	if strings.TrimSpace(m) == "" {
		t.Fatal("empty braille mark")
	}
}
