package vt

import "testing"

func TestStreamSplitUTF8(t *testing.T) {
	// "界" is e7 95 8c
	var s Stream
	if g := s.Write([]byte{0xe7}); g != "" {
		t.Fatalf("partial1: got %q", g)
	}
	if g := s.Write([]byte{0x95}); g != "" {
		t.Fatalf("partial2: got %q", g)
	}
	g := s.Write([]byte{0x8c})
	if g != "界" {
		t.Fatalf("got %q want 界", g)
	}
}

func TestStreamSplitCSI(t *testing.T) {
	var s Stream
	if g := s.Write([]byte("hi\x1b[3")); g != "hi" {
		t.Fatalf("mid CSI: got %q", g)
	}
	g := s.Write([]byte("2mOK\x1b[0m"))
	if g != "OK" {
		t.Fatalf("after CSI: got %q want OK", g)
	}
}

func TestStreamNoTofuOnInvalid(t *testing.T) {
	var s Stream
	// lone 0xFF is invalid UTF-8
	g := s.Write([]byte{0xff, 'x'})
	if g != "x" {
		t.Fatalf("got %q want x", g)
	}
}
