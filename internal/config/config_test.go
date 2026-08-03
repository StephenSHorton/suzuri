package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDefaults(t *testing.T) {
	c := Normalize(Config{})
	if c.FontFace == "" || c.FontSizePx < 10 || c.Theme == "" {
		t.Fatalf("normalize empty: %+v", c)
	}
}

func TestShellMatrixOpacityClamp(t *testing.T) {
	c := Normalize(Config{ShellMatrixOpacity: 150})
	if c.ShellMatrixOpacity != 100 {
		t.Fatalf("clamp high: %d", c.ShellMatrixOpacity)
	}
	c = Normalize(Config{ShellMatrixOpacity: -10})
	if c.ShellMatrixOpacity != 0 {
		t.Fatalf("clamp low: %d", c.ShellMatrixOpacity)
	}
	if Default().ShellMatrixOpacity != 100 {
		t.Fatalf("default opacity %d", Default().ShellMatrixOpacity)
	}
	if Default().ShellMatrixOpacity01() != 1 {
		t.Fatalf("default 01=%v", Default().ShellMatrixOpacity01())
	}
}

func TestShellMatrixOpacityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	want := Default()
	want.ShellMatrixOpacity = 45
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellMatrixOpacity != 45 {
		t.Fatalf("got opacity %d", got.ShellMatrixOpacity)
	}
	// Missing key → default 100
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"font_face":"x","font_size_px":14,"cursor":"block","theme":"inkstone","shell_ansi_map":"soft"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellMatrixOpacity != 100 {
		t.Fatalf("missing opacity should default 100, got %d", got.ShellMatrixOpacity)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Point LOCALAPPDATA at temp so Path() is isolated.
	t.Setenv("LOCALAPPDATA", dir)
	want := Config{
		FontFace:     "Consolas",
		FontSizePx:   18,
		Cursor:       CursorBar,
		Theme:        ThemeCharmtone,
		ShellANSIMap: ANSIMapFull,
		Window: WindowPlacement{
			X: 120, Y: 80, Width: 1000, Height: 700, Maximized: false,
		},
	}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	path := Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// Ensure we wrote under temp suzuri dir.
	if filepath.Dir(path) != filepath.Join(dir, "suzuri") {
		t.Fatalf("path=%s", path)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FontFace != want.FontFace || got.FontSizePx != want.FontSizePx ||
		got.Cursor != want.Cursor || got.Theme != want.Theme ||
		got.ShellANSIMap != want.ShellANSIMap ||
		got.Window != want.Window {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestWindowPlacementValid(t *testing.T) {
	if (WindowPlacement{}).Valid() {
		t.Fatal("zero placement should be invalid")
	}
	if !(WindowPlacement{X: 10, Y: 10, Width: 800, Height: 600}).Valid() {
		t.Fatal("normal placement should be valid")
	}
	if (WindowPlacement{Width: 100, Height: 100}).Valid() {
		t.Fatal("tiny placement should be invalid")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.FontFace != d.FontFace || c.Theme != d.Theme {
		t.Fatalf("got %+v", c)
	}
}
