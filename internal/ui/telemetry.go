package ui

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// UI-thread telemetry for the hang watchdog and the periodic telemetry line
// (Windows handoff §3.2/§3.4). The UI thread records; the watchdog and
// telemetry goroutines read. Everything here is safe to call from any
// goroutine. Recording is a few atomics plus one short mutex per sample, so
// it stays on in release builds.

// telemetryInterval is how often the telemetry line is logged.
const telemetryInterval = 10 * time.Second

// maxInputLatency drops input-latency samples above it. An input that paints
// nothing (a modifier, a key the shell swallows) waits for the next unrelated
// paint, which would read as a huge "latency". Anything genuinely slower than
// this is a stall, and the watchdog reports those.
const maxInputLatency = 250 * time.Millisecond

// maxTelemetrySamples bounds each sample window (frames, input latency) so a
// busy window can't grow memory between two telemetry lines.
const maxTelemetrySamples = 4096

type uiTelemetry struct {
	framesPainted atomic.Int64
	wakes         atomic.Int64

	// phase is what the UI thread is doing right now ("idle" while it waits
	// for messages); phaseSince is when it entered it (unix nanos).
	phase      atomic.Value // string
	phaseSince atomic.Int64

	// inputSince is the time of the oldest input event not yet followed by
	// a finished paint (unix nanos; 0 = none pending).
	inputSince atomic.Int64

	mu         sync.Mutex
	frameTimes []time.Duration
	inputLat   []time.Duration
}

func newUITelemetry() *uiTelemetry {
	t := &uiTelemetry{}
	t.enterPhase("idle")
	return t
}

// phaseMark is a saved phase, restored by leavePhase.
type phaseMark struct {
	name  string
	since int64
}

// enterPhase records the UI thread's current phase and returns the previous
// one, so nested dispatch (modal size/move loops, SendMessage re-entry)
// restores it with its original start time.
func (t *uiTelemetry) enterPhase(p string) phaseMark {
	if t == nil {
		return phaseMark{}
	}
	prev := phaseMark{since: t.phaseSince.Load()}
	prev.name, _ = t.phase.Load().(string)
	t.phase.Store(p)
	t.phaseSince.Store(time.Now().UnixNano())
	return prev
}

// leavePhase restores a phase saved by enterPhase.
func (t *uiTelemetry) leavePhase(m phaseMark) {
	if t == nil {
		return
	}
	t.phase.Store(m.name)
	if m.name == "idle" {
		// Back to waiting for messages: idle time starts now, not when the
		// UI thread first went idle at startup.
		t.phaseSince.Store(time.Now().UnixNano())
		return
	}
	t.phaseSince.Store(m.since)
}

// currentPhase returns the phase and how long the UI thread has been in it.
func (t *uiTelemetry) currentPhase(now time.Time) (string, time.Duration) {
	if t == nil {
		return "", 0
	}
	p, _ := t.phase.Load().(string)
	since := t.phaseSince.Load()
	if since == 0 {
		return p, 0
	}
	return p, now.Sub(time.Unix(0, since))
}

func (t *uiTelemetry) noteWake() {
	if t != nil {
		t.wakes.Add(1)
	}
}

// noteInput marks an input event that happened at `at`. Only the oldest
// pending one counts, so latency is "first unpainted input to frame done".
func (t *uiTelemetry) noteInput(at time.Time) {
	if t == nil {
		return
	}
	t.inputSince.CompareAndSwap(0, at.UnixNano())
}

// noteFrame records one finished paint that took d and ended at end.
func (t *uiTelemetry) noteFrame(d time.Duration, end time.Time) {
	if t == nil {
		return
	}
	t.framesPainted.Add(1)
	var lat time.Duration
	hasLat := false
	if since := t.inputSince.Swap(0); since != 0 {
		lat = end.Sub(time.Unix(0, since))
		hasLat = lat >= 0 && lat <= maxInputLatency
	}
	t.mu.Lock()
	if len(t.frameTimes) < maxTelemetrySamples {
		t.frameTimes = append(t.frameTimes, d)
	}
	if hasLat && len(t.inputLat) < maxTelemetrySamples {
		t.inputLat = append(t.inputLat, lat)
	}
	t.mu.Unlock()
}

