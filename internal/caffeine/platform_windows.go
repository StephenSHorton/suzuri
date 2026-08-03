//go:build windows

package caffeine

import (
	"sync"

	"golang.org/x/sys/windows"
)

// ES_* flags for SetThreadExecutionState.
const (
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
	esContinuous      = 0x80000000
)

var (
	modKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procSetThreadExecutionState = modKernel32.NewProc("SetThreadExecutionState")
)

type windowsHold struct {
	mu sync.Mutex
	on bool
}

func newPlatformHold() platformHold {
	return &windowsHold{}
}

func (h *windowsHold) acquire() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.on {
		return nil
	}
	// Keep system + display awake for this process while continuous flag is held.
	r, _, err := procSetThreadExecutionState.Call(uintptr(esContinuous | esSystemRequired | esDisplayRequired))
	if r == 0 {
		if err != nil {
			return err
		}
		return windows.ERROR_INVALID_PARAMETER
	}
	h.on = true
	return nil
}

func (h *windowsHold) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.on {
		return
	}
	// Clear the continuous request.
	_, _, _ = procSetThreadExecutionState.Call(uintptr(esContinuous))
	h.on = false
}
