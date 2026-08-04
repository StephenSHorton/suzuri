//go:build darwin

package caffeine

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

// Prevent display + system idle sleep (same assertion type classic Caffeine uses).
static IOReturn suzuri_caffeine_acquire(IOPMAssertionID *outID) {
	CFStringRef type = CFSTR("PreventUserIdleDisplaySleep");
	CFStringRef reason = CFSTR("suzuri caffeine");
	return IOPMAssertionCreateWithName(type, kIOPMAssertionLevelOn, reason, outID);
}

static IOReturn suzuri_caffeine_release(IOPMAssertionID id) {
	return IOPMAssertionRelease(id);
}
*/
import "C"

import (
	"fmt"
	"sync"
)

type darwinHold struct {
	mu  sync.Mutex
	id  C.IOPMAssertionID
	on  bool
}

func newPlatformHold() platformHold {
	return &darwinHold{}
}

func (h *darwinHold) acquire() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.on {
		return nil
	}
	var id C.IOPMAssertionID
	r := C.suzuri_caffeine_acquire(&id)
	if r != 0 {
		return fmt.Errorf("IOPMAssertionCreateWithName: 0x%x", uint32(r))
	}
	h.id = id
	h.on = true
	return nil
}

func (h *darwinHold) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.on {
		return
	}
	_ = C.suzuri_caffeine_release(h.id)
	h.id = 0
	h.on = false
}
