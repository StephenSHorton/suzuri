//go:build windows || darwin

package ui

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

var (
	uiBusySince        atomic.Int64
	uiBusyPhase        atomic.Value // string
	uiWatchDepth       atomic.Int32
	uiFrameTiming      atomic.Bool
	uiStallArmed       atomic.Bool
	uiFrameStarveArmed atomic.Bool
	uiExpectFrames     atomic.Bool
	uiLastFrame        atomic.Int64
	uiWatchStart       atomic.Int64
	uiPaintLogN        atomic.Int32
	uiWatchOnce        sync.Once
)

func uiWatchEnter(phase string) {
	if phase == "" {
		phase = "ui"
	}
	uiBusyPhase.Store(phase)
	if uiWatchDepth.Add(1) == 1 {
		uiBusySince.Store(time.Now().UnixNano())
	}
}

func uiWatchLeave() {
	if uiWatchDepth.Add(-1) <= 0 {
		uiWatchDepth.Store(0)
		uiBusySince.Store(0)
	}
}

func uiWatchPhase() string {
	if v, ok := uiBusyPhase.Load().(string); ok {
		return v
	}
	return ""
}

func uiWatchExpectFrames(on bool) {
	uiExpectFrames.Store(on)
}

func noteUIFrame() {
	uiLastFrame.Store(time.Now().UnixNano())
	uiFrameStarveArmed.Store(false)
}

func toggleFrameTiming() bool {
	next := !uiFrameTiming.Load()
	uiFrameTiming.Store(next)
	return next
}

func frameTimingOn() bool {
	return uiFrameTiming.Load()
}

func startUIWatchdog() {
	if watchdogDisabled() {
		log.Info("ui watchdog disabled", "env", envWatchdogDisable)
		return
	}
	uiWatchOnce.Do(func() {
		if frameTimingRequested() {
			uiFrameTiming.Store(true)
			log.Info("frame timing on", "env", envFrameTiming)
		}
		uiWatchStart.Store(time.Now().UnixNano())
		go uiWatchdogLoop()
		log.Info("ui watchdog started", "stall_after", uiStallAfter)
	})
}

func uiWatchdogLoop() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		since := uiBusySince.Load()
		if since == 0 {
			uiStallArmed.Store(false)
		} else {
			stuck := time.Since(time.Unix(0, since))
			if stuck >= uiStallAfter && uiStallArmed.CompareAndSwap(false, true) {
				dumpUIStall("ui thread stalled", uiWatchPhase(), stuck)
			}
		}
		if !uiExpectFrames.Load() {
			uiFrameStarveArmed.Store(false)
			continue
		}
		last := uiLastFrame.Load()
		if last == 0 {
			last = uiWatchStart.Load()
		}
		if last == 0 {
			continue
		}
		idle := time.Since(time.Unix(0, last))
		if idle >= uiStallAfter && uiFrameStarveArmed.CompareAndSwap(false, true) {
			phase := uiWatchPhase()
			if phase == "" {
				phase = "no completed frame"
			}
			dumpUIStall("ui frame starved", phase, idle)
		}
	}
}

func dumpUIStall(kind, phase string, stuck time.Duration) {
	log.Error(kind,
		"phase", phase,
		"stuck_for", stuck.Round(time.Millisecond).String(),
	)
	applog.Trail("ui stall", "kind", kind, "phase", phase, "stuck", stuck.Round(time.Millisecond).String())
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	log.Error("ui stall goroutines", "stacks", string(buf[:n]))
	applog.Sync()
}
