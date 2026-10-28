//go:build darwin

package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/log"
)

// releaseTeamID is the Developer ID team that signs release builds.
// A downloaded bundle must carry it before it replaces the installed app.
const releaseTeamID = "GP6632G333"

// stagePrefix names the hidden folder a bundle update unzips into.
const stagePrefix = ".suzuri-update-"

// runningAppBundle is the .app the running binary lives in, or "".
func runningAppBundle() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return ""
	}
	return appBundleRoot(self)
}

// pickAppZipAsset finds the signed, notarized .app.zip for this arch.
func pickAppZipAsset(assets []ghAsset) ghAsset {
	want := fmt.Sprintf("-darwin-%s.app.zip", runtime.GOARCH)
	for _, a := range assets {
		if strings.HasSuffix(strings.ToLower(a.Name), want) {
			return a
		}
	}
	return ghAsset{}
}

// installAppBundle swaps the whole signed .app in for the running one.
//
// Patching files inside a signed bundle breaks its seal, and the updater
// cannot re-sign with Developer ID on a user's Mac, so it fell back to
// ad-hoc. Ad-hoc apps are identified by build hash, so every update
// silently dropped Accessibility and Screen Recording grants. Installing
// the release bundle intact keeps the Developer ID signature, which TCC
// matches by team, so grants carry over.
func installAppBundle(zipPath, app string) error {
	parent := filepath.Dir(app)
	// Stage next to the app so the final rename stays on one volume.
	stage, err := os.MkdirTemp(parent, stagePrefix)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("can't write to %s to update; install the new version from the .dmg instead: %w", parent, err)
		}
		return fmt.Errorf("stage update: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	if out, err := exec.Command("ditto", "-x", "-k", zipPath, stage).CombinedOutput(); err != nil {
		return fmt.Errorf("unzip app: %w: %s", err, strings.TrimSpace(string(out)))
	}
	matches, _ := filepath.Glob(filepath.Join(stage, "*.app"))
	if len(matches) != 1 {
		return fmt.Errorf("app zip holds %d bundles, want 1", len(matches))
	}
	fresh := matches[0]
	if err := verifyReleaseBundle(fresh); err != nil {
		return err
	}

	old := app + ".old"
	if err := os.RemoveAll(old); err != nil {
		return fmt.Errorf("clear old app: %w", err)
	}
	if err := os.Rename(app, old); err != nil {
		return fmt.Errorf("move running app aside: %w", err)
	}
	if err := os.Rename(fresh, app); err != nil {
		if rerr := os.Rename(old, app); rerr != nil {
			log.Error("update: could not restore app", "app", app, "err", rerr)
		}
		return fmt.Errorf("install new app: %w", err)
	}
	log.Info("update: installed signed app bundle", "app", app)
	return nil
}

// rollbackAppBundle puts the previous app back after a failed relaunch, so a
// retry never deletes the bundle this process is still running from.
func rollbackAppBundle(app string) error {
	old := app + ".old"
	if _, err := os.Stat(old); err != nil {
		return err
	}
	failed := app + ".failed"
	_ = os.RemoveAll(failed)
	if err := os.Rename(app, failed); err != nil {
		return err
	}
	if err := os.Rename(old, app); err != nil {
		_ = os.Rename(failed, app)
		return err
	}
	_ = os.RemoveAll(failed)
	return nil
}

// verifyReleaseBundle refuses a bundle that is not intact, signed by the
// release team, and accepted by Gatekeeper.
func verifyReleaseBundle(app string) error {
	if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
		return fmt.Errorf("new app signature invalid: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if team := codesignTeamOf(app); team != releaseTeamID {
		return fmt.Errorf("new app signed by team %q, want %s", team, releaseTeamID)
	}
	if out, err := exec.Command("spctl", "--assess", "--type", "execute", app).CombinedOutput(); err != nil {
		return fmt.Errorf("gatekeeper rejected new app: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func codesignTeamOf(path string) string {
	out, err := exec.Command("codesign", "-dv", "--verbose=2", path).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TeamIdentifier="); ok {
			return v
		}
	}
	return ""
}

// cleanupOldAppBundle removes the .app.old left by a bundle update, and any
// staging folder an interrupted update left next to the app.
func cleanupOldAppBundle() {
	app := runningAppBundle()
	if app == "" {
		return
	}
	stale, _ := filepath.Glob(filepath.Join(filepath.Dir(app), stagePrefix+"*"))
	for _, dir := range stale {
		_ = os.RemoveAll(dir)
	}
	old := app + ".old"
	if _, err := os.Stat(old); err != nil {
		return
	}
	if err := os.RemoveAll(old); err != nil {
		log.Debug("update: could not remove old app", "path", old, "err", err)
		return
	}
	log.Debug("update: removed old app", "path", old)
}
