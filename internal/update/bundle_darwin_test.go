//go:build darwin

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPickAppZipAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "SHA256SUMS", URL: "sums"},
		{Name: "suzuri-1.0.0-darwin-" + runtime.GOARCH, URL: "bare"},
		{Name: "suzuri-1.0.0-darwin-" + runtime.GOARCH + ".zip", URL: "portable"},
		{Name: "suzuri-1.0.0-darwin-" + runtime.GOARCH + ".dmg", URL: "dmg"},
		{Name: "suzuri-1.0.0-darwin-" + runtime.GOARCH + ".app.zip", URL: "app"},
	}
	if got := pickAppZipAsset(assets); got.URL != "app" {
		t.Fatalf("picked %q, want the .app.zip", got.Name)
	}
	if got := pickAppZipAsset(assets[:3]); got.URL != "" {
		t.Fatalf("picked %q from a release with no .app.zip", got.Name)
	}
}

// An ad-hoc bundle must never replace the installed app.
func TestInstallAppBundleRefusesAdHoc(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "build", "suzuri.app")
	macos := filepath.Join(fake, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile("/usr/bin/true", filepath.Join(macos, "suzuri")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", fake).CombinedOutput(); err != nil {
		t.Skipf("codesign unavailable: %v %s", err, out)
	}
	zip := filepath.Join(dir, "suzuri.app.zip")
	if out, err := exec.Command("ditto", "-c", "-k", "--keepParent", fake, zip).CombinedOutput(); err != nil {
		t.Fatalf("ditto: %v %s", err, out)
	}
	installed := filepath.Join(dir, "Applications", "suzuri.app")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(installed, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := installAppBundle(zip, installed)
	if err == nil || !strings.Contains(err.Error(), "team") {
		t.Fatalf("err = %v, want a team mismatch", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("installed app was touched")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "Applications", ".suzuri-update-*"))
	if len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}
}

func TestRollbackAppBundleRestoresPrevious(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "suzuri.app")
	for p, v := range map[string]string{app: "new", app + ".old": "prev"} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "v"), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := rollbackAppBundle(app); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(app, "v"))
	if string(b) != "prev" {
		t.Fatalf("app holds %q, want the previous build", b)
	}
	for _, p := range []string{app + ".old", app + ".failed"} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s left behind", p)
		}
	}
}
