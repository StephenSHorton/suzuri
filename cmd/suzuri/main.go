package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/StephenSHorton/suzuri/internal/ui"
)

func main() {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "suzuri v0 only runs on Windows (ConPTY).")
		os.Exit(1)
	}

	// Win32 requires CreateWindow + GetMessage + WndProc on one OS thread.
	// Without this, the Go scheduler can migrate the UI goroutine after idle
	// and the window stops processing messages ("Not Responding").
	runtime.LockOSThread()

	if err := ui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: %v\n", err)
		os.Exit(1)
	}
}
