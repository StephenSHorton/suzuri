//go:build darwin

package ui

import "testing"

func TestLeftHeld(t *testing.T) {
	cases := []struct {
		name string
		// pressed is what ebiten.IsMouseButtonPressed reports for the tick;
		// handleMouse used to keep drags alive on it.
		pressed  bool
		pressDur int
		want     bool
	}{
		// Slow frame or trackpad tap: press and release polled in one tick.
		// ebiten still says pressed (just pressed), but the button is up.
		{"whole click in one tick", true, 0, false},
		{"held", true, 3, true},
		{"press tick of a real drag", true, 1, true},
		// Fast double click: release then press in one tick; still down.
		{"release then press in one tick", true, 1, true},
		{"up", false, 0, false},
	}
	for _, c := range cases {
		if got := leftHeld(c.pressDur); got != c.want {
			t.Errorf("%s: leftHeld(%d) = %v, want %v (ebiten pressed=%v)",
				c.name, c.pressDur, got, c.want, c.pressed)
		}
	}
}

// A click whose press and release share a tick starts a gesture in the press
// branch, which returns before any release edge is read. The deferred release
// must end every gesture, or the next mouse move drags (TUI SGR drag reports,
// selection, sash).
func TestReleaseLeftGesturesEndsEverything(t *testing.T) {
	u := &macUI{}
	u.selecting = true
	u.mouseDown = true
	u.notesDragging = true
	u.altMouseDown = true
	u.sashDrag = &sashGeom{}
	u.frameDrag.on = true
	u.paneDrag.arm(7, 10, 10)

	u.releaseLeftGestures(0, 0)

	if u.selecting || u.mouseDown {
		t.Errorf("selection still live: selecting=%v mouseDown=%v", u.selecting, u.mouseDown)
	}
	if u.notesDragging {
		t.Error("notes drag still live")
	}
	if u.altMouseDown {
		t.Error("alt-screen button still down: TUI would get drag reports on every move")
	}
	if u.sashDrag != nil {
		t.Error("sash drag still live")
	}
	if u.frameDrag.on {
		t.Error("frame drag still live")
	}
	if u.paneDrag.engaged() {
		t.Error("pane drag still armed")
	}
}
