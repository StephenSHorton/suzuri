package transfer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EngineName is the preferred sidecar binary name.
const EngineName = "suzuri-transfer"

// legacyEngineName is accepted during the hato → suzuri transition.
const legacyEngineName = "hato"

// ResolveBinary finds the transfer engine executable.
//
// Order:
//  1. SUZURI_TRANSFER_BIN env (explicit path)
//  2. Sibling of the running suzuri executable
//  3. macOS .app: Contents/MacOS/<name> next to self
//  4. PATH lookup for suzuri-transfer, then hato
func ResolveBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv("SUZURI_TRANSFER_BIN")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("SUZURI_TRANSFER_BIN not found: %s", p)
	}

	self, err := os.Executable()
	if err == nil {
		self, _ = filepath.EvalSymlinks(self)
		dir := filepath.Dir(self)
		for _, name := range engineNames() {
			cand := filepath.Join(dir, name)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, nil
			}
		}
		// Dev: binary in repo root, engine in ../hato/target/...
		// Also allow Contents/MacOS layout when self is already there.
	}

	for _, name := range engineNames() {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf(
		"transfer engine not found (looked for %s next to suzuri and on PATH; set SUZURI_TRANSFER_BIN)",
		strings.Join(engineNames(), " or "),
	)
}

func engineNames() []string {
	names := []string{EngineName, legacyEngineName}
	if runtime.GOOS == "windows" {
		out := make([]string, 0, len(names)*2)
		for _, n := range names {
			out = append(out, n+".exe", n)
		}
		return out
	}
	return names
}
