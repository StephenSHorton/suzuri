package ui

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/hinshun/vt10x"
)

func TestBarShouldQueueWhileAwaiting(t *testing.T) {
	tab := &tab{alive: atomicBool(true)}
	if tab.barShouldQueue() {
		t.Fatal("idle tab should not queue")
	}
	tab.markBarCommandSent()
	if !tab.barShouldQueue() {
		t.Fatal("expected queue after bar send")
	}
	if !tab.programOwnsKeys() {
		t.Fatal("running command should own the keyboard")
	}
	// A prompt waiting for input is quiet. That must not hand the bar back.
	tab.lastIOUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	tab.maybeReleaseBarAwaiting()
	if !tab.barAwaiting || !tab.programOwnsKeys() {
		t.Fatal("quiet wait should keep the command on the keyboard")
	}
	tab.markShellIdle()
	tab.lastIOUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	if tab.barAwaiting || tab.programOwnsKeys() {
		t.Fatal("prompt return should restore the bar")
	}
	if tab.barShouldQueue() {
		t.Fatal("idle tab should not queue")
	}
}

func TestCmdQueueFIFO(t *testing.T) {
	tab := &tab{alive: atomicBool(true)}
	tab.markBarCommandSent()
	tab.enqueueBarCmd("a", "a")
	tab.enqueueBarCmd("b", "b")
	if tab.queueLen() != 2 {
		t.Fatalf("len=%d", tab.queueLen())
	}
	// Still awaiting — cannot pop.
	if _, ok := tab.popCmdQueue(); ok {
		t.Fatal("should not pop while awaiting")
	}
	tab.markShellIdle()
	tab.lastIOUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	cmd, ok := tab.popCmdQueue()
	if !ok || cmd.display != "a" {
		t.Fatalf("first=%v ok=%v", cmd, ok)
	}
	// After pop we haven't sent yet — can pop second while idle.
	cmd, ok = tab.popCmdQueue()
	if !ok || cmd.display != "b" {
		t.Fatalf("second=%v ok=%v", cmd, ok)
	}
}

func TestCwdRedrawKeepsKeyboard(t *testing.T) {
	zshTab := &tab{alive: atomicBool(true), shell: "/bin/zsh", cwd: "/tmp"}
	zshTab.markBarCommandSent()
	zshTab.setCwd("/tmp")
	if !zshTab.programOwnsKeys() {
		t.Fatal("cwd redraw took the keyboard back")
	}
	cmdTab := &tab{alive: atomicBool(true), shell: `C:\Windows\System32\cmd.exe`, cwd: `C:\`}
	cmdTab.markBarCommandSent()
	cmdTab.setCwd(`C:\`)
	if cmdTab.programOwnsKeys() {
		t.Fatal("cmd.exe cwd OSC should return the keyboard")
	}
}

func TestFinishOSCReturnsKeyboard(t *testing.T) {
	tab := &tab{alive: atomicBool(true), shell: "/bin/zsh"}
	tab.term = vt10x.New(vt10x.WithSize(20, 5))
	tab.markBarCommandSent()
	tab.pullPTY([]byte("\x1b]7879;done;0;bHM=\x07"), false, true)
	if tab.programOwnsKeys() {
		t.Fatal("finish OSC left the command on the keyboard")
	}
}

func TestCommandStartHoldsKeyboardUntilDone(t *testing.T) {
	tab := &tab{alive: atomicBool(true), shell: "/bin/zsh", cwd: "/tmp"}
	tab.term = vt10x.New(vt10x.WithSize(40, 8))
	tab.markBarCommandSent()
	// Paste off then on is a zle redraw, not the prompt returning.
	tab.pullPTY([]byte("\x1b[?2004l\x1b[?2004h"), false, true)
	tab.setCwd("/tmp")
	if !tab.programOwnsKeys() {
		t.Fatal("redraw took the keyboard")
	}
	// Start then done, and nothing else holds the terminal: the command
	// finished in this chunk (ls). The bar comes back.
	tab.pullPTY([]byte("\x1b]7879;start\x07\x1b]7879;done;0;bHM=\x07"), false, true)
	if tab.foreground || tab.programOwnsKeys() {
		t.Fatal("finished command kept the keyboard")
	}
	// The same bytes while a child still has the tty stay with the program.
	tab.markBarCommandSent()
	tab.forceChild = true
	tab.pullPTY([]byte("\x1b]7879;start\x07\x1b]7879;done;0;bHM=\x07"), false, true)
	if !tab.programOwnsKeys() {
		t.Fatal("child process group handed the bar back")
	}
	tab.forceChild = false
	if tab.programOwnsKeys() {
		t.Fatal("shell process group kept the keyboard")
	}
	tab.markBarCommandSent()
	// A finish for the previous line and a new start in one chunk stays with the program.
	tab.pullPTY([]byte("\x1b]7879;done;0;bHM=\x07\x1b]7879;start\x07"), false, true)
	if !tab.programOwnsKeys() {
		t.Fatal("start after done handed the bar back")
	}
	tab.pullPTY([]byte("\x1b]7879;done;0;bHM=\x07\x1b[?2004h"), false, true)
	if tab.foreground || tab.barAwaiting || tab.programOwnsKeys() {
		t.Fatal("done should return the bar")
	}
}

func TestDiscardCmdQueueKeepsKeyboard(t *testing.T) {
	tab := &tab{alive: atomicBool(true)}
	tab.markBarCommandSent()
	tab.enqueueBarCmd("next", "next")
	n := tab.discardCmdQueue()
	if n != 1 || tab.queueLen() != 0 || !tab.programOwnsKeys() {
		t.Fatalf("n=%d len=%d owns=%v", n, tab.queueLen(), tab.programOwnsKeys())
	}
}

func TestClearCmdQueue(t *testing.T) {
	tab := &tab{alive: atomicBool(true)}
	tab.enqueueBarCmd("x", "x")
	tab.barAwaiting = true
	n := tab.clearCmdQueue()
	if n != 1 || tab.queueLen() != 0 || tab.barAwaiting {
		t.Fatalf("n=%d len=%d awaiting=%v", n, tab.queueLen(), tab.barAwaiting)
	}
}

func atomicBool(v bool) (a atomic.Bool) {
	a.Store(v)
	return a
}
