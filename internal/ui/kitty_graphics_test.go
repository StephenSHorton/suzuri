//go:build windows || darwin

package ui

import (
	"encoding/base64"
	"image"
	"image/png"
	"bytes"
	"testing"
)

func TestFeedKittyTransmitAndPlace(t *testing.T) {
	// 1x1 red PNG
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, image.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	k := newKittyGfx()
	var written []byte
	writeVT := func(b []byte) { written = append(written, b...) }
	curCol, curRow := 3, 5
	cursor := func() (int, int) { return curCol, curRow }

	// Move cursor then place (simulate Grok: CSI H then a=t then a=p)
	seq := []byte("\x1b[6;4H") // row 6 col 4 → 0-based 5,3 after write
	// Transmit
	tx := []byte("\x1b_Ga=t,f=100,t=d,q=2,i=7,m=0;" + b64 + "\x1b\\")
	// Place
	pl := []byte("\x1b_Ga=p,i=7,p=7,c=12,r=6,z=1,C=1,q=2\x1b\\")

	// First: write CSI alone
	rest := feedKittyAPCs(k, seq, writeVT, cursor)
	if string(rest) != "\x1b[6;4H" && len(rest) > 0 {
		// rest may still hold CSI if no APC — should be returned for term.Write
	}
	// Our feed returns residual without writing it — caller writes residual.
	// For CSI-only data, residual is the CSI.
	if len(rest) == 0 && len(written) == 0 {
		// ok if empty
	}

	// Transmit
	rest = feedKittyAPCs(k, tx, writeVT, cursor)
	if len(rest) != 0 {
		t.Fatalf("expected empty rest after pure APC, got %q", rest)
	}
	if k.image(7) == nil {
		t.Fatal("expected image id 7 after transmit")
	}

	// After CSI H would move cursor — set cursor as if VT parsed it
	curCol, curRow = 3, 5
	rest = feedKittyAPCs(k, pl, writeVT, cursor)
	places := k.snapshotPlacements()
	if len(places) != 1 {
		t.Fatalf("places=%d", len(places))
	}
	if places[0].id != 7 || places[0].cols != 12 || places[0].rows != 6 {
		t.Fatalf("place=%+v", places[0])
	}
	if places[0].col != 3 || places[0].row != 5 {
		t.Fatalf("cursor place col,row = %d,%d", places[0].col, places[0].row)
	}
}

func TestParseKittyKV(t *testing.T) {
	m := parseKittyKV("a=p,i=2,c=10,r=5,z=-1,C=1,q=2")
	if m["a"] != "p" || m["i"] != "2" || m["c"] != "10" {
		t.Fatalf("%v", m)
	}
}

func tinyPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, image.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// Incomplete APCs must not leak base64 into residual/written VT text.
func TestFeedKittyIncompleteAPCNotText(t *testing.T) {
	b64 := tinyPNGBase64(t)
	tx := []byte("\x1b_Ga=t,f=100,t=d,q=2,i=9,m=0;" + b64 + "\x1b\\")

	k := newKittyGfx()
	var written []byte
	writeVT := func(b []byte) { written = append(written, b...) }
	cursor := func() (int, int) { return 0, 0 }

	// Split mid-payload (after ESC_G header + some base64).
	split := 20
	if split >= len(tx) {
		t.Fatal("fixture too short")
	}
	rest1 := feedKittyAPCs(k, tx[:split], writeVT, cursor)
	if len(rest1) != 0 {
		t.Fatalf("incomplete APC returned as residual: %q", rest1)
	}
	if len(written) != 0 {
		t.Fatalf("incomplete APC written as VT: %q", written)
	}
	if len(k.pendingAPC) == 0 {
		t.Fatal("expected pendingAPC after incomplete chunk")
	}
	if bytes.Contains(k.pendingAPC, []byte{0x1b, '\\'}) {
		t.Fatal("pending should not include ST yet")
	}

	rest2 := feedKittyAPCs(k, tx[split:], writeVT, cursor)
	if len(rest2) != 0 {
		t.Fatalf("after complete: residual %q", rest2)
	}
	if len(written) != 0 {
		t.Fatalf("APC body must not write VT: %q", written)
	}
	if len(k.pendingAPC) != 0 {
		t.Fatalf("pending should clear, got %d bytes", len(k.pendingAPC))
	}
	if k.image(9) == nil {
		t.Fatal("expected image after reassembled APC")
	}
}

