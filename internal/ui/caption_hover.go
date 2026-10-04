package ui

// captionShouldArmLeave is true only for a client-area pointer that we are
// not already tracking. WM_MOUSELEAVE must pass a negative point and must
// never re-arm: TrackMouseEvent(TME_LEAVE) while the cursor is already
// outside the client (or over HTCAPTION on the custom frame) makes Windows
// post another WM_MOUSELEAVE immediately, which starves paint and input.
func captionShouldArmLeave(alreadyTracking bool, clientX, clientY int32) bool {
	if alreadyTracking {
		return false
	}
	return clientX >= 0 && clientY >= 0
}

// captionApplyLeave is the WM_MOUSELEAVE transition: clear hover and the
// tracking flag, and never request a new TrackMouseEvent.
func captionApplyLeave(hot int, tracking bool) (nextHot int, nextTracking bool, arm bool, dirty bool) {
	return -1, false, false, hot >= 0 || tracking
}

// captionShouldArmNCLeave is the non-client counterpart. WM_NCMOUSELEAVE
// must never re-arm TME_NONCLIENT|TME_LEAVE (same tight loop as 0x2a3).
func captionShouldArmNCLeave(alreadyTracking bool) bool {
	return !alreadyTracking
}
