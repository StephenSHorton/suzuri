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
