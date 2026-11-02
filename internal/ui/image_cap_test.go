//go:build windows || darwin

package ui

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// pngHeaderOnly returns a PNG signature plus an IHDR chunk declaring w x h
// 8-bit RGBA, with no image data: enough for DecodeConfig.
func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // bit depth 8, colour type RGBA
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ihdr)))
	b.Write(l[:])
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	binary.BigEndian.PutUint32(l[:], crc32.ChecksumIEEE(chunk))
	b.Write(l[:])
	return b.Bytes()
}

func TestLoadImageBytesRejectsHugeDimensions(t *testing.T) {
	_, err := loadImageBytes("bomb.png", pngHeaderOnly(100000, 100000))
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("err = %v, want errImageTooLarge", err)
	}
}

func TestLoadImageBytesSmallPNG(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	ti, err := loadImageBytes("ok.png", b.Bytes())
	if err != nil || ti == nil || ti.pxW != 4 || ti.pxH != 3 {
		t.Fatalf("ti=%+v err=%v", ti, err)
	}
}

func TestKittyAPCRejectsHugeDimensions(t *testing.T) {
	k := newKittyGfx()
	apc := "\x1b_Ga=T,f=100,i=7;" + base64.StdEncoding.EncodeToString(pngHeaderOnly(60000, 60000)) + "\x1b\\"
	if res := feedKittyAPCs(k, []byte(apc), nil, nil); len(res) != 0 {
		t.Fatalf("residual VT %q", res)
	}
	if k.image(7) != nil {
		t.Fatalf("oversized image was stored")
	}
}
