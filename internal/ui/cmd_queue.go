package ui

import (
	"time"
)

// How long the shell must be quiet after a bar command before we treat it as
// idle and flush the next queued line. Slightly longer than tabBusyWindow so
// we don't fire mid-output between chunks.
const cmdQueueIdle = 900 * time.Millisecond

// Max queued Warp-bar lines per tab (drop oldest if exceeded).
const cmdQueueMax = 32

// queuedCmd is one Warp-bar submit held until the shell is idle.
type queuedCmd struct {
	display string // block header text
	payload string // bytes to send (no trailing CR)
}

// barShouldQueue is true when a new Warp-bar submit should wait instead of
// going straight to the PTY (primary shell only).
func (t *tab) barShouldQueue() bool {
	if t == nil || !t.alive.Load() {
		return false
	}
	// Alt-screen apps own the keyboard path; bar is usually hidden anyway.
	if t.altScreen() {
		return false
	}
	if t.barAwaiting {
		return true
	}
	// Mid-job: recent PTY activity without an idle gap (e.g. long build).
	return t.shellBusyForQueue()
}

func (t *tab) shellBusyForQueue() bool {
	if t == nil || t.altScreen() {
		return false
	}
	ns := t.lastIOUnixNano.Load()
	if ns == 0 {
		return false
	}
	return time.Since(time.Unix(0, ns)) < cmdQueueIdle
}

func (t *tab) shellQuietFor(d time.Duration) bool {
	if t == nil {
		return true
	}
	ns := t.lastIOUnixNano.Load()
	if ns == 0 {
		return true
	}
	return time.Since(time.Unix(0, ns)) >= d
}

// enqueueBarCmd appends a line to the wait queue. Returns new queue length.
func (t *tab) enqueueBarCmd(display, payload string) int {
	if t == nil {
		return 0
	}
	t.cmdQueue = append(t.cmdQueue, queuedCmd{display: display, payload: payload})
	if len(t.cmdQueue) > cmdQueueMax {
		t.cmdQueue = t.cmdQueue[len(t.cmdQueue)-cmdQueueMax:]
	}
	return len(t.cmdQueue)
}

func (t *tab) queueLen() int {
	if t == nil {
		return 0
	}
	return len(t.cmdQueue)
}

// markBarCommandSent: we just injected a bar command; further submits queue.
func (t *tab) markBarCommandSent() {
	if t == nil {
		return
	}
	t.barAwaiting = true
	t.noteIO()
}

// markShellIdle: prompt returned / quiet — allow flush.
func (t *tab) markShellIdle() {
	if t == nil {
		return
	}
	t.barAwaiting = false
}

// clearCmdQueue drops pending lines (e.g. Ctrl+C while waiting).
func (t *tab) clearCmdQueue() int {
	if t == nil {
		return 0
	}
	n := len(t.cmdQueue)
	t.cmdQueue = nil
	t.barAwaiting = false
	return n
}

// maybeReleaseBarAwaiting clears barAwaiting once the shell has been quiet.
func (t *tab) maybeReleaseBarAwaiting() {
	if t == nil || !t.barAwaiting {
		return
	}
	if t.altScreen() {
		return
	}
	if t.shellQuietFor(cmdQueueIdle) {
		t.barAwaiting = false
	}
}

// popCmdQueue returns the next queued command, or false if empty / still busy.
func (t *tab) popCmdQueue() (queuedCmd, bool) {
	if t == nil || len(t.cmdQueue) == 0 {
		return queuedCmd{}, false
	}
	t.maybeReleaseBarAwaiting()
	if t.barShouldQueue() {
		return queuedCmd{}, false
	}
	cmd := t.cmdQueue[0]
	t.cmdQueue = t.cmdQueue[1:]
	return cmd, true
}
