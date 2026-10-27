//go:build darwin

package ui

// leftHeld reports whether the left button is still down at the end of this
// tick, which is what keeps a selection, sash, notes, pane or TUI drag alive.
//
// ebiten.IsMouseButtonPressed is not that: it is true on any tick with a fresh
// press, even when the release landed in the same tick. GLFW callbacks run
// once per frame, so on a slow frame a whole click is polled together and
// handleMouse sees justPressed, justReleased and pressed all at once.
// pressDur (inpututil.MouseButtonPressDuration) is 0 once a release follows
// the last press, and stays >0 for a release-then-press double click.
func leftHeld(pressDur int) bool {
	return pressDur > 0
}

// releaseLeftGestures ends everything a held left button keeps alive. It runs
// on every tick the button is up, not only on the release edge: the press
// branch of handleMouse returns before the release edge is read, so a click
// whose press and release share a tick has no other chance to end.
func (u *macUI) releaseLeftGestures(mx, my int) {
	u.frameDrag.on = false
	if u.paneDrag.engaged() {
		wasActive := u.paneDrag.active
		if id, target, edge, ok := u.paneDrag.release(); ok {
			hostDropPane(u, id, target, edge)
		} else if wasActive {
			u.markShellDirty() // clear the highlight
		}
	}
	if u.sashDrag != nil {
		u.sashDrag = nil
		u.applyClientSize(u.width, u.height)
	}
	if u.notesDragging {
		u.notesDragging = false
		u.persistNotesIfDirty()
	}
	// Alt-screen mouse release (SGR …m). Without it the TUI thinks the
	// button is still down and every move reports a drag.
	if u.altMouseDown {
		u.altMouseDown = false
		if t := u.activeTab(); t != nil && t.altScreen() {
			u.sendAltMouse(t, mx, my, false)
		}
	}
	u.selecting = false
	u.mouseDown = false
}
