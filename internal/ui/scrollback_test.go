package ui

import "testing"

func TestScreenShiftedUp(t *testing.T) {
	prev := []string{"a", "b", "c"}
	cur := []string{"b", "c", "d"}
	if !screenShiftedUp(prev, cur) {
		t.Fatal("expected shift")
	}
	if screenShiftedUp(prev, prev) {
		t.Fatal("identical should not shift")
	}
	if screenShiftedUp(prev, []string{"x", "y", "z"}) {
		t.Fatal("unrelated should not shift")
	}
}

func TestScrollAmountMulti(t *testing.T) {
	prev := []string{"a", "b", "c", "d"}
	cur := []string{"c", "d", "e", "f"}
	if n := scrollAmount(prev, cur); n != 2 {
		t.Fatalf("got %d want 2", n)
	}
}

func TestScrollByClamp(t *testing.T) {
	s := newScrollback()
	s.lines = []string{"1", "2", "3"}
	s.scrollBy(100, 10)
	if s.offset != 3 {
		t.Fatalf("offset=%d", s.offset)
	}
	s.scrollBy(-100, 10)
	if s.offset != 0 {
		t.Fatalf("offset=%d", s.offset)
	}
}
