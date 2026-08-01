package ui

import "testing"

func TestInputBarInsertMoveBackspace(t *testing.T) {
	var b inputBar
	b.insertRunes([]rune("echo hi"))
	if b.text() != "echo hi" || b.cursor != 7 {
		t.Fatalf("insert: %q cursor=%d", b.text(), b.cursor)
	}
	b.moveLeft()
	b.moveLeft()
	// "echo hi" cursor at end → two lefts → before 'h' → insert X → "echo Xhi"
	b.insertRune('X')
	if b.text() != "echo Xhi" {
		t.Fatalf("mid insert: %q", b.text())
	}
	b.backspace()
	if b.text() != "echo hi" {
		t.Fatalf("backspace: %q", b.text())
	}
	b.moveHome()
	b.deleteForward()
	if b.text() != "cho hi" {
		t.Fatalf("delete: %q", b.text())
	}
}

func TestInputBarSubmitHistory(t *testing.T) {
	var b inputBar
	b.insertRunes([]rune("one"))
	if got := b.submit(); got != "one" {
		t.Fatalf("submit: %q", got)
	}
	if b.text() != "" || b.cursor != 0 {
		t.Fatalf("cleared after submit: %q", b.text())
	}
	b.insertRunes([]rune("two"))
	_ = b.submit()
	b.historyUp()
	if b.text() != "two" {
		t.Fatalf("hist up latest: %q", b.text())
	}
	b.historyUp()
	if b.text() != "one" {
		t.Fatalf("hist up older: %q", b.text())
	}
	b.historyDown()
	if b.text() != "two" {
		t.Fatalf("hist down: %q", b.text())
	}
	b.historyDown()
	if b.text() != "" {
		t.Fatalf("hist to draft: %q", b.text())
	}
}

func TestInputBarFlattenNewlines(t *testing.T) {
	var b inputBar
	b.insertRunes([]rune("a\r\nb\tc"))
	if b.text() != "a b  c" {
		t.Fatalf("flatten: %q", b.text())
	}
}

func TestInputBarViewSlice(t *testing.T) {
	var b inputBar
	b.insertRunes([]rune("0123456789"))
	vis, caret := b.viewSlice(4)
	if vis != "6789" || caret != 4 {
		t.Fatalf("view end: %q caret=%d", vis, caret)
	}
	b.cursor = 2
	vis, caret = b.viewSlice(4)
	if vis != "0123" || caret != 2 {
		t.Fatalf("view start: %q caret=%d", vis, caret)
	}
}
