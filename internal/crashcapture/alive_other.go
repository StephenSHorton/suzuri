//go:build !windows

package crashcapture

import (
	"errors"
	"syscall"
)

// processAlive reports whether m's process still runs (signal 0 probe).
func processAlive(m Marker) bool {
	err := syscall.Kill(m.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
