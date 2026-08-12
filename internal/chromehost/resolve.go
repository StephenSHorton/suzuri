package chromehost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BinaryName is the native chrome executable name (without platform suffix).
const BinaryName = "suzuri-chrome"

// EnvBinary is the env var for an explicit chrome binary path.
const EnvBinary = "SUZURI_CHROME"

// ResolveBinary finds the native suzuri-chrome executable.
//
// Order:
//  1. SUZURI_CHROME env (explicit path)
//  2. Sibling of the running suzuri executable
//  3. Dev heuristic: chrome/target/release/suzuri-chrome under repo roots
//     near the executable or current working directory
//  4. PATH lookup
func ResolveBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvBinary)); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("%s not found: %s", EnvBinary, p)
	}

	names := binaryNames()

	if self, err := os.Executable(); err == nil {
		self, _ = filepath.EvalSymlinks(self)
		dir := filepath.Dir(self)
		if p := firstExisting(dir, names); p != "" {
			return p, nil
		}
		// Dev: exe in repo root, chrome built under chrome/target/release.
		if p := firstExisting(filepath.Join(dir, "chrome", "target", "release"), names); p != "" {
			return p, nil
		}
	}

	if p := findDevBinary(names); p != "" {
		return p, nil
	}

	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf(
		"suzuri-chrome not found (looked next to suzuri, chrome/target/release, and PATH; set %s)",
		EnvBinary,
	)
}

// findDevBinary walks up from cwd (and a few fixed relatives) for a cargo
// release build of suzuri-chrome.
func findDevBinary(names []string) string {
	var starts []string
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	if self, err := os.Executable(); err == nil {
		self, _ = filepath.EvalSymlinks(self)
		starts = append(starts, filepath.Dir(self))
	}
	seen := make(map[string]struct{})
	for _, start := range starts {
		dir := start
		for i := 0; i < 8; i++ {
			if _, ok := seen[dir]; ok {
				break
			}
			seen[dir] = struct{}{}
			// chrome/target/release relative to this dir (repo root)
			if p := firstExisting(filepath.Join(dir, "chrome", "target", "release"), names); p != "" {
				return p
			}
			// already inside chrome/
			if p := firstExisting(filepath.Join(dir, "target", "release"), names); p != "" {
				// only accept if parent looks like the chrome crate
				base := filepath.Base(dir)
				if base == "chrome" || fileExists(filepath.Join(dir, "Cargo.toml")) {
					return p
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

func firstExisting(dir string, names []string) string {
	for _, name := range names {
		cand := filepath.Join(dir, name)
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func binaryNames() []string {
	if runtime.GOOS == "windows" {
		return []string{BinaryName + ".exe", BinaryName}
	}
	return []string{BinaryName}
}
