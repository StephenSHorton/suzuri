package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/log"
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
	if t.barAwaiting || t.foreground || t.childOwnsTTY() {
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
	before := t.programOwnsKeys()
	t.barAwaiting = true
	t.noteIO()
	t.logOwnerFlip(before, "bar-submit")
}

// markCommandStarted latches keyboard ownership from the shell's preexec
// mark (OSC 7879;start). A later cwd report or paste-mode toggle must not
// clear it; only the prompt-returned mark does.
func (t *tab) markCommandStarted() {
	if t == nil {
		return
	}
	before := t.programOwnsKeys()
	t.foreground = true
	t.barAwaiting = true
	t.logOwnerFlip(before, "command-start")
}

// markShellIdle: the shell says the prompt is back.
// A finish mark while some other process still owns the terminal is the
// hook racing the fork. Leave the latch; the process group releases it.
func (t *tab) markShellIdle() {
	if t == nil {
		return
	}
	if t.childOwnsTTY() {
		return
	}
	before := t.programOwnsKeys()
	t.barAwaiting = false
	t.foreground = false
	t.logOwnerFlip(before, "shell-idle")
}

// clearCmdQueue drops pending lines (e.g. Ctrl+C while waiting).
func (t *tab) clearCmdQueue() int {
	if t == nil {
		return 0
	}
	n := len(t.cmdQueue)
	before := t.programOwnsKeys()
	t.cmdQueue = nil
	t.barAwaiting = false
	t.foreground = false
	t.logOwnerFlip(before, "clear-queue")
	return n
}

// cwdOSCEndsCommand is true for shells whose only "prompt is back" mark is
// the cwd OSC. bash, zsh, and PowerShell also emit a finish OSC; a cwd report
// from those can be a resize redraw and must not end the command.
func (t *tab) cwdOSCEndsCommand() bool {
	if t == nil {
		return false
	}
	base := strings.ToLower(shellBaseName(t.shell))
	return base == "cmd" || base == "cmd.exe"
}

// childOwnsTTY is true when a program other than the shell is in the
// foreground of the PTY. That is the fact the shell's OSC marks were racing.
func (t *tab) childOwnsTTY() bool {
	if t == nil || !t.alive.Load() {
		return false
	}
	if t.forceChild {
		return true
	}
	if t.sess == nil {
		return false
	}
	shell := t.sess.Pid()
	if shell <= 0 {
		return false
	}
	pg := t.sess.ForegroundPGID()
	return pg > 0 && pg != shell
}

// noteTTYOwner tracks the foreground process group. A child keeps the
// keyboard. Once that child has been seen and the shell has the terminal
// again, the prompt is back.
func (t *tab) noteTTYOwner() bool {
	if t == nil {
		return false
	}
	if t.childOwnsTTY() {
		t.ttyChild = true
		return true
	}
	if t.ttyChild {
		t.ttyChild = false
		t.foreground = false
		t.barAwaiting = false
	}
	return false
}

// programOwnsKeys is true while a foreground program should take keystrokes.
// The PTY foreground group is authoritative. Enter also hides the bar
// (barAwaiting) before that group switch, and OSC 7879;start latches the
// same state until the prompt mark or the shell takes the terminal back.
func (t *tab) programOwnsKeys() bool {
	if t == nil || !t.alive.Load() || t.altScreen() {
		return false
	}
	if t.noteTTYOwner() {
		return true
	}
	return t.foreground || t.barAwaiting
}

func (t *tab) logOwnerFlip(before bool, why string) {
	if t == nil {
		return
	}
	after := t.programOwnsKeys()
	if before == after {
		return
	}
	log.Info("input owner", "tab", t.id, "program", after, "why", why,
		"foreground", t.foreground, "awaiting", t.barAwaiting)
}

// maybeReleaseBarAwaiting does not treat silence as a returned prompt.
// Callers still invoke it; the prompt OSC is what clears barAwaiting.
func (t *tab) maybeReleaseBarAwaiting() {}

// discardCmdQueue drops lines waiting for the next prompt without handing
// the keyboard back. Ctrl+C while a program owns the block uses this so a
// cancelled job does not flush into the next prompt, and the cursor stays
// with the process until the shell actually returns.
func (t *tab) discardCmdQueue() int {
	if t == nil {
		return 0
	}
	n := len(t.cmdQueue)
	t.cmdQueue = nil
	return n
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
