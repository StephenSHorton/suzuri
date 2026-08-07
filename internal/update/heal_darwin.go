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

// HealMacAppBundle repairs .app installs broken by older portable-zip updates
// (missing NSMicrophoneUsageDescription / Apple Events / version strings).
//
// Critical constraints (0.9.87 incident):
//   - Never write files into a sealed Developer ID / notarized bundle without a
//     successful re-sign — that makes Gatekeeper say “damaged”.
//   - Never relaunch in a loop. Stamp under Application Support (outside the
//     app) so one attempt per version.
//   - Do not require device.audio-input for heal success; ad-hoc re-sign cannot
//     restore shipping notarization.
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

	// Outside the sealed bundle — never Contents/MacOS/.
	stamp := healStampPath(version)
	if _, err := os.Stat(stamp); err == nil {
		return
	}

	// Intact notarized/Developer ID install with privacy keys: leave alone.
	if appCodeSignatureOK(app) &&
		plutilStringEquals(plist, "NSMicrophoneUsageDescription", micUsageDescription) &&
		plutilStringEquals(plist, "NSAppleEventsUsageDescription", appleEventsUsage) {
		_ = writeHealStamp(stamp, "signature-ok\n")
		return
	}

	needPlist := !plutilStringEquals(plist, "NSMicrophoneUsageDescription", micUsageDescription) ||
		!plutilStringEquals(plist, "NSAppleEventsUsageDescription", appleEventsUsage) ||
		!plutilStringEquals(plist, "CFBundleShortVersionString", version)

	if !needPlist {
		// Signature may be broken or ad-hoc; we cannot safely re-notarize here.
		_ = writeHealStamp(stamp, "no-plist-work\n")
		if !appCodeSignatureOK(app) {
			log.Warn("update: app code signature invalid; reinstall from .dmg/.app.zip if Gatekeeper blocks launch")
		}
		return
	}

	log.Info("update: healing macOS Info.plist privacy/version keys", "app", app)

	// Stamp first so a crash mid-heal cannot loop.
	_ = writeHealStamp(stamp, "healing\n")

	patchAppInfoPlist(exe, version)

	// Only re-sign if we have a real Developer ID identity (env or existing
	// Authority). Ad-hoc re-sign after a notarized install causes “damaged”.
	identity := resolveCodesignIdentity(exe)
	if identity == "" || identity == "-" {
		log.Warn("update: patched Info.plist but cannot re-sign with Developer ID; install full .app.zip/.dmg to fix Gatekeeper if needed")
		_ = writeHealStamp(stamp, "plist-only\n")
		return
	}

	resignMacExecutable(exe)
	_ = writeHealStamp(stamp, "resigned\n")

	if !appCodeSignatureOK(app) {
		log.Warn("update: post-heal signature still invalid")
		return
	}

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
	time.Sleep(2 * time.Second)
}

func healStampPath(version string) string {
	// Prefer Application Support (writable, outside seal).
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = filepath.Join(os.TempDir(), "suzuri-heal")
	} else {
		base = filepath.Join(base, "suzuri")
	}
	_ = os.MkdirAll(base, 0o755)
	return filepath.Join(base, "bundle-heal-"+sanitizeVersion(version))
}

func writeHealStamp(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o644)
}

func sanitizeVersion(v string) string {
	v = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, v)
	if v == "" {
		return "unknown"
	}
	return v
}

func plutilStringEquals(plist, key, want string) bool {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", plist).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == want
}

func appCodeSignatureOK(app string) bool {
	// codesign --verify is enough to catch “sealed resource missing/invalid”.
	if err := exec.Command("codesign", "--verify", "--deep", "--strict", app).Run(); err != nil {
		return false
	}
	return true
}

func resolveCodesignIdentity(exePath string) string {
	if id := strings.TrimSpace(os.Getenv("SUZURI_CODESIGN_IDENTITY")); id != "" {
		return id
	}
	for _, p := range []string{exePath, exePath + ".old"} {
		if id := codesignIdentityOf(p); id != "" {
			return id
		}
	}
	if app := appBundleRoot(exePath); app != "" {
		if id := codesignIdentityOf(app); id != "" {
			return id
		}
	}
	return "-"
}

func codesignHasAudioInput(path string) bool {
	out, err := exec.Command("codesign", "-d", "--entitlements", ":-", path).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "device.audio-input")
}
