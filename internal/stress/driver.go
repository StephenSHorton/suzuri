package stress

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// driverConfig is `suzuri stress` flags.
type driverConfig struct {
	Panes       int
	Duration    time.Duration
	Warmup      time.Duration
	Seed        uint64
	OpEvery     time.Duration
	ProbeEvery  time.Duration
	SampleEvery time.Duration
	HangAfter   time.Duration
	Exe         string
	Out         string
	Keep        bool
	Thresholds  thresholds
}

func runDriver(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stress", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c driverConfig
	fs.IntVar(&c.Panes, "panes", 8, "panes to keep open (churn oscillates around this)")
	fs.DurationVar(&c.Duration, "duration", 5*time.Minute, "how long to churn after warm-up")
	fs.DurationVar(&c.Warmup, "warmup", 15*time.Second, "settle time after opening panes, before baseline")
	fs.Uint64Var(&c.Seed, "seed", 1, "seed for the churn script and pane renderers")
	fs.DurationVar(&c.OpEvery, "op-every", 400*time.Millisecond, "interval between churn ops")
	fs.DurationVar(&c.ProbeEvery, "probe-every", 100*time.Millisecond, "interval between UI round-trip probes")
	fs.DurationVar(&c.SampleEvery, "sample-every", 10*time.Second, "interval between metric samples")
	fs.DurationVar(&c.HangAfter, "hang-after", 2*time.Second, "UI unanswered this long counts as a hang")
	fs.StringVar(&c.Exe, "exe", "", "suzuri binary to stress (default: this one)")
	fs.StringVar(&c.Out, "out", "", "report JSON path (default: <config dir>/stress-report.json)")
	fs.BoolVar(&c.Keep, "keep", true, "keep the temp config dir (logs, report)")
	fs.Int64Var(&c.Thresholds.MaxGDIGrowth, "max-gdi-growth", 50, "fail if GDI handles grow more than this")
	fs.Int64Var(&c.Thresholds.MaxUSERGrowth, "max-user-growth", 50, "fail if USER handles grow more than this")
	fs.Int64Var(&c.Thresholds.MaxGoroutineGrowth, "max-goroutine-growth", 50, "fail if goroutines grow more than this")
	fs.Int64Var(&c.Thresholds.MaxRSSGrowthMB, "max-rss-growth-mb", 0, "fail if RSS grows more than this many MB (0 = report only)")
	fs.Float64Var(&c.Thresholds.MaxP99ms, "max-p99-ms", 0, "fail if UI round-trip p99 exceeds this (0 = report only)")
	fs.Usage = func() {
		fmt.Fprint(stderr, `usage: suzuri stress [flags]

Launches an isolated suzuri window (temp config dir, own control server),
opens -panes panes each running a synthetic Claude-like renderer, then churns
split/close/focus/resize for -duration and reports crashes, handle and
goroutine growth, RSS and UI round-trip latency. Exit 0 = PASS, 1 = FAIL,
2 = could not run.

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if c.Panes < 2 {
		c.Panes = 2
	}
	if c.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, "stress:", err)
			return 2
		}
		c.Exe = exe
	}
	if abs, err := filepath.Abs(c.Exe); err == nil {
		c.Exe = abs
	}
	if err := checkExe(c.Exe); err != nil {
		fmt.Fprintln(stderr, "stress:", err)
		return 2
	}
	rep, err := drive(c, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "stress:", err)
		if rep == nil {
			return 2
		}
	}
	if rep == nil {
		return 2
	}
	rep.print(stdout)
	if rep.Verdict != "PASS" {
		return 1
	}
	return 0
}

// child is the suzuri process under test.
type child struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error // from Wait, valid after exited closes
	code   int
	start  time.Time
	died   time.Time
}

func startChild(c driverConfig, dir string) (*child, error) {
	logf, err := os.Create(filepath.Join(dir, "stdout.log"))
	if err != nil {
		return nil, err
	}
	shell := quoteArg(c.Exe) + fmt.Sprintf(" stress pane --seed %d", c.Seed)
	cmd := exec.Command(c.Exe)
	cmd.Dir = dir
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Env = append(filteredEnv(),
		"SUZURI_CONFIG_DIR="+dir,
		"SUZURI_ALLOW_MULTI=1",
		"SUZURI_UI=classic",
		"SUZURI_SHELL="+shell,
		"SUZURI_TITLE_TAG=stress",
	)
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	ch := &child{cmd: cmd, exited: make(chan struct{}), start: time.Now()}
	go func() {
		ch.err = cmd.Wait()
		ch.died = time.Now()
		ch.code = cmd.ProcessState.ExitCode()
		logf.Close()
		close(ch.exited)
	}()
	return ch, nil
}

func (ch *child) alive() bool {
	select {
	case <-ch.exited:
		return false
	default:
		return true
	}
}

// filteredEnv drops variables that would tie the child to the user's own
// suzuri window (when the harness itself runs inside a suzuri pane).
func filteredEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "SUZURI_CONFIG_DIR", "SUZURI_CONTROL_URL", "SUZURI_CONTROL_TOKEN",
			"SUZURI_PANE_ID", "SUZURI", "SUZURI_SHELL", "SUZURI_UI", "SUZURI_TITLE_TAG":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// run state shared by the probe, churn and sampler loops.
type runState struct {
	mu        sync.Mutex
	rtts      []float64
	busy, to  int
	probeErr  int
	lastOK    time.Time
	longest   time.Duration
	ops       map[string]int
	opErrs    map[string]int
	samples   []procSample
	appInput  *float64
	appStats  map[string]any
	logw      io.Writer
	t0        time.Time
}

func (s *runState) opDone(kind string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[kind]++
	if err != nil {
		s.opErrs[kind]++
	}
}

func drive(c driverConfig, out io.Writer) (*Report, error) {
	dir, err := os.MkdirTemp("", "suzuri-stress-")
	if err != nil {
		return nil, err
	}
	if c.Out == "" {
		c.Out = filepath.Join(dir, "stress-report.json")
	}
	fmt.Fprintf(out, "stress: config dir %s\n", dir)
	ch, err := startChild(c, dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if ch.alive() {
			_ = ch.cmd.Process.Kill()
			<-ch.exited
		}
		if !c.Keep {
			_ = os.RemoveAll(dir)
		}
	}()
	fmt.Fprintf(out, "stress: suzuri pid %d (%s)\n", ch.cmd.Process.Pid, c.Exe)

	d, err := waitDiscovery(dir, ch.cmd.Process.Pid, 30*time.Second, ch.exited)
	if err != nil {
		return nil, err
	}
	cl := newClient(d)
	fmt.Fprintf(out, "stress: control %s\n", d.URL)

	rng := rand.New(rand.NewPCG(c.Seed, c.Seed+0x51ed))
	if err := openPanes(cl, c.Panes, ch); err != nil {
		return nil, fmt.Errorf("opening panes: %w", err)
	}
	fmt.Fprintf(out, "stress: %d panes open; warming up %s\n", c.Panes, c.Warmup)
	select {
	case <-time.After(c.Warmup):
	case <-ch.exited:
	}

	st := &runState{ops: map[string]int{}, opErrs: map[string]int{}, logw: out, t0: time.Now(), lastOK: time.Now()}
	pid := ch.cmd.Process.Pid
	st.sample(cl, pid)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); st.probeLoop(cl, c, stop) }()
	go func() { defer wg.Done(); st.churnLoop(cl, c, rng, pid, stop) }()
	go func() { defer wg.Done(); st.sampleLoop(cl, c, pid, stop) }()

	timer := time.NewTimer(c.Duration)
	select {
	case <-timer.C:
	case <-ch.exited:
		timer.Stop()
	}
	close(stop)
	wg.Wait()
	if ch.alive() {
		st.sample(cl, pid)
	}
	ran := time.Since(st.t0)

	rep := &Report{
		Version: 1, OS: runtime.GOOS + "/" + runtime.GOARCH, Exe: c.Exe, Seed: c.Seed,
		PanesWanted: c.Panes, Planned: c.Duration.String(), Ran: ran.Round(time.Second).String(),
		RanMS: ran.Milliseconds(), ConfigDir: dir,
	}
	st.mu.Lock()
	rep.Ops, rep.OpErrors = st.ops, st.opErrs
	rep.Probe = summarize(st.rtts)
	rep.ProbeBusy, rep.ProbeTO, rep.ProbeFail = st.busy, st.to, st.probeErr
	gap := st.longest
	if ch.alive() {
		gap = max(gap, time.Since(st.lastOK))
	}
	rep.LongestGap = float64(gap.Milliseconds())
	rep.Hung = gap >= c.HangAfter
	rep.Samples = st.samples
	rep.AppInputP99 = st.appInput
	rep.AppStatsEnd = st.appStats
	st.mu.Unlock()

	if !ch.alive() {
		code := ch.code
		rep.ExitCode = &code
		rep.Crashed = true
		rep.ExitNote = fmt.Sprintf("exit code %d after %s", code, ch.died.Sub(ch.start).Round(time.Second))
		rep.CrashTail = crashTail(dir)
	}
	s := rep.Samples
	rep.RSS = seriesOf(s, func(p procSample) int64 { return int64(p.RSS) })
	rep.Private = seriesOf(s, func(p procSample) int64 {
		if p.Private == 0 {
			return -1
		}
		return int64(p.Private)
	})
	rep.GDI = seriesOf(s, func(p procSample) int64 { return int64(p.GDI) })
	rep.USER = seriesOf(s, func(p procSample) int64 { return int64(p.USER) })
	rep.Handles = seriesOf(s, func(p procSample) int64 { return int64(p.Handles) })
	rep.Goroutines = seriesOf(s, func(p procSample) int64 { return int64(p.Goroutin) })
	rep.Heap = seriesOf(s, func(p procSample) int64 {
		if p.Goroutin < 0 {
			return -1
		}
		return int64(p.HeapLive)
	})
	rep.judge(c.Thresholds)
	if b, err := json.MarshalIndent(rep, "", "  "); err == nil {
		if err := os.WriteFile(c.Out, b, 0o644); err != nil {
			fmt.Fprintln(out, "stress: write report:", err)
		} else {
			fmt.Fprintf(out, "stress: report written to %s\n", c.Out)
		}
	}
	return rep, nil
}

// openPanes splits the active tab until it has n panes, then equalizes.
func openPanes(cl *client, n int, ch *child) error {
	for tries := 0; tries < n*4; tries++ {
		if !ch.alive() {
			return errors.New("suzuri exited")
		}
		res := cl.call("layout", nil)
		if res.Err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		ids, _ := paneIDs(res.Body)
		if len(ids) >= n {
			for _, id := range ids {
				cl.call("equalize", map[string]any{"pane_id": id})
			}
			return nil
		}
		if len(ids) == 0 {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		// Split the pane with the most room: alternate axes so the grid stays
		// roughly square.
		target := ids[len(ids)/2]
		axis := "right"
		if len(ids)%2 == 1 {
			axis = "down"
		}
		cl.call("split", map[string]any{"pane_id": target, "axis": axis})
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("could not reach %d panes", n)
}

func (s *runState) probeLoop(cl *client, c driverConfig, stop <-chan struct{}) {
	t := time.NewTicker(c.ProbeEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		res := cl.call("ping", nil)
		s.mu.Lock()
		now := time.Now()
		switch {
		case res.Status == 200:
			s.rtts = append(s.rtts, float64(res.RTT.Microseconds())/1000)
			if gap := now.Add(-res.RTT).Sub(s.lastOK); gap > s.longest {
				// time from the last good answer to when this probe was sent
				// plus its own round trip: how long the UI went unanswered.
				s.longest = max(s.longest, now.Sub(s.lastOK)-c.ProbeEvery)
			}
			s.longest = max(s.longest, res.RTT)
			s.lastOK = now
		case res.Status == 503:
			s.busy++
		case res.Status == 504:
			s.to++
		default:
			s.probeErr++
		}
		s.mu.Unlock()
	}
}

// churn op kinds and weights.
var churnOps = []struct {
	name   string
	weight int
}{
	{"focus", 30},
	{"grow", 10},
	{"shrink", 10},
	{"equalize", 5},
	{"rotate", 4},
	{"swap", 4},
	{"split_close", 20},
	{"window_resize", 12},
	{"layout", 5},
}

func pickOp(rng *rand.Rand) string {
	total := 0
	for _, o := range churnOps {
		total += o.weight
	}
	n := rng.IntN(total)
	for _, o := range churnOps {
		if n < o.weight {
			return o.name
		}
		n -= o.weight
	}
	return churnOps[0].name
}

func (s *runState) churnLoop(cl *client, c driverConfig, rng *rand.Rand, pid int, stop <-chan struct{}) {
	t := time.NewTicker(c.OpEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		lay := cl.call("layout", nil)
		if lay.Err != nil {
			s.opDone("layout", lay.Err)
			continue
		}
		ids, _ := paneIDs(lay.Body)
		if len(ids) == 0 {
			s.opDone("layout", errors.New("no panes"))
			continue
		}
		pane := ids[rng.IntN(len(ids))]
		op := pickOp(rng)
		var res callResult
		switch op {
		case "split_close":
			// Keep the count within one of the target; never below 2.
			split := len(ids) < c.Panes || (len(ids) == c.Panes && rng.IntN(2) == 0)
			if split {
				op = "split"
				axis := "right"
				if rng.IntN(2) == 0 {
					axis = "down"
				}
				res = cl.call("split", map[string]any{"pane_id": pane, "axis": axis})
			} else if len(ids) > 2 {
				op = "close"
				res = cl.call("close", map[string]any{"pane_id": pane})
			} else {
				op = "split"
				res = cl.call("split", map[string]any{"pane_id": pane, "axis": "right"})
			}
		case "window_resize":
			if err := resizeWindow(pid, rng); err != nil {
				if errors.Is(err, errUnsupported) {
					op = "grow"
					res = cl.call("grow", map[string]any{"pane_id": pane})
					break
				}
				s.opDone(op, err)
				continue
			}
			s.opDone(op, nil)
			continue
		case "layout":
			res = lay
		default:
			res = cl.call(op, map[string]any{"pane_id": pane})
		}
		err := res.Err
		// "no split" for rotate/swap/equalize on a lone leaf is fine.
		if err != nil && res.Status == 400 && strings.Contains(fmt.Sprint(res.Body["error"]), "no split") {
			err = nil
		}
		s.opDone(op, err)
	}
}

func (s *runState) sampleLoop(cl *client, c driverConfig, pid int, stop <-chan struct{}) {
	t := time.NewTicker(c.SampleEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.sample(cl, pid)
		}
	}
}

func (s *runState) sample(cl *client, pid int) {
	ps, err := sampleProcess(pid)
	if err != nil {
		ps = procSample{GDI: -1, USER: -1, Handles: -1}
	}
	ps.Goroutin = -1
	ps.AtMS = time.Since(s.t0).Milliseconds()
	if st := cl.stats(); st.Err == nil {
		if v, ok := st.Body["goroutines"].(float64); ok {
			ps.Goroutin = int(v)
		}
		if v, ok := st.Body["heap_alloc"].(float64); ok {
			ps.HeapLive = uint64(v)
		}
		s.mu.Lock()
		s.appStats = st.Body
		// The hang watchdog / telemetry card may add a real input-latency
		// figure; prefer it over the round-trip proxy when present.
		for _, k := range []string{"input_latency_p99_ms", "input_p99_ms"} {
			if v, ok := st.Body[k].(float64); ok {
				vv := v
				s.appInput = &vv
			}
		}
		s.mu.Unlock()
	}
	if p := cl.call("ping", nil); p.Err == nil {
		if n, ok := p.Body["panes"].(float64); ok {
			ps.Panes = int(n)
		}
	}
	s.mu.Lock()
	s.samples = append(s.samples, ps)
	var p99 float64
	if n := len(s.rtts); n > 0 {
		p99 = summarize(s.rtts).P99
	}
	ops := 0
	for _, v := range s.ops {
		ops += v
	}
	s.mu.Unlock()
	fmt.Fprintf(s.logw, "stress: %6s panes %2d  rss %s  gdi %d user %d handles %d  goroutines %d  ops %d  ui p99 %.1f ms\n",
		time.Since(s.t0).Round(time.Second), ps.Panes, mb(int64(ps.RSS)), ps.GDI, ps.USER, ps.Handles, ps.Goroutin, ops, p99)
}

// crashTail returns the last lines of the child's log and crash files.
func crashTail(dir string) []string {
	var out []string
	for _, name := range []string{"suzuri-crash.log", "crash.log", "suzuri.log", "stdout.log"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+name))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil || len(b) == 0 {
				continue
			}
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > 15 {
				lines = lines[len(lines)-15:]
			}
			out = append(out, "["+filepath.Base(m)+"]")
			out = append(out, lines...)
		}
	}
	return out
}

var errUnsupported = errors.New("not supported on this OS")
