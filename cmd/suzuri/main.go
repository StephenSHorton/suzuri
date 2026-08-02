package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/mcpsrv"
	"github.com/StephenSHorton/suzuri/internal/ui"
	"github.com/StephenSHorton/suzuri/internal/update"
)

// version is injected at release build time:
//
//	go build -ldflags "-X main.version=0.6.0" ./cmd/suzuri
//
// Dev/local builds stay "dev" and never auto-update.
var version = "dev"

func main() {
	// Subcommands before GUI init. `suzuri mcp` is spawn-on-demand stdio MCP
	// (Grok starts it; no always-on daemon). See docs/mcp.md.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		// MCP speaks on stdio — never send applog to stdout.
		if err := mcpsrv.RunStdio(); err != nil {
			fmt.Fprintf(os.Stderr, "suzuri mcp: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			// applog may or may not be up; best-effort.
			log.Error("fatal panic",
				"err", fmt.Sprint(r),
				"stack", string(debug.Stack()),
			)
			applog.Close()
			os.Exit(2)
		}
		applog.Close()
	}()

	path, err := applog.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: log init: %v (continuing with stderr)\n", err)
	} else {
		log.Info("logging to file", "path", path)
	}

	switch runtime.GOOS {
	case "windows", "darwin":
	default:
		log.Error("unsupported OS", "goos", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "suzuri supports Windows and macOS.")
		os.Exit(1)
	}

	// Win32/AppKit UI loops must stay on one OS thread.
	// Without this, the Go scheduler can migrate the UI goroutine after idle
	// and the window stops processing messages ("Not Responding").
	runtime.LockOSThread()

	// Process-private Nerd Font (embedded TTF) — no system install required.
	if ui.RegisterBundledFonts() {
		log.Info("bundled font ready", "face", ui.BundledFace)
	} else {
		log.Warn("bundled font unavailable; using system monospaced fallbacks")
	}
	defer ui.UnregisterBundledFonts()

	// Leftover from a previous portable update (renamed running image).
	update.CleanupOldBinary()

	// Fire-and-forget auto-update (release builds only; never blocks UI start).
	upd := update.New("StephenSHorton/suzuri", version)
	go upd.AutoUpdate()
	ui.SetUpdater(upd)

	log.Info("starting",
		"pid", os.Getpid(),
		"goos", runtime.GOOS,
		"goarch", runtime.GOARCH,
		"version", version,
		"go", runtime.Version(),
	)

	if err := ui.Run(); err != nil {
		log.Error("ui.Run failed", "err", err)
		fmt.Fprintf(os.Stderr, "suzuri: %v\n", err)
		if path != "" {
			fmt.Fprintf(os.Stderr, "suzuri: see log %s\n", path)
		}
		os.Exit(1)
	}
	log.Info("exiting cleanly", "pid", os.Getpid())
}
