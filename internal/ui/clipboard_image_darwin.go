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
// Screenshots and "Copy Image" often expose «class PNGf» (or can be coerced
// to it). We avoid linking AppKit: osascript + a temp file is enough and
// matches how Grok itself falls back when native reads fail.
func readClipboardImageFile() (path string, err error) {
	dir := filepath.Join(os.TempDir(), "suzuri-paste")
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return "", mkErr
	}
	// Unique name so rapid pastes don't clobber each other.
	name := fmt.Sprintf("clip-%d.png", time.Now().UnixNano())
	out := filepath.Join(dir, name)

	// AppleScript: coerce clipboard → PNG bytes, write to out.
	// Paths must be escaped for AppleScript string literals.
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
	st, statErr := os.Stat(out)
	if statErr != nil || st.Size() < 32 {
		_ = os.Remove(out)
		return "", nil
	}
	// Sanity: PNG magic
	f, openErr := os.Open(out)
	if openErr != nil {
		_ = os.Remove(out)
		return "", openErr
	}
	var magic [8]byte
	_, _ = f.Read(magic[:])
	_ = f.Close()
	if string(magic[:4]) != "\x89PNG" {
		_ = os.Remove(out)
		return "", nil
	}
	return out, nil
}

func applescriptEscape(s string) string {
	// AppleScript double-quoted string: backslash and quote.
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

// bracketedPaste frames text for terminals that enable DECSET 2004.
// Grok always has bracketed paste on and routes Event::Paste through the
// image / drop-path classifier.
func bracketedPaste(text string) []byte {
	// Normalize newlines the way host terminals do inside paste brackets.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r")
	return []byte("\x1b[200~" + text + "\x1b[201~")
}
