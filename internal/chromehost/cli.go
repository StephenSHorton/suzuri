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

// RunCLI handles `suzuri chrome [args…]`: resolve binary, start MCP bridge
// proxy, spawn chrome, wait for exit. Extra args are forwarded to
// suzuri-chrome. Returns a process exit code.
//
// When PreferChromeUI is true, the default `suzuri` entry also uses this path
// (classic ebiten is SUZURI_UI=classic).
func RunCLI(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bridge while chrome runs so `suzuri mcp` attaches to native UI the same
	// way it attaches to classic ebiten (loopback + bridge.json).
	host, err := startChromeBridge(os.Getpid())
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri chrome: mcp bridge: %v (continuing without bridge)\n", err)
	} else {
		defer host.Stop()
		logBridgeStart(host)
		pubStop := make(chan struct{})
		defer close(pubStop)
		go runStatusPublisher(host, os.Getpid(), pubStop)
	}

	cmd, err := Start(ctx, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri chrome: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: build with `cd chrome && cargo build --release` or set SUZURI_CHROME")
		fmt.Fprintln(os.Stderr, "hint: set SUZURI_UI=classic to force classic ebiten UI")
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
	fmt.Fprintf(os.Stderr, "suzuri chrome: %v\n", err)
	return 1
}
