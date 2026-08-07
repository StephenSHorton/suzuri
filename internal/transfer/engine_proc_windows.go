//go:build windows

package transfer

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW: console-subsystem children (suzuri-transfer) must not open
// a separate terminal when the parent is a GUI process.
const createNoWindow = 0x08000000

func configureEngineCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
