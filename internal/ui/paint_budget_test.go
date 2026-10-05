//go:build windows || darwin

package ui

import (
	"testing"
	"time"
)

func TestShouldDisableAmbient(t *testing.T) {
	if shouldDisableAmbient(50*time.Millisecond, 10) {
		t.Fatal("fast paints must keep rain")
	}
	if shouldDisableAmbient(90*time.Millisecond, 4) {
		t.Fatal("sub-warn paints must keep rain")
	}
	if shouldDisableAmbient(160*time.Millisecond, 2) {
		t.Fatal("two 160ms paints are a warning, not a kill")
	}
	if !shouldDisableAmbient(160*time.Millisecond, 3) {
		t.Fatal("three slow paints should disable rain")
	}
	if !shouldDisableAmbient(400*time.Millisecond, 1) {
		t.Fatal("a 400ms paint should disable rain immediately")
	}
}

func TestSkipAmbientFrame(t *testing.T) {
	if skipAmbientFrame(0) {
		t.Fatal("first paint must run")
	}
	if !skipAmbientFrame(20 * time.Millisecond) {
		t.Fatal("sub-gap rain frames must skip")
	}
	if skipAmbientFrame(minAmbientPaintGap) {
		t.Fatal("gap elapsed must paint")
	}
}

func TestUnfocusedPTYFloodDoesNotPaintEveryChunk(t *testing.T) {
	// Focused: every visible ingest may invalidate (paintPending still coalesces).
	if !shouldInvalidateFromPTY(true, true, false, time.Millisecond) {
		t.Fatal("focused ingest should paint")
	}
	if shouldInvalidateFromPTY(true, true, true, time.Millisecond) {
		t.Fatal("paintPending already coalesces")
	}
	if shouldInvalidateFromPTY(true, false, false, time.Millisecond) {
		t.Fatal("hidden pane must not invalidate")
	}
	// Unfocused grok-fork flood: first present ok, then 10fps cap.
	if !shouldInvalidateFromPTY(false, true, false, 0) {
		t.Fatal("first unfocused present must run")
	}
	if shouldInvalidateFromPTY(false, true, false, 20*time.Millisecond) {
		t.Fatal("20ms after a present must not start another unfocused full paint")
	}
	if !shouldInvalidateFromPTY(false, true, false, unfocusedPaintGap) {
		t.Fatal("after the gap, one present is allowed")
	}
	paints := 0
	since := time.Duration(0)
	for i := 0; i < 40; i++ {
		if shouldInvalidateFromPTY(false, true, false, since) {
			paints++
			since = 0
		} else {
			since += 17 * time.Millisecond // 60fps frame + PTY chunk
		}
	}
	if paints > 8 {
		t.Fatalf("unfocused flood painted %d times in ~680ms, want ≤8 (10fps)", paints)
	}
	if paints < 1 {
		t.Fatal("unfocused flood must still present occasionally")
	}
}

func TestAmbientDropsDuringUnfocusedFlood(t *testing.T) {
	if shouldAmbientWhileUnfocused(false, false, false) {
		t.Fatal("AnimateUnfocused off")
	}
	if !shouldAmbientWhileUnfocused(true, false, false) {
		t.Fatal("idle unfocused rain stays on")
	}
	if shouldAmbientWhileUnfocused(true, true, false) {
		t.Fatal("PTY flood must drop rain so ingest can drain")
	}
	if shouldAmbientWhileUnfocused(true, false, true) {
		t.Fatal("slow present must drop rain")
	}
}

func TestAmbientFramePeriodCapsRefresh(t *testing.T) {
	if ambientFramePeriod(0) != time.Second/60 {
		t.Fatal("0 Hz must fall back to 60 fps")
	}
	if ambientFramePeriod(30) != time.Second/30 {
		t.Fatal("30 Hz should pace to the display")
	}
	if ambientFramePeriod(60) != time.Second/60 {
		t.Fatal("60 Hz")
	}
	if ambientFramePeriod(239) != time.Second/60 {
		t.Fatal("239 Hz must not chase the panel")
	}
}

func TestEnvFlagOn(t *testing.T) {
	t.Setenv("SUZURI_SAFE_MODE", "1")
	if !safeModeRequested() {
		t.Fatal("SUZURI_SAFE_MODE=1")
	}
	t.Setenv("SUZURI_SAFE_MODE", "")
	t.Setenv("SUZURI_NO_AMBIENT", "true")
	if !safeModeRequested() {
		t.Fatal("SUZURI_NO_AMBIENT=true")
	}
	t.Setenv("SUZURI_NO_AMBIENT", "")
	if safeModeRequested() {
		t.Fatal("empty env is not safe mode")
	}
}

func resetUIWatchForTest() {
	uiBusySince.Store(0)
	uiWatchDepth.Store(0)
	uiBusyPhase.Store("")
	uiStallArmed.Store(false)
	uiFrameStarveArmed.Store(false)
	uiLastFrame.Store(0)
	uiExpectFrames.Store(false)
}

func TestWatchEnterLeaveClearsBusy(t *testing.T) {
	resetUIWatchForTest()
	uiWatchEnter("WM_PAINT")
	if uiBusySince.Load() == 0 || uiWatchPhase() != "WM_PAINT" {
		t.Fatal("enter should mark the UI thread busy")
	}
	uiWatchLeave()
	if uiBusySince.Load() != 0 {
		t.Fatal("leave should clear busy")
	}
}

func TestWatchNestedLeaveKeepsOuterBusy(t *testing.T) {
	resetUIWatchForTest()
	uiWatchEnter("outer")
	started := uiBusySince.Load()
	uiWatchEnter("inner")
	uiWatchLeave()
	if uiBusySince.Load() == 0 {
		t.Fatal("inner leave must not clear an outer enter")
	}
	if uiBusySince.Load() != started {
		t.Fatal("outer busy timestamp should stay put across nested enter")
	}
	uiWatchLeave()
	if uiBusySince.Load() != 0 {
		t.Fatal("final leave should clear busy")
	}
}

func TestNoteUIFrameClearsStarveArm(t *testing.T) {
	resetUIWatchForTest()
	uiFrameStarveArmed.Store(true)
	noteUIFrame()
	if uiLastFrame.Load() == 0 {
		t.Fatal("noteUIFrame should stamp a frame")
	}
	if uiFrameStarveArmed.Load() {
		t.Fatal("a completed frame should re-arm starve detection")
	}
}

func TestDumpUIStallDoesNotPanic(t *testing.T) {
	dumpUIStall("ui thread stalled", "test", 2*time.Second)
}
