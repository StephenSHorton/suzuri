package workspacesync

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// IsArg reports whether os.Args[1] is the workspace-sync subcommand.
func IsArg(arg string) bool {
	return arg == "workspace-sync"
}

// RunCLI shells out to suzuri-workspace-sync with the remaining args.
// args[0] is "workspace-sync". Returns a process exit code.
func RunCLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: suzuri workspace-sync [listen|join] …")
		return 2
	}
	bin, err := ResolveBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "suzuri workspace-sync: %v\n", err)
		return 1
	}
	cmd := exec.Command(bin, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "suzuri workspace-sync: %v\n", err)
		return 1
	}
	return 0
}
