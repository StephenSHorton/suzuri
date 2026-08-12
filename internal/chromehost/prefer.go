package chromehost

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvUI is retained for compatibility. The product UI is native-only;
// classic/ebiten is no longer a launch path. Values "classic"/"ebiten"/"legacy"
// are ignored (and logged by callers if needed).
const EnvUI = "SUZURI_UI"

// PreferChromeUI always reports true: product GUI is native GPU UI only.
// Kept so older call sites / tests compile without branching on classic.
func PreferChromeUI() bool {
	_ = strings.ToLower(strings.TrimSpace(os.Getenv(EnvUI)))
	return true
}

// SiblingChromeAvailable is true when the UI binary sits next to the running
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
