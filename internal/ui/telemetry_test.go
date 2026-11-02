package ui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	ms := func(v ...int) []time.Duration {
		out := make([]time.Duration, len(v))
		for i, x := range v {
			out[i] = time.Duration(x) * time.Millisecond
		}
		return out
	}
	if got := percentile(nil, 99); got != 0 {
		t.Fatalf("empty: got %v", got)
	}
	if got := percentile(ms(7), 50); got != 7*time.Millisecond {
		t.Fatalf("one sample p50: got %v", got)
	}
	// 1..100 ms shuffled: p50 = 50, p99 = 99, p100 = 100.
	s := make([]int, 100)
	for i := range s {
		s[i] = (i*37)%100 + 1
	}
	for _, c := range []struct {
		p    float64
		want int
	}{{50, 50}, {99, 99}, {100, 100}, {1, 1}} {
		if got := percentile(ms(s...), c.p); got != time.Duration(c.want)*time.Millisecond {
			t.Errorf("p%v: got %v want %dms", c.p, got, c.want)
		}
	}
	// A single slow frame among 99 fast ones shows at p99 only when it is
	// in the top 1%: with 100 samples p99 is the 99th, so it stays hidden;
	// with 50 samples p99 is the 50th, the slow one.
	fast := make([]int, 49)
	for i := range fast {
		fast[i] = 5
	}
	if got := percentile(ms(append(fast, 900)...), 99); got != 900*time.Millisecond {
		t.Errorf("slow frame in 50: got %v", got)
	}
}

func TestTelemetryWindowResets(t *testing.T) {
	tel := newUITelemetry()
	base := time.Now()
	tel.noteWake()
	tel.noteWake()
	tel.noteInput(base)
	tel.noteInput(base.Add(5 * time.Millisecond)) // ignored: one already pending
	tel.noteFrame(4*time.Millisecond, base.Add(12*time.Millisecond))
	tel.noteFrame(8*time.Millisecond, base.Add(30*time.Millisecond)) // no pending input
	w := tel.takeWindow()
	if w.frames != 2 || w.wakes != 2 {
		t.Fatalf("frames %d wakes %d, want 2 and 2", w.frames, w.wakes)
	}
	if w.inputN != 1 || w.inputP99 != 12*time.Millisecond {
		t.Fatalf("input latency n=%d p99=%v, want 1 and 12ms (oldest input to frame end)", w.inputN, w.inputP99)
	}
	if w.frameP50 != 4*time.Millisecond || w.frameP99 != 8*time.Millisecond {
		t.Fatalf("frame p50 %v p99 %v", w.frameP50, w.frameP99)
	}
	w = tel.takeWindow()
	if w.frames != 0 || w.wakes != 0 || w.inputN != 0 || w.frameP99 != 0 {
		t.Fatalf("second window not reset: %+v", w)
	}
}

func TestInputLatencyDropsUnpaintedInputs(t *testing.T) {
	tel := newUITelemetry()
	base := time.Now()
	tel.noteInput(base) // e.g. Shift: no repaint until the blink 400ms later
	tel.noteFrame(time.Millisecond, base.Add(400*time.Millisecond))
	if w := tel.takeWindow(); w.inputN != 0 || w.frames != 1 {
		t.Fatalf("inputs %d frames %d, want the 400ms sample dropped", w.inputN, w.frames)
	}
}

func TestTelemetrySamplesBounded(t *testing.T) {
	tel := newUITelemetry()
	now := time.Now()
	for i := 0; i < maxTelemetrySamples+100; i++ {
		tel.noteFrame(time.Millisecond, now)
	}
	tel.mu.Lock()
	n := len(tel.frameTimes)
	tel.mu.Unlock()
	if n != maxTelemetrySamples {
		t.Fatalf("samples %d, want cap %d", n, maxTelemetrySamples)
	}
	if w := tel.takeWindow(); w.frames != int64(maxTelemetrySamples+100) {
		t.Fatalf("frame count %d should still count every frame", w.frames)
	}
}

