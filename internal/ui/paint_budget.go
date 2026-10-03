//go:build windows || darwin

package ui

import (
	"os"
	"strings"
	"time"
)

const (
	uiStallAfter       = 2 * time.Second
	minAmbientPaintGap = 70 * time.Millisecond
	slowPaintWarn      = 80 * time.Millisecond
	slowPaintKill      = 200 * time.Millisecond
	slowPaintKillCount = 2
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

// shouldDisableAmbient is true when GDI/present is so slow that rain would
// pin the UI thread (dual-GPU / high-refresh hangs look like this).
func shouldDisableAmbient(elapsed time.Duration, slowCount int) bool {
	if elapsed >= slowPaintKill {
		return true
	}
	return elapsed >= slowPaintWarn && slowCount >= slowPaintKillCount
}

func skipAmbientFrame(sinceLast time.Duration) bool {
	return sinceLast > 0 && sinceLast < minAmbientPaintGap
}
