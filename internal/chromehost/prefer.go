package chromehost

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvUI selects which GUI `suzuri` (no subcommand) launches.
//
//	chrome / native — suzuri-chrome (native GPU)
//	classic / ebiten / legacy — classic ebiten UI
//
// Empty: prefer chrome when a chrome binary is resolvable (install layout or
// dev release build); otherwise classic.
const EnvUI = "SUZURI_UI"

// PreferChromeUI reports whether the default `suzuri` entrypoint should launch
// native chrome instead of classic ebiten.
func PreferChromeUI() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvUI))) {
	case "classic", "ebiten", "legacy":
		return false
	case "chrome", "native":
		return true
	}
	// Default: chrome when binary is available (sibling install or cargo release).
	_, err := ResolveBinary()
	return err == nil
}

// SiblingChromeAvailable is true when suzuri-chrome sits next to the running
// suzuri executable (product install layout). Does not search PATH or dev trees.
func SiblingChromeAvailable() bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	dir := filepath.Dir(self)
	return firstExisting(dir, binaryNames()) != ""
}
