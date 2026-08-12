package chromehost

import (
	"os"
	"path/filepath"
	"strings"
)

// LaunchCwd is the working directory new shells (and the UI process) should
// inherit. Dock / Launch Services start a .app in Contents/Resources; Windows
// installers often start in the folder that contains suzuri.exe. Those are
// not useful project directories — prefer $HOME. A real project cwd (or a
// source checkout that contains go.mod + chrome/Cargo.toml) is kept.
func LaunchCwd() string {
	if wd, err := os.Getwd(); err == nil && !IsUnhelpfulCwd(wd) {
		return wd
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			return home
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// IsUnhelpfulCwd reports whether cwd is an app-bundle or install-tree path
// that should not become the user's first shell directory.
func IsUnhelpfulCwd(cwd string) bool {
	exeDir := ""
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		exeDir = filepath.Dir(self)
	}
	return isUnhelpfulCwd(cwd, exeDir)
}

func isUnhelpfulCwd(cwd, exeDir string) bool {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return true
	}
	slash := filepath.ToSlash(cwd)
	if strings.Contains(slash, ".app/Contents") {
		return true
	}
	if exeDir == "" {
		return false
	}
	if !sameDir(cwd, exeDir) {
		return false
	}
	return !looksLikeSourceTree(exeDir)
}

func looksLikeSourceTree(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "chrome", "Cargo.toml")); err != nil {
		return false
	}
	return true
}

func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	if errA == nil && errB == nil && ca == cb {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
