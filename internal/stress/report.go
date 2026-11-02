package stress

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// procSample is what the driver can see of the child from outside.
type procSample struct {
	AtMS     int64         `json:"at_ms"`
	RSS      uint64        `json:"rss_bytes"`
	Private  uint64        `json:"private_bytes,omitempty"`
	GDI      int           `json:"gdi_handles"`  // -1 when not available
	USER     int           `json:"user_handles"` // -1 when not available
	Handles  int           `json:"handles"`      // kernel handles / fds; -1 n/a
	Goroutin int           `json:"goroutines"`   // from /v1/stats; -1 n/a
	HeapLive uint64        `json:"heap_alloc"`
	Panes    int           `json:"panes"`
}

// series tracks first/last/max of one metric.
type series struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Max   int64 `json:"max"`
	Delta int64 `json:"delta"`
	OK    bool  `json:"available"`
}

func seriesOf(samples []procSample, get func(procSample) int64) series {
	var s series
	for _, p := range samples {
		v := get(p)
		if v < 0 {
			continue
		}
		if !s.OK {
			s.Start, s.Max, s.OK = v, v, true
		}
		s.End = v
		if v > s.Max {
			s.Max = v
		}
	}
	s.Delta = s.End - s.Start
	return s
}

type latency struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50_ms"`
	P90   float64 `json:"p90_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
	Over  map[string]int `json:"over"`
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	// nearest-rank
	k := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	k = max(0, min(k, len(sorted)-1))
	return sorted[k]
}

func summarize(ms []float64) latency {
	s := append([]float64(nil), ms...)
	sort.Float64s(s)
	l := latency{Count: len(s), Over: map[string]int{}}
	if len(s) == 0 {
		return l
	}
	l.P50 = percentile(s, 50)
	l.P90 = percentile(s, 90)
	l.P99 = percentile(s, 99)
	l.Max = s[len(s)-1]
	for _, v := range s {
		if v > 16 {
			l.Over["16ms"]++
		}
		if v > 100 {
			l.Over["100ms"]++
		}
		if v > 1000 {
			l.Over["1s"]++
		}
	}
	return l
}

// Report is printed at the end and written as JSON.
type Report struct {
	Version     int            `json:"version"`
	OS          string         `json:"os"`
	Exe         string         `json:"exe"`
	Seed        uint64         `json:"seed"`
	PanesWanted int            `json:"panes"`
	Planned     string         `json:"duration_planned"`
	Ran         string         `json:"duration_ran"`
	RanMS       int64          `json:"duration_ran_ms"`
	ConfigDir   string         `json:"config_dir"`
	Crashed     bool           `json:"crashed"`
	ExitCode    *int           `json:"exit_code"` // nil while alive at the end
	ExitNote    string         `json:"exit_note,omitempty"`
	CrashTail   []string       `json:"crash_tail,omitempty"`
	Hung        bool           `json:"hung"`
	Ops         map[string]int `json:"ops"`
	OpErrors    map[string]int `json:"op_errors"`
	Probe       latency        `json:"ui_rtt"`
	ProbeBusy   int            `json:"ui_busy_503"`
	ProbeTO     int            `json:"ui_timeout_504"`
	ProbeFail   int            `json:"ui_probe_errors"`
	LongestGap  float64        `json:"longest_unanswered_ms"`
	AppInputP99 *float64       `json:"app_input_latency_p99_ms,omitempty"`
	RSS         series         `json:"rss_bytes"`
	Private     series         `json:"private_bytes"`
	GDI         series         `json:"gdi_handles"`
	USER        series         `json:"user_handles"`
	Handles     series         `json:"handles"`
	Goroutines  series         `json:"goroutines"`
	Heap        series         `json:"heap_alloc"`
	Samples     []procSample   `json:"samples"`
	AppStatsEnd map[string]any `json:"app_stats_end,omitempty"`
	Verdict     string         `json:"verdict"`
	Problems    []string       `json:"problems,omitempty"`
}

// thresholds turn the report into pass/fail. Growth is measured from the
// first sample after warm-up to the last.
type thresholds struct {
	MaxGDIGrowth       int64
	MaxUSERGrowth      int64
	MaxGoroutineGrowth int64
	MaxRSSGrowthMB     int64
	MaxP99ms           float64
}

func (r *Report) judge(t thresholds) {
	var p []string
	if r.Crashed {
		p = append(p, "suzuri exited during the run: "+r.ExitNote)
	}
	if r.Hung {
		p = append(p, fmt.Sprintf("UI thread did not answer for %.0f ms", r.LongestGap))
	}
	if r.GDI.OK && r.GDI.Delta > t.MaxGDIGrowth {
		p = append(p, fmt.Sprintf("GDI handles grew by %d (limit %d)", r.GDI.Delta, t.MaxGDIGrowth))
	}
	if r.USER.OK && r.USER.Delta > t.MaxUSERGrowth {
		p = append(p, fmt.Sprintf("USER handles grew by %d (limit %d)", r.USER.Delta, t.MaxUSERGrowth))
	}
	if r.Goroutines.OK && r.Goroutines.Delta > t.MaxGoroutineGrowth {
		p = append(p, fmt.Sprintf("goroutines grew by %d (limit %d)", r.Goroutines.Delta, t.MaxGoroutineGrowth))
	}
	if r.RSS.OK && t.MaxRSSGrowthMB > 0 && r.RSS.Delta > t.MaxRSSGrowthMB<<20 {
		p = append(p, fmt.Sprintf("RSS grew by %s (limit %d MB)", mb(r.RSS.Delta), t.MaxRSSGrowthMB))
	}
	if t.MaxP99ms > 0 && r.Probe.P99 > t.MaxP99ms {
		p = append(p, fmt.Sprintf("UI round-trip p99 %.1f ms (limit %.0f ms)", r.Probe.P99, t.MaxP99ms))
	}
	r.Problems = p
	if len(p) == 0 {
		r.Verdict = "PASS"
	} else {
		r.Verdict = "FAIL"
	}
}

func mb(b int64) string {
	sign := ""
	if b < 0 {
		sign, b = "-", -b
	}
	return fmt.Sprintf("%s%.1f MB", sign, float64(b)/(1<<20))
}

func (r *Report) print(w io.Writer) {
	fmt.Fprintf(w, "\nsuzuri stress report (%s)\n", r.OS)
	fmt.Fprintf(w, "  run         %s of %s, %d panes, seed %d\n", r.Ran, r.Planned, r.PanesWanted, r.Seed)
	switch {
	case r.Crashed:
		fmt.Fprintf(w, "  exit        CRASHED: %s\n", r.ExitNote)
		for _, l := range r.CrashTail {
			fmt.Fprintf(w, "              | %s\n", l)
		}
	default:
		fmt.Fprintf(w, "  exit        alive at the end (stopped by the harness)\n")
	}
	fmt.Fprintf(w, "  ops         %s\n", fmtCounts(r.Ops))
	if len(r.OpErrors) > 0 {
		fmt.Fprintf(w, "  op errors   %s\n", fmtCounts(r.OpErrors))
	}
	fmt.Fprintf(w, "  UI RTT      p50 %.1f  p90 %.1f  p99 %.1f  max %.1f ms  (%d probes; >16ms %d, >100ms %d, >1s %d)\n",
		r.Probe.P50, r.Probe.P90, r.Probe.P99, r.Probe.Max, r.Probe.Count,
		r.Probe.Over["16ms"], r.Probe.Over["100ms"], r.Probe.Over["1s"])
	fmt.Fprintf(w, "  UI stalls   busy(503) %d  timeout(504) %d  errors %d  longest unanswered %.0f ms\n",
		r.ProbeBusy, r.ProbeTO, r.ProbeFail, r.LongestGap)
	if r.AppInputP99 != nil {
		fmt.Fprintf(w, "  input p99   %.1f ms (app telemetry)\n", *r.AppInputP99)
	} else {
		fmt.Fprintf(w, "  input p99   n/a (app has no input-latency counter yet; UI RTT above is the proxy)\n")
	}
	row := func(name string, s series, f func(int64) string) {
		if !s.OK {
			fmt.Fprintf(w, "  %-11s n/a on this OS\n", name)
			return
		}
		fmt.Fprintf(w, "  %-11s start %s  end %s  max %s  delta %s\n", name, f(s.Start), f(s.End), f(s.Max), f(s.Delta))
	}
	num := func(v int64) string { return fmt.Sprint(v) }
	mbf := func(v int64) string { return mb(v) }
	row("GDI", r.GDI, num)
	row("USER", r.USER, num)
	row("handles", r.Handles, num)
	row("goroutines", r.Goroutines, num)
	row("RSS", r.RSS, mbf)
	row("private", r.Private, mbf)
	row("Go heap", r.Heap, mbf)
	fmt.Fprintf(w, "  verdict     %s\n", r.Verdict)
	for _, p := range r.Problems {
		fmt.Fprintf(w, "              - %s\n", p)
	}
}

func fmtCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	total := 0
	for k, v := range m {
		keys = append(keys, k)
		total += v
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return fmt.Sprintf("%d total: %s", total, strings.Join(parts, ", "))
}
