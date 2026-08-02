package ui
import "testing"
func TestShortTitleStripsGrokSpinner(t *testing.T) {
	cases := []struct{ in, want string }{
		{"⠿ Grok Build", "Grok Build"},
		{"⣿grok", "grok"},
		{"⠋  thinking", "thinking"},
		{"  ⣷  suzuri", "suzuri"},
		{"xargs", "xargs"},
		{"* Grok", "Grok"},
		{"C:\\Users\\foo\\bar", "bar"},
		{"● circle title", "circle title"},
	}
	for _, tc := range cases {
		if got := shortTitle(tc.in); got != tc.want {
			t.Fatalf("shortTitle(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestTitleReportsBusy(t *testing.T) {
	if !titleReportsBusy("⠋ Grok") {
		t.Fatal("spinner frame should report busy")
	}
	if !titleReportsBusy("⣾ thinking…") {
		t.Fatal("dots2 frame should report busy")
	}
	if titleReportsBusy("⠿ Grok") {
		t.Fatal("static ⠿ is idle badge, not busy")
	}
	if titleReportsBusy("Grok Build") {
		t.Fatal("plain title is not busy")
	}
	if titleReportsBusy("C:\\Users\\foo") {
		t.Fatal("path is not busy")
	}
}

