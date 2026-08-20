package chromehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsUnhelpfulCwdAppBundle(t *testing.T) {
	exe := "/Applications/suzuri.app/Contents/MacOS"
	if !isUnhelpfulCwd("/Applications/suzuri.app/Contents/Resources", exe) {
		t.Fatal("Resources should be unhelpful")
	}
	if !isUnhelpfulCwd("/Applications/suzuri.app/Contents/MacOS", exe) {
		t.Fatal("MacOS dir should be unhelpful")
	}
	if isUnhelpfulCwd("/Users/stephen/projects/foo", exe) {
		t.Fatal("project dir should be kept")
	}
	if isUnhelpfulCwd("/tmp", "/usr/local/bin") {
		t.Fatal("unrelated cwd should be kept")
	}
	if !isUnhelpfulCwd("", exe) {
		t.Fatal("empty cwd is unhelpful")
	}
}

func TestIsUnhelpfulCwdSourceTreeKept(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "chrome"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "chrome", "Cargo.toml"), []byte("[package]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isUnhelpfulCwd(root, root) {
		t.Fatal("source checkout cwd should be kept")
	}
}

func TestIsUnhelpfulCwdInstallDir(t *testing.T) {
	dir := t.TempDir()
	// Install layout: exe sits next to chrome, no go.mod.
	if !isUnhelpfulCwd(dir, dir) {
		t.Fatal("install dir == exe dir should be unhelpful")
	}
}

func TestLaunchCwdNeverAppBundle(t *testing.T) {
	got := LaunchCwd()
	if got == "" {
		t.Fatal("LaunchCwd empty")
	}
	if strings.Contains(filepath.ToSlash(got), ".app/Contents") {
		t.Fatalf("LaunchCwd=%q still inside .app", got)
	}
	if isWindowsSystemCwd(got) {
		t.Fatalf("LaunchCwd=%q is a Windows system directory", got)
	}
}

func TestIsUnhelpfulCwdWindowsSystem(t *testing.T) {
	exe := `C:\Program Files\WindowsApps\suzuri`
	cases := []string{
		`C:\WINDOWS\system32`,
		`C:\Windows\System32\`,
		`C:/windows/syswow64`,
		`C:\Windows`,
		`C:\Program Files`,
		`C:\Program Files (x86)\suzuri`,
		`C:\ProgramData`,
		`C:\`,
		`C:`,
	}
	for _, cwd := range cases {
		if !isUnhelpfulCwd(cwd, exe) {
			t.Fatalf("%q should be unhelpful", cwd)
		}
	}
	if isUnhelpfulCwd(`C:\Users\stephen\projects\foo`, exe) {
		t.Fatal("user project should be kept")
	}
}
