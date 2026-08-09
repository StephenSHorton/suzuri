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

// readClipboardImageFile writes the macOS pasteboard raster (if any) to a
// temp PNG and returns its path. Empty / non-image pasteboards return "".
//
// Prefer in-process NSPasteboard (AppKit) so Hardened Runtime / re-sign does
// not block image paste the way osascript Apple Events can. Fall back to
// osascript «class PNGf» when native dump finds nothing (or is unavailable).
func readClipboardImageFile() (path string, err error) {
	dir := filepath.Join(os.TempDir(), "suzuri-paste")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	// Unique name so rapid pastes don't clobber each other.
	name := fmt.Sprintf("clip-%d.png", time.Now().UnixNano())
	out := filepath.Join(dir, name)

	if ok, nerr := writeClipboardPNGNative(out); nerr != nil {
		_ = os.Remove(out)
		// Fall through to osascript — do not fail the whole paste yet.
	} else if ok {
		if valid, verr := clipboardPNGLooksValid(out); verr != nil || !valid {
			_ = os.Remove(out)
		} else {
			return out, nil
		}
	} else {
		_ = os.Remove(out)
	}

	// Fallback: AppleScript coerce clipboard → PNG (needs Automation on some builds).
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
		return "", nil // no image — not an error
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
