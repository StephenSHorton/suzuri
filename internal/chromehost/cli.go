package chromehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// RunCLI is the product GUI entry: resolve the native UI binary, start the MCP
// bridge proxy, spawn UI, wait for exit. Extra args are forwarded to the UI
// process. Returns a process exit code.
//
// The binary on disk is still named `suzuri-chrome` (sidecar next to `suzuri`);
// that is an implementation detail — the product is just **suzuri**.
func RunCLI(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bridge while UI runs so `suzuri mcp` attaches to the live window.
	host, err := startChromeBridge(os.Getpid())
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: mcp bridge: %v (continuing without bridge)\n", err)
	} else {
		defer host.Stop()
		logBridgeStart(host)
		pubStop := make(chan struct{})
		defer close(pubStop)
		go runStatusPublisher(host, os.Getpid(), pubStop)
	}

	cmd, err := Start(ctx, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: install a release that includes the UI binary, or build with:")
		fmt.Fprintln(os.Stderr, "  cd chrome && cargo build --release")
		fmt.Fprintln(os.Stderr, "  (or set SUZURI_CHROME to the UI binary path)")
		return 1
	}
	if cmd.Process != nil && host != nil {
		// Re-publish with real chrome pid once known.
		publishChromeStatus(host, cmd.Process.Pid)
	}

	err = cmd.Wait()
	removeChromeStatus()
	// Brief grace so MCP clients see a clean remove of bridge.json after Stop.
	time.Sleep(20 * time.Millisecond)

	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	fmt.Fprintf(os.Stderr, "suzuri: %v\n", err)
	return 1
}
