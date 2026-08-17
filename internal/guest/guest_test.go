package guest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseCatalog(t *testing.T) {
	c, err := parseCatalog([]byte(`{"version":1,"guests":[{"id":"ladybird","name":"Ladybird"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	g, ok := c.find("Ladybird")
	if !ok || g.ID != "ladybird" {
		t.Fatalf("find: %+v %v", g, ok)
	}
	if _, err := parseCatalog([]byte(`{"version":9}`)); err == nil {
		t.Fatal("want version error")
	}
}

func TestInstallFromAppAndRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ladybird is macOS-only")
	}
	root := t.TempDir()
	t.Setenv("SUZURI_CONFIG_DIR", root)
	t.Setenv("LADYBIRD", "")
	t.Setenv("LADYBIRD_SOURCE_DIR", "")

	src := filepath.Join(root, "src", "Ladybird.app", "Contents", "MacOS", "Ladybird")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("dummy"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(root, "src", "Ladybird.app")

	m, err := install("ladybird", InstallOptions{From: app})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "ladybird" || m.Protocol != 1 {
		t.Fatalf("manifest: %+v", m)
	}
	if len(m.Commands) != 1 || m.Commands[0].Title != "Open Browser Pane" {
		t.Fatalf("commands: %+v", m.Commands)
	}
	if _, err := os.Stat(m.Command); err != nil {
		t.Fatal(err)
	}
	got, ok := installed("ladybird")
	if !ok || got.Command != m.Command {
		t.Fatalf("installed: %+v %v", got, ok)
	}

	if err := remove("ladybird"); err != nil {
		t.Fatal(err)
	}
	if _, ok := installed("ladybird"); ok {
		t.Fatal("still installed")
	}
	if _, err := os.Stat(InstallDir("ladybird")); !os.IsNotExist(err) {
		t.Fatalf("install dir left behind: %v", err)
	}
}

func TestRemoveMissing(t *testing.T) {
	t.Setenv("SUZURI_CONFIG_DIR", t.TempDir())
	if err := remove("ladybird"); err == nil {
		t.Fatal("want error")
	}
}

func TestUnknownGuest(t *testing.T) {
	t.Setenv("SUZURI_CONFIG_DIR", t.TempDir())
	if _, err := install("nope", InstallOptions{}); err == nil {
		t.Fatal("want unknown guest")
	}
}