// telemetryWindow is one interval's numbers.
type telemetryWindow struct {
	frames     int64
	wakes      int64
	frameP50   time.Duration
	frameP99   time.Duration
	inputP99   time.Duration
	inputN     int
	panes      []paneTelemetry
	queued     int64
	gdi, user  int
	goroutines int
}

// takeWindow returns the counters since the last call and resets them.
func (t *uiTelemetry) takeWindow() telemetryWindow {
	if t == nil {
		return telemetryWindow{}
	}
	w := telemetryWindow{
		frames: t.framesPainted.Swap(0),
		wakes:  t.wakes.Swap(0),
	}
	t.mu.Lock()
	ft, il := t.frameTimes, t.inputLat
	t.frameTimes, t.inputLat = nil, nil
	t.mu.Unlock()
	w.frameP50 = percentile(ft, 50)
	w.frameP99 = percentile(ft, 99)
	w.inputP99 = percentile(il, 99)
	w.inputN = len(il)
	return w
}

// percentile returns the nearest-rank p-th percentile (0 for no samples).
// It sorts s in place.
func percentile(s []time.Duration, p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(p/100*float64(len(s))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

// --- per-pane counters

// paneTelemetry is one pane's bytes for a window.
type paneTelemetry struct {
	id       int
	ingested int64 // bytes fed to the VT parser this window
	queued   int64 // bytes read from the PTY, not yet drained (now)
}

// paneCounters live on each tab; the PTY reader and the UI thread update them.
type paneCounters struct {
	ingested atomic.Int64 // total bytes drained into the VT parser
	queued   atomic.Int64 // bytes waiting in inBuf
	lastSeen int64        // ingested at the previous telemetry line (registry lock)
}

// paneRegistry lists live tabs for the watchdog and telemetry goroutines,
// which must not touch the UI thread's tab slices.
var paneRegistry = struct {
	sync.Mutex
	m map[int]*paneCounters
}{m: map[int]*paneCounters{}}

func registerPaneTelemetry(id int, c *paneCounters) {
	if c == nil {
		return
	}
	paneRegistry.Lock()
	paneRegistry.m[id] = c
	paneRegistry.Unlock()
}

func unregisterPaneTelemetry(id int) {
	paneRegistry.Lock()
	delete(paneRegistry.m, id)
	paneRegistry.Unlock()
}

// paneTelemetrySnapshot returns each pane's bytes since the previous call
// (advance=true) or without moving the window (advance=false, used by the
// stall dump), sorted by pane id, plus the total queued bytes.
func paneTelemetrySnapshot(advance bool) ([]paneTelemetry, int64) {
	paneRegistry.Lock()
	defer paneRegistry.Unlock()
	out := make([]paneTelemetry, 0, len(paneRegistry.m))
	var queued int64
	for id, c := range paneRegistry.m {
		total := c.ingested.Load()
		q := c.queued.Load()
		out = append(out, paneTelemetry{id: id, ingested: total - c.lastSeen, queued: q})
		if advance {
			c.lastSeen = total
		}
		queued += q
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, queued
}

// formatPanes renders "id:ingested/queued" per pane, e.g. "0:51234/0 3:12/4096".
func formatPanes(ps []paneTelemetry) string {
	if len(ps) == 0 {
		return "-"
	}
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%d/%d", p.id, p.ingested, p.queued)
	}
	return b.String()
}

// allGoroutineStacks returns runtime.Stack for every goroutine, growing the
// buffer until it fits (capped at 16 MiB). Tabs become spaces: the log's text
// formatter would print them as a literal \t.
func allGoroutineStacks() string {
	n := 1 << 16
	for {
		buf := make([]byte, n)
		m := runtime.Stack(buf, true)
		if m < len(buf) || n >= 16<<20 {
			return strings.ReplaceAll(string(buf[:m]), "\t", "    ")
		}
		n *= 2
	}
}

// msDur rounds a duration to 0.1 ms for log lines.
func msDur(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}
