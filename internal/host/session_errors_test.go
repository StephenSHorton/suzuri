package host

import "testing"

func TestStripNoColor(t *testing.T) {
	in := []string{
		"PATH=/bin",
		"NO_COLOR=1",
		"COLORTERM=truecolor",
		"no_color=yes",
	}
	got := stripNoColor(in)
	for _, e := range got {
		if len(e) >= 8 && (e[:8] == "NO_COLOR" || e[:8] == "no_color") {
			t.Fatalf("NO_COLOR leaked: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
