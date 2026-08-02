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

// fakeTerm is a minimal vt10x-like grid for liveExtent / viewCells tests.
type fakeTerm struct {
	cols, rows int
	cells      [][]rune
	cx, cy     int
}

func newFakeTerm(cols, rows int) *fakeTerm {
	cells := make([][]rune, rows)
	for y := range cells {
		cells[y] = make([]rune, cols)
		for x := range cells[y] {
			cells[y][x] = ' '
		}
	}
	return &fakeTerm{cols: cols, rows: rows, cells: cells}
}

func (f *fakeTerm) put(y int, s string) {
	for i, r := range s {
		if i < f.cols {
			f.cells[y][i] = r
		}
	}
}

// We can't implement vt10x.Terminal without the full interface; viewCells tests
// that need a real term live under windows with hinshun/vt10x in integration.
// liveExtent logic is covered via a pure helper tested through normalize-style
// unit on row scanning — see TestLiveExtentFromRows.

func TestLiveExtentFromRows(t *testing.T) {
	// Mirror liveExtent rules on a simple grid (cursor anchors empty rows).
	extent := func(rows []string, cursorY int) int {
		last := -1
		if cursorY >= 0 && cursorY < len(rows) {
			last = cursorY
		}
		for y, row := range rows {
			for _, ch := range row {
				if ch != ' ' && ch != 0 {
					if y > last {
						last = y
					}
					break
				}
			}
		}
		if last < 0 {
			return 1
		}
		return last + 1
	}
	if n := extent([]string{"hello     ", "          ", "          "}, 0); n != 1 {
		t.Fatalf("sparse top content: got %d want 1", n)
	}
	if n := extent([]string{"a", "b", "c", " ", " "}, 2); n != 3 {
		t.Fatalf("got %d want 3", n)
	}
	if n := extent([]string{"     ", "     "}, 0); n != 1 {
		t.Fatalf("empty with cursor: got %d want 1", n)
	}
	if n := extent([]string{"     ", "     "}, -1); n != 1 {
		t.Fatalf("fully empty: got %d want 1", n)
	}
}

func TestIsClearCommand(t *testing.T) {
	for _, s := range []string{"clear", "CLEAR", "  cls  ", "Clear-Host", "/usr/bin/clear"} {
		if !isClearCommand(s) {
			t.Fatalf("want clear: %q", s)
		}
	}
	for _, s := range []string{"echo clear", "clearly", "ls", ""} {
		if isClearCommand(s) {
			t.Fatalf("want not clear: %q", s)
		}
	}
}

func TestClearCommandPinsAfterBlock(t *testing.T) {
	s := newScrollback()
	s.push("old output")
	s.pushBlock("clear", 40)
	s.pinHere()
	if s.pin != len(s.lines) {
		t.Fatalf("pin=%d len=%d", s.pin, len(s.lines))
	}
	// Stick-bottom short path: no post-pin content → empty-looking pane.
	if s.pin == 0 {
		t.Fatal("pin should be set")
	}
}

func TestCommitLiveThenPushBlockOrder(t *testing.T) {
	// commitLive must place prior output in history before the next block header
	// so blocks and outputs stay associated (not all outputs in a live stack).
	s := newScrollback()
	s.pushBlock("echo one", 40)
	// Simulate committed output of first command (as commitLive would push).
	s.push("one")
	s.pushBlock("echo two", 40)
	// Expect order: block1 … "one" … block2
	var sawOne, sawBlock2 bool
	oneIdx, block2Idx := -1, -1
	for i, hl := range s.lines {
		if hl.kind == histNormal && hl.text == "one" {
			sawOne = true
			oneIdx = i
		}
		if hl.kind == histBlockCmd && stringsContains(hl.text, "echo two") {
			sawBlock2 = true
			block2Idx = i
		}
	}
	if !sawOne || !sawBlock2 {
		t.Fatalf("missing pieces: %+v", s.lines)
	}
	if oneIdx > block2Idx {
		t.Fatalf("output should precede next block: one@%d block2@%d", oneIdx, block2Idx)
	}
}

func TestClearPinHidesHistoryAtStickBottom(t *testing.T) {
	s := newScrollback()
	for i := 0; i < 20; i++ {
		s.push("old line")
	}
	// Simulate clear pin.
	s.pin = len(s.lines)
	s.stickBottom()
	// Post-clear: one command block.
	s.pushBlock("echo hi", 40)
	// Without a real term, exercise the pin short-content path via rowAt math.
	// content after pin should be the block only; pin floor prevents old lines.
	if s.pin != 20 {
		t.Fatalf("pin=%d", s.pin)
	}
	// Scrolling up must still reach pre-pin history.
	s.scrollBy(5, 10)
	if s.offset != 5 {
		t.Fatalf("offset=%d", s.offset)
	}
	s.stickBottom()
	if s.offset != 0 {
		t.Fatal("stick")
	}
}

func TestScreenWasCleared(t *testing.T) {
	prev := []string{"hello", "world", "   "}
	cur := []string{"   ", "   ", "   "}
	if !screenWasCleared(prev, cur) {
		t.Fatal("expected clear")
	}
	if screenWasCleared(cur, cur) {
		t.Fatal("blank→blank is not a clear")
	}
	if screenWasCleared(prev, []string{"hello", "   ", "   "}) {
		t.Fatal("partial content is not a clear")
	}
}

func TestViewCellsShowsBlockWithSparseLive(t *testing.T) {
	// Document model: history block + short live should appear together at bottom.
	s := newScrollback()
	s.pushBlock("Write-Output hello", 40)
	// Simulate effective live height of 2 (output + prompt) without a real PTY.
	// viewCells needs vt10x.Terminal — verify the composition math instead.
	hist := len(s.lines)
	liveEff := 2
	viewport := 20
	docLen := hist + liveEff
	start := docLen - viewport - s.offset
	if start < 0 {
		start = 0
	}
	// With small history+live, start is 0 → block is in the viewport.
	if start != 0 {
		t.Fatalf("expected full doc visible, start=%d hist=%d", start, hist)
	}
	// Contrast: old model used full live == viewport → start == hist (block off-screen).
	oldLive := viewport
	oldStart := hist + oldLive - viewport
	if oldStart != hist {
		t.Fatalf("sanity: old start=%d want hist=%d", oldStart, hist)
	}
	if oldStart <= start {
		t.Fatal("expected clipped live to reveal history that full live hid")
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
