package chromehost

import (
	"os"
	"path/filepath"
	"strings"
)

// LaunchCwd is the working directory new shells (and the UI process) should
// inherit. Dock / Launch Services start a .app in Contents/Resources; Windows
// Start Menu / MSIX / Win+R often start in C:\Windows\System32; installers
// start in the folder that contains suzuri.exe. Those are not useful project
// directories — prefer $HOME. A real project cwd (or a source checkout that
// contains go.mod + chrome/Cargo.toml) is kept.
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

// IsUnhelpfulCwd reports whether cwd is an app-bundle, Windows system, or
// install-tree path that should not become the user's first shell directory.
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
	if isWindowsDriveRoot(cwd) || isWindowsSystemCwd(cwd) {
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

// isWindowsDriveRoot is true for "C:", "C:\", "C:/".
func isWindowsDriveRoot(cwd string) bool {
	s := strings.TrimSpace(filepath.ToSlash(cwd))
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	if !isDriveLetter(s[0]) {
		return false
	}
	rest := strings.Trim(s[2:], "/")
	return rest == ""
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// isWindowsSystemCwd is true for Windows, System32, Program Files, ProgramData
// (Start Menu / MSIX / Win+R typical launch directories).
func isWindowsSystemCwd(cwd string) bool {
	s := strings.ToLower(filepath.ToSlash(strings.TrimSpace(cwd)))
	s = strings.TrimRight(s, "/")
	if s == "" {
		return false
	}
	if len(s) >= 2 && s[1] == ':' && isDriveLetter(s[0]) {
		s = s[2:]
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	switch {
	case s == "/windows", strings.HasPrefix(s, "/windows/"):
		return true
	case s == "/program files", strings.HasPrefix(s, "/program files/"):
		return true
	case s == "/program files (x86)", strings.HasPrefix(s, "/program files (x86)/"):
		return true
	case s == "/programdata", strings.HasPrefix(s, "/programdata/"):
		return true
	default:
		return false
	}
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
