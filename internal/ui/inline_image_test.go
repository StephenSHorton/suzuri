//go:build windows || darwin

package ui

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestFindImagePathsInText(t *testing.T) {
	s := `see C:\Users\4step\projects\suzuri\testdata-inline.png and images\1.jpg`
	got := findImagePathsInText(s)
	if len(got) < 1 {
		t.Fatalf("expected paths, got %#v", got)
	}
}

func TestFindImagePathsUnix(t *testing.T) {
	s := `see /Users/stephen/.grok/sessions/abc/images/1.jpg and /tmp/shot.png`
	got := findImagePathsInText(s)
	if len(got) < 2 {
		t.Fatalf("expected unix paths, got %#v", got)
	}
	found := false
	for _, p := range got {
		if p == "/tmp/shot.png" || filepath.Base(p) == "shot.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing /tmp/shot.png in %#v", got)
	}
}

func TestFixGrokSessionDisplayPath(t *testing.T) {
	// Grok paints a double-drive path; real folder uses URL-encoded workspace.
	mangled := `C:\Users\4step\.grok\sessions\C:\Users\4step\projects\suzuri\019fc39e-feb8-7a41-a2fc-23298878c66e\images\1.jpg`
	fixed := fixGrokSessionDisplayPath(mangled)
	want := `C:\Users\4step\.grok\sessions\C%3A%5CUsers%5C4step%5Cprojects%5Csuzuri\019fc39e-feb8-7a41-a2fc-23298878c66e\images\1.jpg`
	if fixed != want {
		t.Fatalf("got %q want %q", fixed, want)
	}
	// resolve should find the real file if present
	if abs := resolveImagePath("", mangled); abs == "" {
		// May be missing on CI; only require fix string when file exists
		if _, err := os.Stat(want); err == nil {
			t.Fatalf("resolve failed for existing %q", want)
		}
	}
}

func TestParseImageOSCPath(t *testing.T) {
	payload := []byte(`7879;image=C:\tmp\a.png`)
	p, blob, ok := parseImageOSC(payload)
	if !ok || p == "" || blob.data != nil {
		t.Fatalf("path osc: p=%q ok=%v", p, ok)
	}
}

func TestParseImageOSC1337(t *testing.T) {
	root, _ := os.Getwd()
	path := filepath.Join(root, "testdata-inline.png")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(root, "..", "..", "testdata-inline.png")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no test png")
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	payload := []byte("1337;File=inline=1;width=auto:" + b64)
	p, blob, ok := parseImageOSC(payload)
	if !ok || p != "" || len(blob.data) == 0 {
		t.Fatalf("1337: ok=%v len=%d", ok, len(blob.data))
	}
	im, err := loadImageBytes("t.png", blob.data)
	if err != nil || im == nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestLoadImageFile(t *testing.T) {
	// Prefer repo test asset if present
	root, _ := os.Getwd()
	path := filepath.Join(root, "testdata-inline.png")
	if _, err := os.Stat(path); err != nil {
		// ui package cwd may be module root or package dir
		path = filepath.Join(root, "..", "..", "testdata-inline.png")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("no testdata-inline.png")
	}
	im, err := loadImageFile(path)
	if err != nil || im == nil || im.pxW < 1 {
		t.Fatalf("load: %v im=%v", err, im)
	}
}
