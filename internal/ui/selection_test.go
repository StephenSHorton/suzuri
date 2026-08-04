package ui

import (
	"testing"
	"time"
)

func TestWordBounds(t *testing.T) {
	cases := []struct {
		line       string
		col        int
		wantS, wantE int
	}{
		{"hello world", 1, 0, 4},
		{"hello world", 6, 6, 10},
		{"  foo  ", 0, 0, 1},
		{"  foo  ", 3, 2, 4},
		{"a::b", 1, 1, 2},
		{"", 0, 0, 0},
		{"x", 0, 0, 0},
		{"path/to", 4, 4, 4}, // '/'
		{"path/to", 0, 0, 3}, // "path"
		{"path/to", 5, 5, 6}, // "to"
	}
	for _, tc := range cases {
		s, e := wordBounds(tc.line, tc.col)
		if s != tc.wantS || e != tc.wantE {
			t.Errorf("wordBounds(%q, %d)=%d,%d want %d,%d", tc.line, tc.col, s, e, tc.wantS, tc.wantE)
		}
	}
}

func TestMultiClickBump(t *testing.T) {
	var m multiClick
	t0 := time.Now()
	if n := m.bump(3, 5, t0); n != 1 {
		t.Fatalf("first=%d", n)
	}
	if n := m.bump(3, 5, t0.Add(100*time.Millisecond)); n != 2 {
		t.Fatalf("double=%d", n)
	}
	if n := m.bump(3, 5, t0.Add(200*time.Millisecond)); n != 3 {
		t.Fatalf("triple=%d", n)
	}
	if n := m.bump(3, 5, t0.Add(300*time.Millisecond)); n != 1 {
		t.Fatalf("wrap=%d", n)
	}
	// Far away resets.
	if n := m.bump(20, 5, t0.Add(350*time.Millisecond)); n != 1 {
		t.Fatalf("move=%d", n)
	}
	// Timeout resets.
	if n := m.bump(20, 5, t0.Add(350*time.Millisecond+multiClickWindow+time.Millisecond)); n != 1 {
		t.Fatalf("timeout=%d", n)
	}
}
