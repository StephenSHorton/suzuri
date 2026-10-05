package ui

import "testing"

func TestInputBarFlushRectEatsLeftInset(t *testing.T) {
	g := paneGeom{x: 4, w: 796, barY: 560, barH: 36}
	l, top, r, b := inputBarFlushRect(g, 800, 600)
	if l != 0 || r != 800 {
		t.Fatalf("panel %d..%d want 0..800 (left glass gap)", l, r)
	}
	if top != 560 || b != 600 {
		t.Fatalf("vertical %d..%d want 560..600 (flush bottom)", top, b)
	}
	// Mid-split pane must not steal the neighbor's edge.
	mid := paneGeom{x: 200, w: 300, barY: 560, barH: 40}
	l, _, r, _ = inputBarFlushRect(mid, 800, 600)
	if l != 200 || r != 500 {
		t.Fatalf("inner pane %d..%d", l, r)
	}
}

func TestDeleteToLineStart(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("hello world"))
	b.cursor = len(b.runes)
	b.deleteToLineStart()
	if b.text() != "" || b.cursor != 0 {
		t.Fatalf("clear whole line: text=%q cursor=%d", b.text(), b.cursor)
	}

	b.insertRunes([]rune("aa\nbbcc"))
	// Caret after "bb" (index 5): "aa\nbb|cc"
	b.cursor = 5
	b.deleteToLineStart()
	if b.text() != "aa\ncc" || b.cursor != 3 {
		t.Fatalf("mid-line: text=%q cursor=%d", b.text(), b.cursor)
	}

	// At line start: no-op
	b.cursor = 3
	b.deleteToLineStart()
	if b.text() != "aa\ncc" {
		t.Fatalf("at start no-op: %q", b.text())
	}
}

func TestClearLine(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("aa\nbb"))
	b.clearLine()
	if b.text() != "" || b.cursor != 0 {
		t.Fatalf("clearLine: %q cursor=%d", b.text(), b.cursor)
	}
}
