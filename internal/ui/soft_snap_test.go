package ui

import "testing"

func TestSoftSnapDim(t *testing.T) {
	full := 1000
	// Exact half → snaps.
	if g := softSnapDim(500, full, 18); g != 500 {
		t.Fatalf("exact half: got %d", g)
	}
	// Within threshold of half.
	if g := softSnapDim(510, full, 18); g != 500 {
		t.Fatalf("near half: got %d want 500", g)
	}
	// Outside threshold — free.
	if g := softSnapDim(540, full, 18); g != 540 {
		t.Fatalf("far from half: got %d want 540", g)
	}
	// Near third.
	if g := softSnapDim(340, full, 18); g != 333 && g != full/3 {
		// 1000/3 = 333
		if g != 333 {
			t.Fatalf("near third: got %d want 333", g)
		}
	}
}

func TestSoftSnapSize(t *testing.T) {
	w, h := softSnapSize(512, 760, 1000, 800, 18)
	if w != 500 {
		t.Fatalf("w=%d want 500", w)
	}
	if h != 800 { // full height is a target
		// 760 is 40 away from 800 — outside 18, should stay 760; near half 400 no
		if h != 760 {
			t.Fatalf("h=%d want 760 (no snap)", h)
		}
	}
}

func TestSoftSnapRectRightEdge(t *testing.T) {
	l, t0, r, b := 100, 50, 612, 650 // w=512 within 18 of half(500)
	if !softSnapRect(&l, &t0, &r, &b, 2 /*right*/, 0, 0, 1000, 800, 18) {
		t.Fatal("expected snap")
	}
	if r-l != 500 {
		t.Fatalf("width=%d want 500 (l=%d r=%d)", r-l, l, r)
	}
	if l != 100 {
		t.Fatalf("left should stay 100, got %d", l)
	}
}