// Grok emits 4096-byte base64 chunks; PTY drains often split inside a chunk.
func TestFeedKittyGrokChunkedAcrossDrains(t *testing.T) {
	// Build a multi-chunk stream like kitty_chunked_escape (4096 b64 / chunk).
	// Inflate a small PNG's base64 by repeating a valid single-chunk transmit
	// path: first chunk m=1, second m=0 with remaining.
	b64 := tinyPNGBase64(t)
	// Force two logical chunks even for a tiny image by splitting our own
	// stream at an arbitrary interior point (mirrors mid-chunk PTY read).
	full := "\x1b_Ga=t,f=100,t=d,q=2,i=3,m=0;" + b64 + "\x1b\\"
	k := newKittyGfx()
	var written []byte
	writeVT := func(b []byte) {
		written = append(written, b...)
	}
	cursor := func() (int, int) { return 1, 2 }

	// Interleave ordinary VT text before and after; must survive, APC must not.
	parts := [][]byte{
		[]byte("hello"),
		[]byte(full[:12]),
		[]byte(full[12:40]),
		[]byte(full[40:]),
		[]byte("world"),
	}
	var residual []byte
	for _, p := range parts {
		r := feedKittyAPCs(k, p, writeVT, cursor)
		// Residual is only non-APC VT; accumulate as the host would Write it.
		if len(r) > 0 {
			written = append(written, r...)
			residual = append(residual, r...)
		}
	}
	if k.image(3) == nil {
		t.Fatal("expected reassembled image id 3")
	}
	if bytes.Contains(written, []byte(b64[:min(8, len(b64))])) {
		t.Fatalf("base64 leaked into VT stream: %q", written)
	}
	if !bytes.Contains(written, []byte("hello")) || !bytes.Contains(written, []byte("world")) {
		t.Fatalf("normal VT lost: %q residual=%q", written, residual)
	}
}

func TestFeedKittyTrailingESCPrefixHeld(t *testing.T) {
	k := newKittyGfx()
	var written []byte
	writeVT := func(b []byte) { written = append(written, b...) }
	cursor := func() (int, int) { return 0, 0 }

	rest := feedKittyAPCs(k, []byte("x\x1b"), writeVT, cursor)
	// 'x' residual; ESC held in pending (writeVT may have gotten nothing if
	// residual path only — feed returns residual for caller to write).
	if string(rest) != "x" {
		t.Fatalf("rest=%q", rest)
	}
	if len(k.pendingAPC) != 1 || k.pendingAPC[0] != 0x1b {
		t.Fatalf("pending ESC: %v", k.pendingAPC)
	}

	b64 := tinyPNGBase64(t)
	// Continue as _G… (pending ESC + this = full APC start)
	cont := []byte("_Ga=t,f=100,t=d,q=2,i=4,m=0;" + b64 + "\x1b\\")
	rest = feedKittyAPCs(k, cont, writeVT, cursor)
	if len(rest) != 0 {
		t.Fatalf("rest=%q", rest)
	}
	if k.image(4) == nil {
		t.Fatal("expected image after ESC-prefix reassembly")
	}
}

func TestTrimInBufPreferKitty(t *testing.T) {
	// Noise + full APC + trailing noise; keep a window that starts mid-noise
	// so a naive slice would begin before ESC_G — trim should advance to it.
	apc := []byte("\x1b_Ga=t,m=0;AAAA\x1b\\")
	buf := append(bytes.Repeat([]byte("N"), 100), apc...)
	buf = append(buf, bytes.Repeat([]byte("Z"), 20)...)
	// Window covers some leading N's + full APC + Z's.
	keep := 10 + len(apc) + 20
	got := trimInBufPreferKitty(buf, keep)
	if !bytes.HasPrefix(got, []byte{0x1b, '_', 'G'}) {
		t.Fatalf("expected APC-aligned trim, got %q", got[:min(20, len(got))])
	}
	if !bytes.Contains(got, apc) {
		t.Fatal("trimmed buffer lost APC")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
