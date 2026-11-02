//go:build !windows

package crashcapture

import (
	"os"
	"runtime/debug"
	"syscall"
)

// SetupTraceback: dev builds use GOTRACEBACK=crash (all goroutines, then
// abort for a core/crash report). Release builds keep Go's default.
func SetupTraceback(version string) {
	if version == "dev" {
		debug.SetTraceback("crash")
	}
}

// nativeAccessViolation: on macOS the closest stand-in for a native hard
// death is SIGKILL (no Go trace, no clean exit), which exercises the marker
// and the next-launch notice.
func nativeAccessViolation() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}
