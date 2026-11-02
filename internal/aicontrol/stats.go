package aicontrol

import (
	"os"
	"runtime"
	"time"
)

var processStart = time.Now()

// ProcessStats is the GET /v1/stats body: counters only this process can see
// (goroutines, Go heap) plus OS handle counts where the platform has them.
// It never touches the UI thread. The hang watchdog / telemetry work can add
// its own counters here.
func ProcessStats() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out := map[string]any{
		"pid":            os.Getpid(),
		"uptime_ms":      time.Since(processStart).Milliseconds(),
		"goroutines":     runtime.NumGoroutine(),
		"heap_alloc":     ms.HeapAlloc,
		"heap_sys":       ms.HeapSys,
		"go_sys":         ms.Sys,
		"num_gc":         ms.NumGC,
		"gc_pause_total": ms.PauseTotalNs,
	}
	addOSStats(out)
	return out
}