func TestPhaseNesting(t *testing.T) {
	tel := newUITelemetry()
	outer := tel.enterPhase("size")
	time.Sleep(2 * time.Millisecond)
	_, sizeSince := tel.currentPhase(time.Now())
	inner := tel.enterPhase("paint")
	if p, _ := tel.currentPhase(time.Now()); p != "paint" {
		t.Fatalf("phase %q, want paint", p)
	}
	tel.leavePhase(inner)
	p, d := tel.currentPhase(time.Now())
	if p != "size" || d < sizeSince {
		t.Fatalf("after nested paint: phase %q for %v, want size for >= %v", p, d, sizeSince)
	}
	time.Sleep(2 * time.Millisecond)
	tel.leavePhase(outer)
	if p, d := tel.currentPhase(time.Now()); p != "idle" || d > time.Millisecond {
		t.Fatalf("phase %q for %v, want idle restamped to now", p, d)
	}
}

func TestPaneTelemetrySnapshot(t *testing.T) {
	var a, b paneCounters
	registerPaneTelemetry(901, &a)
	registerPaneTelemetry(900, &b)
	defer unregisterPaneTelemetry(900)
	defer unregisterPaneTelemetry(901)

	a.ingested.Add(100)
	a.queued.Store(40)
	b.ingested.Add(7)

	pick := func(ps []paneTelemetry) []paneTelemetry {
		var out []paneTelemetry
		for _, p := range ps {
			if p.id == 900 || p.id == 901 {
				out = append(out, p)
			}
		}
		return out
	}
	ps, _ := paneTelemetrySnapshot(false)
	ps = pick(ps)
	if len(ps) != 2 || ps[0].id != 900 || ps[1].id != 901 {
		t.Fatalf("panes %+v, want 900 then 901", ps)
	}
	if ps[1].ingested != 100 || ps[1].queued != 40 {
		t.Fatalf("pane 901 %+v", ps[1])
	}
	// advance=false (stall dump) must not move the window.
	ps, _ = paneTelemetrySnapshot(true)
	if pick(ps)[1].ingested != 100 {
		t.Fatalf("peek moved the window: %+v", pick(ps))
	}
	a.ingested.Add(5)
	ps, _ = paneTelemetrySnapshot(true)
	if got := pick(ps)[1].ingested; got != 5 {
		t.Fatalf("second window ingested %d, want 5", got)
	}
	if s := formatPanes(pick(ps)); s != "900:0/0 901:5/40" {
		t.Fatalf("formatPanes = %q", s)
	}
}

func TestTabTelemetryCounts(t *testing.T) {
	tb := &tab{}
	tb.inMu.Lock()
	tb.inBuf = append(tb.inBuf, "hello"...)
	tb.tel.queued.Store(int64(len(tb.inBuf)))
	tb.inMu.Unlock()
	if got := tb.takeInput(); string(got) != "hello" {
		t.Fatalf("takeInput %q", got)
	}
	if tb.tel.queued.Load() != 0 || tb.tel.ingested.Load() != 5 {
		t.Fatalf("queued %d ingested %d, want 0 and 5", tb.tel.queued.Load(), tb.tel.ingested.Load())
	}
}

func TestTelemetryConcurrent(t *testing.T) {
	tel := newUITelemetry()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				tel.takeWindow()
				paneTelemetrySnapshot(true)
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		m := tel.enterPhase("paint")
		tel.noteInput(time.Now())
		tel.noteFrame(time.Microsecond, time.Now())
		tel.noteWake()
		tel.leavePhase(m)
	}
	close(stop)
	wg.Wait()
}

func TestAllGoroutineStacks(t *testing.T) {
	s := allGoroutineStacks()
	if !strings.Contains(s, "TestAllGoroutineStacks") || !strings.Contains(s, "goroutine ") || strings.Contains(s, "\t") {
		t.Fatalf("stacks missing this goroutine:\n%.400s", s)
	}
}
