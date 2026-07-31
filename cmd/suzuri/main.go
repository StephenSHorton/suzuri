package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/StephenSHorton/suzuri/internal/host"
	"github.com/StephenSHorton/suzuri/internal/ui"
)

func main() {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(os.Stderr, "suzuri v0 only runs on Windows (ConPTY).")
		os.Exit(1)
	}

	cols, rows := 100, 30
	sess, err := host.StartSession(host.DefaultShell(), cols, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: start session: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	if err := ui.Run(sess); err != nil {
		fmt.Fprintf(os.Stderr, "suzuri: %v\n", err)
		os.Exit(1)
	}
}
