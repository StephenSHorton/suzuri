//go:build windows || darwin

package ui

import (
	"os"
	"strings"
	"time"
)

const (
	uiStallAfter       = 2 * time.Second
	minAmbientPaintGap = 40 * time.Millisecond // blink clock (~25 fps)
	slowPaintWarn      = 150 * time.Millisecond
	slowPaintKill      = 400 * time.Millisecond
	slowPaintKillCount = 3
	// unfocusedPaintGap caps presents while alt-tabbed. A grok-fork build
	// flood used to InvalidateRect on every 256KiB ingest; those full GDI
	// paints (rain + alt-screen + glass) jumped to 150–376ms and then the
	// host vanished with no trail.
	unfocusedPaintGap  = 100 * time.Millisecond
	envSafeMode        = "SUZURI_SAFE_MODE"
	envNoAmbient       = "SUZURI_NO_AMBIENT"
	envFrameTiming     = "SUZURI_FRAME_TIMING"
	envWatchdogDisable = "SUZURI_WATCHDOG"
)

func envFlagOn(keys ...string) bool {
	for _, k := range keys {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(k)))
		switch v {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func envFlagOff(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func safeModeRequested() bool {
	return envFlagOn(envSafeMode, envNoAmbient)
}

func frameTimingRequested() bool {
	return envFlagOn(envFrameTiming)
}

func watchdogDisabled() bool {
	return envFlagOff(envWatchdogDisable)
}

// shouldDisableAmbient is true only for genuinely slow presents. Healthy
// GDI frames on Stephen's PC were 0–15ms; do not trip on a layout hitch.
func shouldDisableAmbient(elapsed time.Duration, slowCount int) bool {
	if elapsed >= slowPaintKill {
		return true
	}
	return elapsed >= slowPaintWarn && slowCount >= slowPaintKillCount
}

func skipAmbientFrame(sinceLast time.Duration) bool {
	return sinceLast > 0 && sinceLast < minAmbientPaintGap
}

// shouldInvalidateFromPTY is whether a PTY ingest may InvalidateRect.
// Ingest always runs (so the child does not block on a full pipe). Paint
// does not: an unfocused flood plus 60fps rain was the de25fe7 silent death.
func shouldInvalidateFromPTY(focused, visible, paintPending bool, sinceLast time.Duration) bool {
	if !visible || paintPending {
		return false
	}
	if focused {
		return true
	}
	return sinceLast <= 0 || sinceLast >= unfocusedPaintGap
}

// shouldAmbientWhileUnfocused is rain/settings underlay while alt-tabbed.
// Drop it during a PTY flood or after a slow present — ingest still runs.
func shouldAmbientWhileUnfocused(animateUnfocused, ptyHot, lastPaintSlow bool) bool {
	if !animateUnfocused {
		return false
	}
	if ptyHot || lastPaintSlow {
		return false
	}
	return true
}

// sessionSuppressAmbient is the paint-stall rain guard. keepSaved is the
// caller's ShellAmbient unchanged — persistWindowPlacement writes u.cfg.
func sessionSuppressAmbient(already bool, savedAmbient string) (suppress bool, keepSaved, toast string) {
	if already {
		return true, savedAmbient, ""
	}
	return true, savedAmbient, sessionAmbientPausedToast()
}

func sessionAmbientPausedToast() string {
	return "rain paused for this session"
}

// ambientFramePeriod is the rain/settings underlay tick. Cap at 60 fps so a
// 239 Hz desktop cannot flood GDI; floor at 30 fps so a bogus 0 Hz DC
// still animates.
func ambientFramePeriod(hz int32) time.Duration {
	if hz < 30 {
		hz = 60
	}
	if hz > 60 {
		hz = 60
	}
	return time.Second / time.Duration(hz)
}
