package ui

import "testing"

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
