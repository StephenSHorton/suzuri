package transfer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// ConfigDir is where the engine stores identity/contacts.
// Under suzuri's app support tree: …/suzuri/transfer/
func ConfigDir() (string, error) {
	if p := os.Getenv("HATO_CONFIG_DIR"); p != "" {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return "", err
		}
		return p, nil
	}
	dir := filepath.Join(config.Dir(), "transfer")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("transfer config dir: %w", err)
	}
	return dir, nil
}
