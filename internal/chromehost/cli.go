package chromehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// RunCLI handles `suzuri chrome [args…]`: resolve binary, spawn, wait for exit.
// Extra args are forwarded to suzuri-chrome. Returns a process exit code.
//
// Default `suzuri` (no subcommand) still opens the classic ebiten UI; this
// path is chrome-only and does not start ebiten.
func RunCLI(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, err := Start(ctx, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri chrome: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: build with `cd chrome && cargo build --release` or set SUZURI_CHROME")
		return 1
	}

	err = cmd.Wait()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	// ctx cancel / signal: chrome usually exits; report 130 for SIGINT-style stop.
	if ctx.Err() != nil {
		return 130
	}
	fmt.Fprintf(os.Stderr, "suzuri chrome: %v\n", err)
	return 1
}
