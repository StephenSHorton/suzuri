//go:build darwin

package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// readClipboardImageFileUI writes the macOS pasteboard raster to a temp PNG.
// Must run on the ebiten/AppKit UI thread — NSPasteboard is not thread-safe.
// Empty / non-image pasteboards return "".
func readClipboardImageFileUI() (path string, err error) {
	dir := filepath.Join(os.TempDir(), "suzuri-paste")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	name := fmt.Sprintf("clip-%d.png", time.Now().UnixNano())
	out := filepath.Join(dir, name)

	ok, nerr := writeClipboardPNGNative(out)
	if nerr != nil {
		_ = os.Remove(out)
		return "", nerr
	}
	if !ok {
		_ = os.Remove(out)
		return "", nil
	}
	if valid, verr := clipboardPNGLooksValid(out); verr != nil {
		_ = os.Remove(out)
		return "", verr
	} else if !valid {
		_ = os.Remove(out)
		return "", nil
	}
	return out, nil
}

// readClipboardImageFileOsascript is safe off-thread (no AppKit). Used when
// the UI-thread native dump finds nothing. May need Automation on hardened builds.
func readClipboardImageFileOsascript() (path string, err error) {
	dir := filepath.Join(os.TempDir(), "suzuri-paste")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	name := fmt.Sprintf("clip-%d.png", time.Now().UnixNano())
	out := filepath.Join(dir, name)

	script := fmt.Sprintf(`
try
  set pngData to the clipboard as «class PNGf»
  set outPath to POSIX file "%s"
  set f to open for access outPath with write permission
  set eof of f to 0
  write pngData to f
  close access f
  return "ok"
on error errMsg number errNum
  return "err " & errNum
end try
`, applescriptEscape(out))

	cmd := exec.Command("osascript", "-e", script)
	outBytes, runErr := cmd.CombinedOutput()
	result := strings.TrimSpace(string(outBytes))
	if runErr != nil {
		_ = os.Remove(out)
		return "", fmt.Errorf("osascript: %w (%s)", runErr, result)
	}
	if !strings.HasPrefix(result, "ok") {
		_ = os.Remove(out)
		return "", nil
	}
	if valid, verr := clipboardPNGLooksValid(out); verr != nil {
		_ = os.Remove(out)
		return "", verr
	} else if !valid {
		_ = os.Remove(out)
		return "", nil
	}
	return out, nil
}

// readClipboardImageFile is kept for tests / callers that run on the UI thread.
// Prefer readClipboardImageFileUI explicitly in host code.
func readClipboardImageFile() (path string, err error) {
	if p, e := readClipboardImageFileUI(); e != nil {
		return "", e
	} else if p != "" {
		return p, nil
	}
	return readClipboardImageFileOsascript()
}

func clipboardPNGLooksValid(path string) (bool, error) {
	st, statErr := os.Stat(path)
	if statErr != nil || st.Size() < 32 {
		return false, nil
	}
	f, openErr := os.Open(path)
	if openErr != nil {
		return false, openErr
	}
	var magic [8]byte
	_, _ = f.Read(magic[:])
	_ = f.Close()
	if string(magic[:4]) != "\x89PNG" {
		return false, nil
	}
	return true, nil
}

func applescriptEscape(s string) string {
	// AppleScript double-quoted string: backslash and quote.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
