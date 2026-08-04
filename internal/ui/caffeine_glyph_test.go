//go:build darwin

package ui

import "testing"

func TestCoffeeCupHasGlyphOrFallback(t *testing.T) {
	RegisterBundledFonts()
	const cup = '☕'
	if displayRune(cup) != cup {
		t.Fatalf("displayRune blanks coffee cup")
	}
	hasP := primaryHasRune(cup)
	hasC := cjkHasRune(cup)
	hasS := symbolsHasRune(cup)
	t.Logf("primary=%v cjk=%v symbols=%v", hasP, hasC, hasS)
	if !hasP && !hasC && !hasS {
		t.Fatal("no face covers ☕ — caffeine chip would paint blank")
	}
}
