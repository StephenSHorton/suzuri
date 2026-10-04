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
