package chromehost

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// EnvConfigDir is passed to chrome so host and chrome share the product data dir.
const EnvConfigDir = "SUZURI_CONFIG_DIR"

// EnvVersion is the host's release version (`-ldflags -X main.version`).
// Chrome uses it to skip GitHub checks on `dev` builds.
const EnvVersion = "SUZURI_VERSION"

// Start resolves suzuri-chrome, spawns it as a child process (never via macOS
// `open`), and returns the running command. The caller owns Wait / Kill.
//
// stdio is inherited so chrome logs appear on the parent terminal when launched
// as `suzuri chrome`. On Windows this uses CreateProcess (Go exec); HideWindow
// is not set — chrome is a GUI app that must show its window.
//
// Env includes SUZURI_CONFIG_DIR=config.Dir() so chrome can share notes/prefs
// with the host, and SUZURI_VERSION for the in-app updater. The child cwd is
// [LaunchCwd] so Dock / .app launches do not inherit Contents/Resources.
func Start(ctx context.Context, version string, args ...string) (*exec.Cmd, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	bin, err := ResolveBinary()
	if err != nil {
		return nil, err
	}

	// Resolve first (may walk from the original cwd), then leave the bundle.
	if dir := LaunchCwd(); dir != "" {
		if wd, err := os.Getwd(); err != nil || !sameDir(wd, dir) {
			_ = os.Chdir(dir)
		}
	}

	//nolint:gosec // binary path is resolved by us; args are host-controlled.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = LaunchCwd()
	cmd.Env = withHostEnv(os.Environ(), config.Dir(), version)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	return cmd, nil
}

func withConfigDir(env []string, dir string) []string {
	return withReplacedEnv(env, EnvConfigDir, dir)
}

func withHostEnv(env []string, dir, version string) []string {
	env = withReplacedEnv(env, EnvConfigDir, dir)
	env = withReplacedEnv(env, EnvVersion, version)
	return env
}

func withReplacedEnv(env []string, key, val string) []string {
	if val == "" {
		return env
	}
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return append(out, prefix+val)
}
