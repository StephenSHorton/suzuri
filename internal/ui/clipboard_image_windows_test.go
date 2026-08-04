//go:build windows

package ui

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

// buildBI_RGB24 builds a minimal CF_DIB payload (bottom-up 24bpp BI_RGB).
func buildBI_RGB24(w, h int, fill color.RGBA) []byte {
	rowBytes := ((w*24 + 31) / 32) * 4
	pix := make([]byte, rowBytes*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*rowBytes + x*3
			pix[i+0] = fill.B
			pix[i+1] = fill.G
			pix[i+2] = fill.R
		}
	}
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:4], 40) // biSize
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(int32(w)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(int32(h)))
	binary.LittleEndian.PutUint16(hdr[12:14], 1)  // planes
	binary.LittleEndian.PutUint16(hdr[14:16], 24) // bitCount
	// compression 0 BI_RGB
	return append(hdr, pix...)
}

func TestDecodeDIB24(t *testing.T) {
	want := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	data := buildBI_RGB24(2, 2, want)
	img, err := decodeDIB(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("size %v", img.Bounds())
	}
	// Bottom-up: first pixel row in file is bottom of image → after decode y=0 is top.
	// All pixels same fill.
	got := img.At(0, 0)
	r, g, b, a := got.RGBA()
	if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B || uint8(a>>8) != 255 {
		t.Fatalf("pixel got rgba=%d,%d,%d,%d want %v", r>>8, g>>8, b>>8, a>>8, want)
	}
}

func TestDecodeDIB32TopDown(t *testing.T) {
	w, h := 1, 1
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:4], 40)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(int32(w)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(int32(-h))) // top-down
	binary.LittleEndian.PutUint16(hdr[12:14], 1)
	binary.LittleEndian.PutUint16(hdr[14:16], 32)
	// BGRA
	pix := []byte{200, 100, 50, 255}
	data := append(hdr, pix...)
	img, err := decodeDIB(data)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if uint8(r>>8) != 50 || uint8(g>>8) != 100 || uint8(b>>8) != 200 || uint8(a>>8) != 255 {
		t.Fatalf("got %d,%d,%d,%d", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestDecodeDIBRejectsBad(t *testing.T) {
	if _, err := decodeDIB([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for short dib")
	}
}

func TestLooksLikeImagePath(t *testing.T) {
	if !looksLikeImagePath(`C:\tmp\shot.PNG`) {
		t.Fatal("png")
	}
	if looksLikeImagePath(`C:\tmp\notes.txt`) {
		t.Fatal("txt should not match")
	}
}

// Ensure image.Image interface is used (decode path returns usable image).
func TestDecodeDIBAsImage(t *testing.T) {
	data := buildBI_RGB24(4, 3, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	img, err := decodeDIB(data)
	if err != nil {
		t.Fatal(err)
	}
	var _ image.Image = img
	if img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
		t.Fatalf("bounds %v", img.Bounds())
	}
}
