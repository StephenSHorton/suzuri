package workspacesync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EngineName is the sidecar binary name.
const EngineName = "suzuri-workspace-sync"

// EnvBinary is the env var for an explicit engine path.
const EnvBinary = "SUZURI_WORKSPACE_SYNC_BIN"

// ResolveBinary finds the workspace-sync engine executable.
//
// Order:
//  1. SUZURI_WORKSPACE_SYNC_BIN env (explicit path)
//  2. Sibling of the running suzuri executable
//  3. Dev: libs/transfer/target/{debug,release}/ under repo roots near cwd/exe
//  4. PATH lookup
func ResolveBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvBinary)); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("%s not found: %s", EnvBinary, p)
	}

	names := engineNames()

	if self, err := os.Executable(); err == nil {
		self, _ = filepath.EvalSymlinks(self)
		dir := filepath.Dir(self)
		if p := firstExisting(dir, names); p != "" {
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
		"workspace-sync engine not found (looked for %s next to suzuri, libs/transfer/target, and PATH; set %s)",
		EngineName, EnvBinary,
	)
}

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
			for _, sub := range []string{
				filepath.Join("libs", "transfer", "target", "debug"),
				filepath.Join("libs", "transfer", "target", "release"),
			} {
				if p := firstExisting(filepath.Join(dir, sub), names); p != "" {
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

func engineNames() []string {
	if runtime.GOOS == "windows" {
		return []string{EngineName + ".exe", EngineName}
	}
	return []string{EngineName}
}
