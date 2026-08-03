package ui

import "testing"

func TestCleanURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://example.com/foo", "https://example.com/foo"},
		{"https://example.com/foo.", "https://example.com/foo"},
		{"www.example.com", "https://www.example.com"},
		{"https://x.com)", "https://x.com"},
		{"not a url", ""},
		{"http://", ""},
	}
	for _, c := range cases {
		got := cleanURL(c.in)
		if got != c.want {
			t.Errorf("cleanURL(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestFindLinksInGrid(t *testing.T) {
	// "see https://example.com/x now"
	line := "see https://example.com/x now"
	row := make([]cellPix, len([]rune(line)))
	for i, r := range []rune(line) {
		row[i] = cellPix{Ch: r, FR: 200, FG: 200, FB: 200}
	}
	grid := [][]cellPix{row}
	spans := findLinksInGrid(grid)
	if len(spans) != 1 {
		t.Fatalf("spans=%d want 1: %+v", len(spans), spans)
	}
	s := spans[0]
	if s.url != "https://example.com/x" {
		t.Fatalf("url=%q", s.url)
	}
	if s.x0 != 4 { // after "see "
		t.Fatalf("x0=%d want 4", s.x0)
	}
	// "https://example.com/x" = 21 runes → x1 = 4+21 = 25
	if s.x1 != 25 {
		t.Fatalf("x1=%d want 25", s.x1)
	}
	if _, ok := linkAt(spans, 10, 0); !ok {
		t.Fatal("expected hit at col 10")
	}
	if _, ok := linkAt(spans, 0, 0); ok {
		t.Fatal("no hit at col 0")
	}
}

func TestFindLinksWWW(t *testing.T) {
	line := "www.github.com/foo"
	row := make([]cellPix, len([]rune(line)))
	for i, r := range []rune(line) {
		row[i] = cellPix{Ch: r}
	}
	spans := findLinksInGrid([][]cellPix{row})
	if len(spans) != 1 || spans[0].url != "https://www.github.com/foo" {
		t.Fatalf("got %+v", spans)
	}
}

func TestApplyLinkHoverTint(t *testing.T) {
	row := make([]cellPix, 10)
	for i := range row {
		row[i] = cellPix{Ch: 'a', FR: 100, FG: 100, FB: 100}
	}
	grid := [][]cellPix{row}
	applyLinkHoverTint(grid, linkSpan{row: 0, x0: 2, x1: 5, url: "https://x"})
	if grid[0][2].FR == 100 && grid[0][2].FG == 100 {
		t.Fatal("expected primary tint on hover span")
	}
	if grid[0][0].FR != 100 {
		t.Fatal("col 0 should be untouched")
	}
}
