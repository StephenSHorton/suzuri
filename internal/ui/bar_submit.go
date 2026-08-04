package ui

import (
	"fmt"
	"strings"
)

// prepareBarSubmit expands aliases and returns display + payload for a Warp-bar line.
func prepareBarSubmit(line, shell string) (display, payload string) {
	return expandBarSubmit(line, shell)
}

// applyBarSubmitToTab commits scrollback block + echo arm for a real send.
// Does not write to the PTY.
func applyBarSubmitToTab(t *tab, display, payload string, cols int) {
	if t == nil {
		return
	}
	if strings.TrimSpace(display) == "" {
		return
	}
	t.sb.commitLive(t.term)
	if cols < 20 {
		cols = 80
	}
	t.sb.pushBlock(display, cols, t.cwd)
	if next, ok := cwdAfterCommand(t.cwd, payload); ok {
		// Don't mark idle via setCwd for speculative cd — use direct assign.
		if next != "" && next != t.cwd {
			t.cwd = next
		}
	}
	t.echo.arm(payload)
	if isClearCommand(payload) {
		t.sb.pinHere()
	}
}

// sendBarPayload writes the command + CR to the PTY and marks in-flight.
func sendBarPayload(t *tab, payload string) {
	if t == nil {
		return
	}
	if strings.Contains(payload, "\n") {
		payload = strings.ReplaceAll(payload, "\n", "\r")
	}
	t.sendKey([]byte(payload + "\r"))
	t.markBarCommandSent()
	t.sb.stickBottom()
}

// submitBarLine is the shared Warp-bar Enter path (macOS + Windows).
// toast may be nil. Returns a short status toast when a line was queued.
func submitBarLine(t *tab, line string, cols int, toast func(string)) {
	if t == nil {
		return
	}
	display, payload := prepareBarSubmit(line, t.shell)

	if strings.TrimSpace(display) == "" && strings.TrimSpace(payload) == "" {
		t.sendKey([]byte{'\r'})
		return
	}

	if t.barShouldQueue() {
		n := t.enqueueBarCmd(display, payload)
		if toast != nil {
			toast(fmt.Sprintf("queued · %d", n))
		}
		return
	}

	applyBarSubmitToTab(t, display, payload, cols)
	sendBarPayload(t, payload)
}

// tryFlushCmdQueue sends the next queued bar command if the shell is idle.
func tryFlushCmdQueue(t *tab, cols int, toast func(string)) bool {
	if t == nil {
		return false
	}
	t.maybeReleaseBarAwaiting()
	cmd, ok := t.popCmdQueue()
	if !ok {
		return false
	}
	applyBarSubmitToTab(t, cmd.display, cmd.payload, cols)
	sendBarPayload(t, cmd.payload)
	if toast != nil {
		left := t.queueLen()
		if left > 0 {
			toast(fmt.Sprintf("ran queued · %d left", left))
		} else {
			toast("ran queued")
		}
	}
	return true
}
