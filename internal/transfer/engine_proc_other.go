//go:build !windows

package transfer

import "os/exec"

func configureEngineCmd(cmd *exec.Cmd) {
	// Unix: engine is a normal subprocess; no console window to hide.
	_ = cmd
}
