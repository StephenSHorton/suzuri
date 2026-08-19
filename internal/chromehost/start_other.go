//go:build !windows

package chromehost

import "os/exec"

func configureChromeCmd(*exec.Cmd) {}
