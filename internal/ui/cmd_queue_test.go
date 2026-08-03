package ui

import (
	"sync/atomic"
	"testing"
	"time"
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
	// Simulate quiet past idle window.
	tab.lastIOUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	tab.maybeReleaseBarAwaiting()
	if tab.barAwaiting {
		t.Fatal("expected barAwaiting cleared after quiet")
	}
	if tab.barShouldQueue() {
		t.Fatal("quiet tab should not queue")
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
	tab.lastIOUnixNano.Store(time.Now().Add(-2 * time.Second).UnixNano())
	tab.maybeReleaseBarAwaiting()
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
