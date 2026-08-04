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
func resignMacExecutable(exePath string) {
	if exePath == "" {
		return
	}
	identity := strings.TrimSpace(os.Getenv("SUZURI_CODESIGN_IDENTITY"))
	if identity == "" {
		identity = codesignIdentityOf(exePath + ".old")
	}
	if identity == "" {
		identity = "-" // ad-hoc
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
	args := []string{"--force", "--sign", identity, "--identifier", identifier, "--timestamp"}
	// Hardened runtime + entitlements for Developer ID (matches packaging/macos/build-app.sh).
	if identity != "-" && strings.HasPrefix(identity, "Developer ID") {
		args = append(args, "--options", "runtime")
		if ent := findEntitlementsPlist(); ent != "" {
			args = append(args, "--entitlements", ent)
		}
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

// findEntitlementsPlist looks for the shipping entitlements next to a
// development checkout, then next to the running binary (not usually present).
func findEntitlementsPlist() string {
	candidates := []string{
		"packaging/macos/entitlements.plist",
	}
	if exe, err := os.Executable(); err == nil {
		// .../suzuri.app/Contents/MacOS/suzuri → not useful; skip
		_ = exe
	}
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
