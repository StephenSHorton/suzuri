package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/StephenSHorton/suzuri/internal/chromehost"
	"github.com/StephenSHorton/suzuri/internal/mcpsrv"
	"github.com/StephenSHorton/suzuri/internal/transfer"
	"github.com/StephenSHorton/suzuri/internal/update"
	"github.com/StephenSHorton/suzuri/internal/winconsole"
	"github.com/StephenSHorton/suzuri/internal/workspacesync"
)

// version is injected at release build time:
//
//	go build -ldflags "-X main.version=0.6.0" ./cmd/suzuri
//
// Windows release builds also use -H windowsgui so double-click does not open a
// console. CLI subcommands reattach to the parent console when needed.
//
// Dev/local builds stay "dev" and never auto-update.
var version = "dev"

func main() {
	// Subcommands before GUI. `suzuri mcp` is spawn-on-demand stdio MCP.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		// MCP speaks on inherited pipes — do not AttachConsole.
		if err := mcpsrv.RunStdio(); err != nil {
			fmt.Fprintf(os.Stderr, "suzuri mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		winconsole.AttachParent()
		fmt.Println(version)
		return
	}
	// P2P transfer CLI (shells out to suzuri-transfer engine).
	if len(os.Args) > 1 && transfer.IsTransferArg(os.Args[1]) {
		winconsole.AttachParent()
		os.Exit(transfer.RunCLI(os.Args[1:]))
	}
	// Opt-in workspace iroh sync (shells out to suzuri-workspace-sync).
	if len(os.Args) > 1 && workspacesync.IsArg(os.Args[1]) {
		winconsole.AttachParent()
		os.Exit(workspacesync.RunCLI(os.Args[1:]))
	}

	// Product GUI is native only (Rust/wgpu). There is no Charm/ebiten path.
	// `suzuri chrome …` remains as an explicit alias that forwards extra args.
	winconsole.AttachParent()
	update.CleanupOldBinary()
	update.HealMacAppBundle(version)

	switch runtime.GOOS {
	case "windows", "darwin":
	default:
		fmt.Fprintln(os.Stderr, "suzuri supports Windows and macOS.")
		os.Exit(1)
	}

	args := []string(nil)
	if len(os.Args) > 1 && os.Args[1] == "chrome" {
		args = os.Args[2:]
	} else if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "suzuri: unknown command %q\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "usage: suzuri | suzuri mcp | suzuri version | suzuri transfer … | suzuri workspace-sync …")
		os.Exit(2)
	}
	os.Exit(chromehost.RunCLI(version, args))
}
