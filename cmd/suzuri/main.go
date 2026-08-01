package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/ui"
)

func main() {
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

	if runtime.GOOS != "windows" {
		log.Error("unsupported OS", "goos", runtime.GOOS)
		fmt.Fprintln(os.Stderr, "suzuri v0 only runs on Windows (ConPTY).")
		os.Exit(1)
	}

	// Win32 requires CreateWindow + GetMessage + WndProc on one OS thread.
	// Without this, the Go scheduler can migrate the UI goroutine after idle
	// and the window stops processing messages ("Not Responding").
	runtime.LockOSThread()

	log.Info("starting",
		"pid", os.Getpid(),
		"goos", runtime.GOOS,
		"goarch", runtime.GOARCH,
		"version", runtime.Version(),
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
