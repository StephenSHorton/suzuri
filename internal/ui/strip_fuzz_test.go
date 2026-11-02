//go:build windows || darwin

package ui

import (
	"bytes"
	"testing"
)

// fuzzIsSubsequence reports whether every byte of sub appears in s in order.
// The PTY strippers only remove bytes, so their output must be a
// subsequence of their input.
func fuzzIsSubsequence(sub, s []byte) bool {
	j := 0
	for i := 0; i < len(s) && j < len(sub); i++ {
		if s[i] == sub[j] {
			j++
		}
	}
	return j == len(sub)
}

var ptySeeds = []string{
	"",
	"plain\r\n",
	"hello\x1b]7878;cwd=C:\\Users\\test\x07world",
	"\x1b]7878;cwd=/Users/test\x1b\\",
	"\x1b]7;file://host/Users/test%20dir\x07",
	"\x1b]7;file:////server/share\x07",
	"\x1b]7;file://\x07",
	"\x1b]7878;cwd=   \x07",
	"\x1b]0;title\x07",
	"\x1b]7878;cwd=unterminated",
	"\x1b]7879;image=C:\\tmp\\a.png\x07",
	"\x1b]1337;File=name=YS5wbmc=;inline=1:iVBORw0KGgo=\x07",
	"\x1b]1337;File=inline=0:AAAA\x1b\\",
	"\x1b]1337;File=:\x07",
	"see /Users/me/.grok/sessions/0123abcd-0123-4567-89ab-0123456789ab/images/x.png [Open Image]",
	"C:\\Users\\me\\images\\shot.JPG",
	"\x1b_Ga=T,f=100,i=1;AAAA\x1b\\",
	"\x1b_Ga=T,f=100,i=2,m=1;AAAA\x1b\\\x1b_Gm=0;AAAA\x1b\\",
	"\x1b_Ga=p,i=1,c=4,r=2\x1b\\",
	"\x1b_Ga=d,d=A\x1b\\",
	"\x1b_Ga=T;unterminated",
	"\x1b_",
	"\x1b",
	"\x1b[31mred\x1b[0m",
}

func FuzzStripAndTakeCwd(f *testing.F) {
	for _, s := range ptySeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		orig := append([]byte(nil), in...)
		clean, cwd, ok := stripAndTakeCwd(in)
		if !bytes.Equal(in, orig) {
			t.Fatalf("input mutated")
		}
		if !fuzzIsSubsequence(clean, in) {
			t.Fatalf("clean %q is not a subsequence of %q", clean, in)
		}
		if ok && cwd == "" {
			t.Fatalf("ok with empty cwd")
		}
		if !ok && cwd != "" {
			t.Fatalf("cwd %q without ok", cwd)
		}
		if !bytes.Contains(in, []byte{0x1b, ']'}) && (!bytes.Equal(clean, in) || ok) {
			t.Fatalf("input without OSC changed: %q -> %q", in, clean)
		}
	})
}

func FuzzStripAndTakeImages(f *testing.F) {
	for _, s := range ptySeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		orig := append([]byte(nil), in...)
		clean, paths, blobs := stripAndTakeImages(in)
		if !bytes.Equal(in, orig) {
			t.Fatalf("input mutated")
		}
		if !fuzzIsSubsequence(clean, in) {
			t.Fatalf("clean %q is not a subsequence of %q", clean, in)
		}
		seen := map[string]bool{}
		for _, p := range paths {
			if p == "" {
				t.Fatalf("empty path in %q", paths)
			}
			if seen[p] {
				t.Fatalf("duplicate path %q", p)
			}
			seen[p] = true
		}
		for _, b := range blobs {
			if b.data == nil {
				t.Fatalf("blob %q with nil data", b.name)
			}
		}
		if !bytes.Contains(in, []byte{0x1b, ']'}) && !bytes.Equal(clean, in) {
			t.Fatalf("input without OSC changed: %q -> %q", in, clean)
		}
	})
}

// feedKittyAll runs data through feedKittyAPCs split into chunks of n bytes
// and returns every VT byte it passed on (writeVT plus the residuals).
func feedKittyAll(t *testing.T, data []byte, n int) []byte {
	t.Helper()
	k := newKittyGfx()
	var vt []byte
	write := func(b []byte) { vt = append(vt, b...) }
	cursor := func() (int, int) { return 3, 4 }
	for i := 0; i < len(data); i += n {
		end := i + n
		if end > len(data) {
			end = len(data)
		}
		res := feedKittyAPCs(k, data[i:end], write, cursor)
		vt = append(vt, res...)
		if len(k.pendingAPC) > maxKittyOpenBuf {
			t.Fatalf("pendingAPC grew to %d", len(k.pendingAPC))
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.images) > maxKittyImages {
		t.Fatalf("%d images held, cap %d", len(k.images), maxKittyImages)
	}
	if len(k.openBuf) > maxKittyOpenBuf {
		t.Fatalf("openBuf grew to %d", len(k.openBuf))
	}
	return vt
}

func FuzzFeedKittyAPCs(f *testing.F) {
	for _, s := range ptySeeds {
		f.Add([]byte(s), uint8(0))
		f.Add([]byte(s), uint8(3))
	}
	f.Fuzz(func(t *testing.T, in []byte, split uint8) {
		whole := feedKittyAll(t, in, len(in)+1)
		if bytes.Contains(whole, []byte("\x1b_G")) {
			t.Fatalf("Kitty APC leaked to VT: %q", whole)
		}
		if !fuzzIsSubsequence(whole, in) {
			t.Fatalf("VT %q is not a subsequence of %q", whole, in)
		}
		if split == 0 {
			return
		}
		// Chunk boundaries must not change what reaches the VT parser.
		chunked := feedKittyAll(t, in, int(split))
		if !bytes.Equal(whole, chunked) {
			t.Fatalf("split %d changed VT bytes:\nwhole   %q\nchunked %q", split, whole, chunked)
		}
	})
}

func FuzzTrimInBufPreferKitty(f *testing.F) {
	for _, s := range ptySeeds {
		f.Add([]byte(s), 4)
	}
	f.Add([]byte("0123456789\x1b_Ga=T;AAAA\x1b\\tail"), 12)
	f.Fuzz(func(t *testing.T, buf []byte, keep int) {
		got := trimInBufPreferKitty(buf, keep)
		if keep < 1 || len(buf) <= keep {
			if !bytes.Equal(got, buf) {
				t.Fatalf("untrimmed buffer changed")
			}
			return
		}
		if len(got) > keep {
			t.Fatalf("kept %d bytes, want <= %d", len(got), keep)
		}
		if !bytes.HasSuffix(buf, got) {
			t.Fatalf("result is not a suffix of the buffer")
		}
		if len(got) > 0 && len(got) < keep && !bytes.HasPrefix(got, []byte("\x1b_G")) {
			t.Fatalf("trimmed past keep without landing on ESC_G: %q", got)
		}
		if len(got) > 0 && &got[0] == &buf[len(buf)-len(got)] {
			t.Fatalf("result aliases the input buffer")
		}
	})
}
