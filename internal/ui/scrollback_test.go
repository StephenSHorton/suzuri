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
	s.lines = []histLine{{text: "1"}, {text: "2"}, {text: "3"}}
	s.scrollBy(100, 10)
	if s.offset != 3 {
		t.Fatalf("offset=%d", s.offset)
	}
	s.scrollBy(-100, 10)
	if s.offset != 0 {
		t.Fatalf("offset=%d", s.offset)
	}
}

func TestPushBlock(t *testing.T) {
	s := newScrollback()
	s.pushBlock("echo hi", 40)
	if len(s.lines) < 3 {
		t.Fatalf("expected block lines, got %d", len(s.lines))
	}
	// last non-empty should be cmd
	var foundRule, foundCmd bool
	for _, hl := range s.lines {
		if hl.kind == histBlockRule {
			foundRule = true
		}
		if hl.kind == histBlockCmd && stringsContains(hl.text, "echo hi") {
			foundCmd = true
		}
	}
	if !foundRule || !foundCmd {
		t.Fatalf("block missing rule/cmd: %+v", s.lines)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
