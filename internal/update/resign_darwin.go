//go:build darwin

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
)

const macBundleID = "com.stephenshorton.suzuri"

// Default Hardened Runtime entitlements when Resources/entitlements.plist is
// missing (older installs). Keep in sync with packaging/macos/entitlements.plist.
const embeddedEntitlementsPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>com.apple.security.automation.apple-events</key>
	<true/>
	<key>com.apple.security.cs.allow-unsigned-executable-memory</key>
	<true/>
	<key>com.apple.security.cs.allow-jit</key>
	<true/>
	<key>com.apple.security.cs.disable-library-validation</key>
	<true/>
	<key>com.apple.security.device.audio-input</key>
	<true/>
	<key>com.apple.security.network.client</key>
	<true/>
	<key>com.apple.security.network.server</key>
	<true/>
</dict>
</plist>
`

// resignMacExecutable re-signs a replaced Mach-O (and its .app parent when
// present) after an in-app update.
//
// Identity resolution (first match wins):
//  1. SUZURI_CODESIGN_IDENTITY env (Developer ID / local self-signed name)
//  2. The identity that signed the previous binary (best-effort from codesign -dv)
//  3. Ad-hoc ("-") with a stable bundle identifier
//
// TCC folder grants survive updates only when (1)/(2) is a stable certificate
// with a Team ID. Ad-hoc always gets a new CDHash and macOS re-prompts.
//
// Developer ID re-sign always applies entitlements (mic, Apple Events, etc.).
// Without --entitlements, codesign drops Hardened Runtime capability grants
// and child tools like Grok /voice fail with silent TCC denial.
func resignMacExecutable(exePath string) {
	if exePath == "" {
		return
	}
	identity := strings.TrimSpace(os.Getenv("SUZURI_CODESIGN_IDENTITY"))
	// Prefer the current binary's identity (before we clobber it), then .old.
	if identity == "" {
		identity = codesignIdentityOf(exePath)
	}
	if identity == "" {
		identity = codesignIdentityOf(exePath + ".old")
	}
	if identity == "" {
		if app := appBundleRoot(exePath); app != "" {
			identity = codesignIdentityOf(app)
		}
	}
	if identity == "" {
		identity = "-" // ad-hoc — last resort; loses Team ID / stable TCC
	}

	// Never replace a notarized Developer ID seal with ad-hoc: Gatekeeper then
	// reports “damaged and can’t be opened” (0.9.87 portable-update incident).
	if identity == "-" {
		if app := appBundleRoot(exePath); app != "" && appHasDeveloperID(app) {
			log.Warn("update: skip ad-hoc re-sign of Developer ID app; install .app.zip/.dmg for a full signed update")
			return
		}
		if appHasDeveloperID(exePath) {
			log.Warn("update: skip ad-hoc re-sign of Developer ID binary")
			return
		}
	}

	// Prefer signing the .app bundle when we live inside Contents/MacOS.
	target := exePath
	if app := appBundleRoot(exePath); app != "" {
		// Nested binary first, then bundle (binds Info.plist).
		if err := runCodesign(identity, macBundleID, exePath); err != nil {
			log.Debug("update: codesign binary", "err", err)
		}
		target = app
	}
	if err := runCodesign(identity, macBundleID, target); err != nil {
		log.Debug("update: codesign", "target", target, "identity", identity, "err", err)
		return
	}
	log.Info("update: re-signed", "target", target, "identity", identity)
}

func appHasDeveloperID(path string) bool {
	id := codesignIdentityOf(path)
	return id != "" && strings.Contains(id, "Developer ID")
}

func appBundleRoot(exePath string) string {
	// .../Foo.app/Contents/MacOS/suzuri → .../Foo.app
	dir := filepath.Dir(exePath) // MacOS
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	app := filepath.Dir(contents)
	if strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}

func codesignIdentityOf(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	out, err := exec.Command("codesign", "-dv", "--verbose=4", path).CombinedOutput()
	if err != nil {
		return ""
	}
	// Look for Authority= lines; first non-ad-hoc authority is the signing cert.
	// Ad-hoc dumps "Signature=adhoc" with no Authority.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authority=") {
			auth := strings.TrimPrefix(line, "Authority=")
			if auth != "" && !strings.EqualFold(auth, "adhoc") {
				return auth
			}
		}
	}
	return ""
}

func runCodesign(identity, identifier, path string) error {
	args := []string{"--force", "--sign", identity, "--identifier", identifier}
	// Timestamp requires a network identity (not ad-hoc).
	if identity != "-" {
		args = append(args, "--timestamp")
	}
	// Always attach shipping entitlements when we have them. Portable updates
	// often re-sign ad-hoc; without --entitlements, audio-input is dropped and
	// older heals looped forever trying to restore it.
	ent, cleanup := resolveEntitlementsPlist(path)
	if cleanup != nil {
		defer cleanup()
	}
	if ent != "" {
		args = append(args, "--entitlements", ent)
	} else {
		log.Warn("update: codesign without entitlements — mic/Automation may fail")
	}
	// Hardened Runtime for real certs (matches packaging/macos/build-app.sh).
	if identity != "-" && (strings.HasPrefix(identity, "Developer ID") || strings.Contains(identity, "Developer ID")) {
		args = append(args, "--options", "runtime")
	}
	if strings.HasSuffix(path, ".app") {
		args = append(args, "--deep")
	}
	args = append(args, path)
	cmd := exec.Command("codesign", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = out
	return nil
}

// resolveEntitlementsPlist returns a path to an entitlements file for codesign.
// Search order: env, app Resources, packaging checkout, embedded fallback (temp file).
func resolveEntitlementsPlist(signedPath string) (path string, cleanup func()) {
	if env := strings.TrimSpace(os.Getenv("SUZURI_ENTITLEMENTS")); env != "" {
		if st, err := os.Stat(env); err == nil && !st.IsDir() {
			return env, nil
		}
	}
	if p := findEntitlementsPlist(signedPath); p != "" {
		return p, nil
	}
	// Write embedded defaults so Developer ID re-sign never drops audio-input.
	f, err := os.CreateTemp("", "suzuri-entitlements-*.plist")
	if err != nil {
		return "", nil
	}
	name := f.Name()
	if _, err := f.WriteString(embeddedEntitlementsPlist); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", nil
	}
	_ = f.Close()
	return name, func() { _ = os.Remove(name) }
}

// findEntitlementsPlist looks for shipping entitlements: app Resources (update
// path), then a development checkout path.
func findEntitlementsPlist(signedPath string) string {
	var candidates []string
	if app := appBundleRoot(signedPath); app != "" {
		candidates = append(candidates, filepath.Join(app, "Contents", "Resources", "entitlements.plist"))
	}
	// signedPath may be the binary inside MacOS.
	if strings.HasSuffix(signedPath, ".app") {
		candidates = append(candidates, filepath.Join(signedPath, "Contents", "Resources", "entitlements.plist"))
	} else if dir := filepath.Dir(signedPath); filepath.Base(dir) == "MacOS" {
		candidates = append(candidates, filepath.Join(filepath.Dir(dir), "Resources", "entitlements.plist"))
	}
	if exe, err := os.Executable(); err == nil {
		if app := appBundleRoot(exe); app != "" {
			candidates = append(candidates, filepath.Join(app, "Contents", "Resources", "entitlements.plist"))
		}
	}
	candidates = append(candidates, "packaging/macos/entitlements.plist")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}
