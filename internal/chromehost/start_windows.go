//go:build windows

package chromehost

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW: a console-subsystem child must not pop a spare terminal
// when the parent is a GUI process (Start Menu / Store / -H windowsgui).
// Do not set HideWindow — that is SW_HIDE and would hide the GPU window.
const createNoWindow = 0x08000000

func configureChromeCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
	}
}
