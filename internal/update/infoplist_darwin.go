//go:build darwin

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/log"
)

// Privacy usage strings kept in sync with packaging/macos/Info.plist.
// In-app updates only replace Contents/MacOS/suzuri; without patching the
// bundle Info.plist, NSMicrophoneUsageDescription never appears and TCC
// silently refuses mic access (no System Settings prompt).
const (
	micUsageDescription = "suzuri needs microphone access when tools like Grok use voice dictation."
	appleEventsUsage    = "suzuri uses AppleScript for clipboard image paste and when tools need Automation."
)

// patchAppInfoPlist updates CFBundle version strings and required TCC usage
// keys on the host .app after a portable-binary in-app update.
func patchAppInfoPlist(exePath, version string) {
	app := appBundleRoot(exePath)
	if app == "" || version == "" {
		return
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return
	}
	// plutil -replace creates the key on modern macOS when missing.
	sets := []struct{ key, val string }{
		{"CFBundleShortVersionString", version},
		{"CFBundleVersion", version},
		{"NSMicrophoneUsageDescription", micUsageDescription},
		{"NSAppleEventsUsageDescription", appleEventsUsage},
	}
	for _, s := range sets {
		if err := plutilSetString(plist, s.key, s.val); err != nil {
			log.Warn("update: Info.plist patch failed", "key", s.key, "err", err)
			return
		}
	}
	log.Info("update: Info.plist privacy + version patched", "path", plist, "version", version)
}

func plutilSetString(plist, key, value string) error {
	// Prefer replace; if the key is absent some older plutil versions error —
	// fall back to insert.
	cmd := exec.Command("plutil", "-replace", key, "-string", value, plist)
	if out, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("plutil", "-insert", key, "-string", value, plist)
		out2, err2 := cmd.CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("plutil replace/insert %s: %w (%s; %s)",
				key, err2, stringsTrim(out), stringsTrim(out2))
		}
	}
	return nil
}

func stringsTrim(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
