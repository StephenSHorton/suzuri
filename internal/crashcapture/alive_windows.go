//go:build windows

package crashcapture

import (
	"time"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// processAlive reports whether m's process still runs. A live pid whose
// process started well after the marker is a reused pid, so it counts as
// gone.
func processAlive(m Marker) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(m.PID))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err == nil && !m.Started.IsZero() {
		if time.Unix(0, created.Nanoseconds()).After(m.Started.Add(time.Minute)) {
			return false
		}
	}
	return true
}
