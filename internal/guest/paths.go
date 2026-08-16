package guest

import (
	"os"
	"path/filepath"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Dir is the product config root (same place chrome looks).
// SUZURI_CONFIG_DIR wins so a host-spawned chrome and this CLI share guests/.
func Dir() string {
	if d := os.Getenv("SUZURI_CONFIG_DIR"); d != "" {
		return d
	}
	return config.Dir()
}

// GuestsDir is `{config}/guests`.
func GuestsDir() string {
	return filepath.Join(Dir(), "guests")
}

// ManifestPath is `{config}/guests/{id}.json`.
func ManifestPath(id string) string {
	return filepath.Join(GuestsDir(), id+".json")
}

// InstallDir is `{config}/guests/{id}/` — the extracted helper lives here.
func InstallDir(id string) string {
	return filepath.Join(GuestsDir(), id)
}
