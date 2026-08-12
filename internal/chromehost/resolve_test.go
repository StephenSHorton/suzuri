package chromehost

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveBinaryEnv(t *testing.T) {
	dir := t.TempDir()
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvBinary, path)

	got, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != path {
		t.Fatalf("got %q want %q", got, path)
	}
}

func TestResolveBinaryEnvMissing(t *testing.T) {
	t.Setenv(EnvBinary, filepath.Join(t.TempDir(), "no-such-chrome"))
	if _, err := ResolveBinary(); err == nil {
		t.Fatal("expected error for missing SUZURI_CHROME")
	}
}

func TestFindDevBinary(t *testing.T) {
	root := t.TempDir()
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	rel := filepath.Join("chrome", "target", "release")
	if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, rel, name)
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Clear env override; walk from a nested cwd under the fake repo.
	t.Setenv(EnvBinary, "")
	nested := filepath.Join(root, "cmd", "suzuri")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	got := findDevBinary(binaryNames())
	// macOS temp dirs may be under /var → /private/var; compare canonical paths.
	want, _ := filepath.EvalSymlinks(path)
	gotCanon, _ := filepath.EvalSymlinks(got)
	if gotCanon == "" {
		gotCanon = got
	}
	if want == "" {
		want = path
	}
	if gotCanon != want {
		t.Fatalf("findDevBinary: got %q want %q", got, path)
	}
}

func TestWithConfigDir(t *testing.T) {
	env := []string{"FOO=1", EnvConfigDir + "=/old", "BAR=2"}
	out := withConfigDir(env, "/new/config")
	var saw string
	for _, e := range out {
		if len(e) > len(EnvConfigDir)+1 && e[:len(EnvConfigDir)+1] == EnvConfigDir+"=" {
			if saw != "" {
				t.Fatalf("duplicate %s in %v", EnvConfigDir, out)
			}
			saw = e
		}
	}
	if saw != EnvConfigDir+"=/new/config" {
		t.Fatalf("got %q", saw)
	}
}

func TestWithHostEnvSetsVersion(t *testing.T) {
	env := []string{"FOO=1", EnvVersion + "=old", "BAR=2"}
	out := withHostEnv(env, "/cfg", "0.9.113")
	var sawVer, sawDir string
	for _, e := range out {
		if strings.HasPrefix(e, EnvVersion+"=") {
			if sawVer != "" {
				t.Fatalf("duplicate version in %v", out)
			}
			sawVer = e
		}
		if strings.HasPrefix(e, EnvConfigDir+"=") {
			sawDir = e
		}
	}
	if sawVer != EnvVersion+"=0.9.113" {
		t.Fatalf("version %q", sawVer)
	}
	if sawDir != EnvConfigDir+"=/cfg" {
		t.Fatalf("dir %q", sawDir)
	}
}
