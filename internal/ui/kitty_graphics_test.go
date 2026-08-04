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
