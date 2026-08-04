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

func TestStripOSC8Hyperlinks(t *testing.T) {
	// ESC]8;;https://example.comST + text + ESC]8;;ST
	in := []byte("see \x1b]8;;https://example.com/path\x1b\\click me\x1b]8;;\x1b\\ please")
	got := string(StripOSC8Hyperlinks(in))
	want := "see click me please"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// BEL terminator form
	in2 := []byte("\x1b]8;;http://x\x07label\x1b]8;;\x07")
	if g := string(StripOSC8Hyperlinks(in2)); g != "label" {
		t.Fatalf("BEL form: %q", g)
	}
}


