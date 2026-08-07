//go:build darwin

package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// HealMacAppBundle repairs .app installs broken by older portable-zip updates:
// missing NSMicrophoneUsageDescription / Apple Events strings, stale bundle
// version, or Hardened Runtime without device.audio-input.
//
// Call once at process start. When a re-sign is required, this relaunches the
// fixed binary and exits so TCC sees the new signature (needed on every Mac
// that only ever in-app-updated through 0.9.83).
func HealMacAppBundle(version string) {
	if version == "" || version == "dev" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return
	}
	app := appBundleRoot(exe)
	if app == "" {
		return
	}
	plist := filepath.Join(app, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return
	}

	needPlist := !plutilStringEquals(plist, "NSMicrophoneUsageDescription", micUsageDescription) ||
		!plutilStringEquals(plist, "NSAppleEventsUsageDescription", appleEventsUsage) ||
		!plutilStringEquals(plist, "CFBundleShortVersionString", version)

	needEntFile := ensureBundledEntitlements(app)
	needAudio := !codesignHasAudioInput(app) && !codesignHasAudioInput(exe)

	if !needPlist && !needEntFile && !needAudio {
		return
	}

	log.Info("update: healing macOS app bundle for mic/TCC",
		"app", app,
		"plist", needPlist,
		"entitlements_file", needEntFile,
		"audio_input", needAudio,
	)

	if needPlist {
		patchAppInfoPlist(exe, version)
	}
	// Always re-sign when we touched the bundle so Info.plist + entitlements bind.
	resignMacExecutable(exe)

	// Relaunch so the running process matches the new code signature.
	// Without this, Hardened Runtime / TCC can still deny mic for this PID.
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		log.Warn("update: heal relaunch failed", "err", err)
		return
	}
	log.Info("update: heal relaunch started; exiting old process")
	go func() {
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}()
	// Block briefly so Exit runs; UI must not start on a half-healed process.
	time.Sleep(2 * time.Second)
}

func plutilStringEquals(plist, key, want string) bool {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", plist).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == want
}

func ensureBundledEntitlements(app string) (wrote bool) {
	dst := filepath.Join(app, "Contents", "Resources", "entitlements.plist")
	if st, err := os.Stat(dst); err == nil && !st.IsDir() && st.Size() > 0 {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		log.Debug("update: Resources mkdir", "err", err)
		return false
	}
	if err := os.WriteFile(dst, []byte(embeddedEntitlementsPlist), 0o644); err != nil {
		log.Debug("update: write entitlements.plist", "err", err)
		return false
	}
	return true
}

func codesignHasAudioInput(path string) bool {
	out, err := exec.Command("codesign", "-d", "--entitlements", ":-", path).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "device.audio-input")
}
