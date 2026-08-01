package update

import "testing"

func TestCmpSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.1.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-beta", "1.0.0", -1},
	}
	for _, tc := range cases {
		if got := cmpSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("cmpSemver(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if isNewer("v1.0.0", "dev") {
		t.Fatal("dev should not update")
	}
	if !isNewer("v1.1.0", "1.0.0") {
		t.Fatal("expected newer")
	}
	if isNewer("v1.0.0", "1.0.0") {
		t.Fatal("same version")
	}
}
