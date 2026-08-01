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
		got.ShellANSIMap != want.ShellANSIMap {
		t.Fatalf("got %+v want %+v", got, want)
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
