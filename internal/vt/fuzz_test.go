package vt

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

// isSubsequence reports whether every byte of sub appears in s in order.
// The strippers only remove bytes, so their output must be a subsequence
// of their input.
func isSubsequence(sub, s []byte) bool {
	j := 0
	for i := 0; i < len(s) && j < len(sub); i++ {
		if s[i] == sub[j] {
			j++
		}
	}
	return j == len(sub)
}

var vtSeeds = []string{
	"",
	"plain text\r\n",
	"\x1b[31mred\x1b[0m",
	"\x1b]0;title\x07after",
	"\x1b]8;;https://example.com\x07link\x1b]8;;\x07",
	"\x1b]8;id=1;https://example.com\x1b\\link\x1b]8;;\x1b\\",
	"\x1b]8;;unterminated",
	"\x1b\x1b]8;;x\x07]8",
	"\x1bP1$r0m\x1b\\",
	"\x1b_Ga=T,f=100;AAAA\x1b\\",
	"\x1b(B\x1b=",
	"日本語\xe6\x97",
	"\xef\xbb\xbfbom",
	"\xff\xfe invalid",
	"\x1b",
	"\x1b[",
}

func FuzzStripOSC8Hyperlinks(f *testing.F) {
	for _, s := range vtSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		orig := append([]byte(nil), in...)
		out := StripOSC8Hyperlinks(in)
		if !bytes.Equal(in, orig) {
			t.Fatalf("input mutated")
		}
		if len(out) > len(in) {
			t.Fatalf("output longer than input: %d > %d", len(out), len(in))
		}
		if !isSubsequence(out, in) {
			t.Fatalf("output %q is not a subsequence of input %q", out, in)
		}
		if !bytes.Contains(in, []byte{0x1b, ']', '8'}) && !bytes.Equal(out, in) {
			t.Fatalf("input without OSC 8 changed: %q -> %q", in, out)
		}
	})
}

func FuzzStreamWrite(f *testing.F) {
	for _, s := range vtSeeds {
		f.Add([]byte(s), uint8(1))
	}
	f.Fuzz(func(t *testing.T, in []byte, split uint8) {
		var whole Stream
		got := whole.Write(in) + whole.Flush()
		checkStreamText(t, got)

		// Feed the same bytes in chunks of size split.
		n := int(split)
		if n == 0 {
			n = 1
		}
		var chunked Stream
		var sb []byte
		for i := 0; i < len(in); i += n {
			end := i + n
			if end > len(in) {
				end = len(in)
			}
			s := chunked.Write(in[i:end])
			checkStreamText(t, s)
			sb = append(sb, s...)
			if len(chunked.buf) > 256 {
				t.Fatalf("held buffer grew to %d", len(chunked.buf))
			}
		}
		sb = append(sb, chunked.Flush()...)
		checkStreamText(t, string(sb))

		// Below the 256-byte hangover cap no bytes are dropped, so chunking
		// must not change what Write emits.
		if len(in) < 256 {
			var a, b Stream
			wa := a.Write(in)
			var wb []byte
			for i := 0; i < len(in); i += n {
				end := i + n
				if end > len(in) {
					end = len(in)
				}
				wb = append(wb, b.Write(in[i:end])...)
			}
			if wa != string(wb) {
				t.Fatalf("chunked output differs (split %d):\nwhole   %q\nchunked %q", n, wa, wb)
			}
		}
	})
}

func checkStreamText(t *testing.T, s string) {
	t.Helper()
	if !utf8.ValidString(s) {
		t.Fatalf("invalid UTF-8 out: %q", s)
	}
	for _, r := range s {
		if r == 0x1b {
			t.Fatalf("ESC leaked: %q", s)
		}
		if r == utf8.RuneError {
			t.Fatalf("U+FFFD leaked: %q", s)
		}
	}
}
