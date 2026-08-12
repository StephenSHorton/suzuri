package chromehost

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// EnvConfigDir is passed to chrome so host and chrome share the product data dir.
const EnvConfigDir = "SUZURI_CONFIG_DIR"

// Start resolves suzuri-chrome, spawns it as a child process (never via macOS
// `open`), and returns the running command. The caller owns Wait / Kill.
//
// stdio is inherited so chrome logs appear on the parent terminal when launched
// as `suzuri chrome`. On Windows this uses CreateProcess (Go exec); HideWindow
// is not set — chrome is a GUI app that must show its window.
//
// Env includes SUZURI_CONFIG_DIR=config.Dir() so chrome can share notes/prefs
// with the classic host when it honors that variable.
func Start(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bin, err := ResolveBinary()
	if err != nil {
		return nil, err
	}

	//nolint:gosec // binary path is resolved by us; args are host-controlled.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = withConfigDir(os.Environ(), config.Dir())

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	return cmd, nil
}

func withConfigDir(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	// Replace any existing SUZURI_CONFIG_DIR so the product dir wins.
	prefix := EnvConfigDir + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if len(e) >= len(prefix) && e[:len(prefix)] == prefix {
			continue
		}
		out = append(out, e)
	}
	return append(out, prefix+dir)
}
