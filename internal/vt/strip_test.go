package vt

import "testing"

func TestStripCSI(t *testing.T) {
	in := []byte("\x1b[32mhi\x1b[0m\r\n\x1b]0;title\x07ok")
	got := string(StripCSI(in))
	want := "hi\r\nok"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestStripPreservesBackspace(t *testing.T) {
	in := []byte("ab\b\x1b[Kc")
	got := string(StripCSI(in))
	// CSI K stripped; BS kept
	want := "ab\bc"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
