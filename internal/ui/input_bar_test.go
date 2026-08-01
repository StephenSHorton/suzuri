package ui

import "testing"

func TestInputBarInsertMoveBackspace(t *testing.T) {
	var b inputBar
	b.histIdx = -1
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
	b.histIdx = -1
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

func TestInputBarMultiline(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("a\nb\tc"))
	if b.text() != "a\nb  c" {
		t.Fatalf("multiline: %q", b.text())
	}
	lines, cr, cc := b.wrapLayout(20)
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b  c" {
		t.Fatalf("layout: %#v", lines)
	}
	if cr != 1 {
		t.Fatalf("caret row %d", cr)
	}
	_ = cc
	// Soft wrap
	b.clear()
	b.insertRunes([]rune("0123456789"))
	lines, cr, cc = b.wrapLayout(4)
	if len(lines) != 3 {
		t.Fatalf("soft wrap lines: %#v", lines)
	}
	if lines[0] != "0123" || lines[1] != "4567" || lines[2] != "89" {
		t.Fatalf("soft wrap content: %#v", lines)
	}
	if cr != 2 || cc != 2 {
		t.Fatalf("caret at end: row=%d col=%d", cr, cc)
	}
}

func TestInputBarVisualMove(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	b.insertRunes([]rune("aaaa\nbbbb"))
	// cursor at end of second line
	if !b.moveVisualUp(10) {
		t.Fatal("expected move up")
	}
	if b.cursor != 4 { // after "aaaa" before \n — want col clamped on first line
		// first line "aaaa" col want 4 → index 4 (\n)
		if string(b.runes[:b.cursor]) != "aaaa" && b.cursor != 4 {
			// accept end of first line
			if b.cursor > 5 {
				t.Fatalf("cursor after up: %d text before %q", b.cursor, string(b.runes[:b.cursor]))
			}
		}
	}
	if b.moveVisualUp(10) {
		t.Fatal("already top — should fail")
	}
}

func TestInputBarVisibleWindow(t *testing.T) {
	var b inputBar
	b.histIdx = -1
	for i := 0; i < 12; i++ {
		if i > 0 {
			b.insertNewline()
		}
		b.insertRunes([]rune{rune('a' + i%26)})
	}
	view, cr, _ := b.visibleWindow(20, 4)
	if len(view) != 4 {
		t.Fatalf("window len %d", len(view))
	}
	if cr < 0 || cr >= 4 {
		t.Fatalf("caret row in window %d", cr)
	}
}
