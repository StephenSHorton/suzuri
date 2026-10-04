package ui

import "testing"

func TestCaptionLeaveDoesNotRearmTrackMouse(t *testing.T) {
	// The 0.9.160 / PR #86 freeze: WM_MOUSELEAVE cleared the flag then
	// called trackCaptionHover(hwnd, -1, -1), which re-armed TME_LEAVE.
	if captionShouldArmLeave(false, -1, -1) {
		t.Fatal("leave coordinates must not arm TrackMouseEvent")
	}
	if captionShouldArmLeave(false, 0, -1) {
		t.Fatal("partially invalid leave point must not arm")
	}
	if captionShouldArmLeave(true, 40, 8) {
		t.Fatal("already tracking must not arm again")
	}
	if !captionShouldArmLeave(false, 40, 8) {
		t.Fatal("first in-client move should arm TME_LEAVE")
	}
}

func TestCaptionNCLeaveDoesNotRearm(t *testing.T) {
	if captionShouldArmNCLeave(true) {
		t.Fatal("already tracking must not arm TME_NONCLIENT")
	}
	if !captionShouldArmNCLeave(false) {
		t.Fatal("first NC move should arm")
	}
	_, _, arm, _ := captionApplyLeave(1, true)
	if arm {
		t.Fatal("WM_NCMOUSELEAVE must use the same no-arm leave transition")
	}
}

func TestCaptionApplyLeaveNeverArms(t *testing.T) {
	hot, trk, arm, dirty := captionApplyLeave(2, true)
	if hot != -1 || trk || arm {
		t.Fatalf("leave: hot=%d tracking=%v arm=%v", hot, trk, arm)
	}
	if !dirty {
		t.Fatal("clearing a hot caption button should dirty the strip")
	}
	hot, trk, arm, dirty = captionApplyLeave(-1, false)
	if hot != -1 || trk || arm || dirty {
		t.Fatalf("idle leave: hot=%d tracking=%v arm=%v dirty=%v", hot, trk, arm, dirty)
	}
}
