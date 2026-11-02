package stress

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

// Synthetic Claude-like renderer: what `suzuri stress pane` runs in each pane.
//
// It imitates the output pattern of an inline agent TUI (Claude Code and
// friends), which is what stresses suzuri hardest:
//   - a live region at the bottom (spinner line, input box, footer) redrawn
//     ~12 times a second by moving the cursor up and erasing lines;
//   - every frame wrapped in DEC 2026 synchronized output (CSI ?2026h/l);
//   - an OSC 0 title with a spinner glyph;
//   - bursts: hundreds to thousands of coloured lines (tool output, diffs)
//     committed above the live region in a few large writes;
//   - a full clear-and-redraw (CSI 2J 3J H) when the terminal size changes,
//     and now and then on its own, the way Claude repaints after a resize.
//
// Raw-mode stdin is drained and shown in the input box so keys reach a reader.
// It runs until stdout fails (the pane was closed) or stdin hits EOF.

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

var spinnerGlyphs = []string{"·", "✢", "✳", "✶", "✻", "✽"}

var verbs = []string{
	"Thinking", "Pondering", "Reticulating", "Compiling", "Spelunking",
	"Synthesizing", "Untangling", "Refactoring", "Percolating", "Noodling",
}

// paneConfig drives one renderer.
type paneConfig struct {
	Seed       uint64
	FrameEvery time.Duration // live-region redraw interval
	BurstMin   time.Duration // gap between bursts
	BurstMax   time.Duration
	BurstLines int // max lines per burst
	ClearEvery time.Duration
}

func defaultPaneConfig() paneConfig {
	return paneConfig{
		FrameEvery: 80 * time.Millisecond,
		BurstMin:   2 * time.Second,
		BurstMax:   9 * time.Second,
		BurstLines: 1500,
		ClearEvery: 90 * time.Second,
	}
}

func runPane(args []string) int {
	fs := flag.NewFlagSet("stress pane", flag.ContinueOnError)
	cfg := defaultPaneConfig()
	seed := fs.Uint64("seed", 1, "base seed (mixed with SUZURI_PANE_ID)")
	fs.DurationVar(&cfg.FrameEvery, "frame", cfg.FrameEvery, "live-region redraw interval")
	fs.IntVar(&cfg.BurstLines, "burst-lines", cfg.BurstLines, "max lines per burst")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg.Seed = *seed
	if id, err := strconv.ParseUint(os.Getenv("SUZURI_PANE_ID"), 10, 64); err == nil {
		cfg.Seed = cfg.Seed*1_000_003 + id
	}

	if st, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
		defer term.Restore(os.Stdin.Fd(), st)
	}
	// Ctrl+C in raw mode arrives as a byte, but a signal can still come from
	// the host closing the pane; just exit quietly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	r := newRenderer(os.Stdout, cfg)
	r.size = func() (int, int) {
		w, h, err := term.GetSize(os.Stdout.Fd())
		if err != nil {
			return 0, 0
		}
		return w, h
	}
	done := make(chan struct{})
	go r.readInput(os.Stdin, done)
	err := r.run(done, sig)
	if err != nil && !errors.Is(err, errStop) {
		return 1
	}
	return 0
}

var errStop = errors.New("stop")

type renderer struct {
	w    *bufio.Writer
	cfg  paneConfig
	rng  *rand.Rand
	size func() (cols, rows int)

	cols, rows int
	start      time.Time
	frame      int
	tokens     int
	verb       string
	liveDrawn  int // lines of live region currently on screen

	mu    sync.Mutex
	typed []rune
	keys  int
}

func newRenderer(out io.Writer, cfg paneConfig) *renderer {
	return &renderer{
		w:     bufio.NewWriterSize(out, 64<<10),
		cfg:   cfg,
		rng:   rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)),
		start: time.Now(),
		cols:  80,
		rows:  24,
		verb:  verbs[0],
	}
}

func (r *renderer) readInput(in io.Reader, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 256)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			r.mu.Lock()
			for _, ch := range string(buf[:n]) {
				r.keys++
				switch {
				case ch == '\r' || ch == '\n':
					r.typed = r.typed[:0]
				case ch == 0x7f || ch == 0x08:
					if len(r.typed) > 0 {
						r.typed = r.typed[:len(r.typed)-1]
					}
				case ch >= 0x20:
					r.typed = append(r.typed, ch)
					if len(r.typed) > 200 {
						r.typed = r.typed[len(r.typed)-200:]
					}
				}
			}
			r.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (r *renderer) run(done <-chan struct{}, sig <-chan os.Signal) error {
	r.refreshSize()
	if err := r.fullRedraw(); err != nil {
		return err
	}
	tick := time.NewTicker(r.cfg.FrameEvery)
	defer tick.Stop()
	nextBurst := time.Now().Add(r.between(r.cfg.BurstMin, r.cfg.BurstMax))
	nextClear := time.Now().Add(r.between(r.cfg.ClearEvery/2, r.cfg.ClearEvery*3/2))
	for {
		select {
		case <-done:
			return errStop
		case <-sig:
			return errStop
		case now := <-tick.C:
			var err error
			switch {
			case r.refreshSize():
				err = r.fullRedraw()
			case now.After(nextClear):
				nextClear = now.Add(r.between(r.cfg.ClearEvery/2, r.cfg.ClearEvery*3/2))
				err = r.fullRedraw()
			case now.After(nextBurst):
				nextBurst = now.Add(r.between(r.cfg.BurstMin, r.cfg.BurstMax))
				err = r.burst()
			default:
				err = r.drawFrame()
			}
			if err != nil {
				return err
			}
		}
	}
}

func (r *renderer) between(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(r.rng.Int64N(int64(hi-lo)))
}

// refreshSize reports whether the terminal size changed.
func (r *renderer) refreshSize() bool {
	if r.size == nil {
		return false
	}
	c, h := r.size()
	if c <= 0 || h <= 0 || (c == r.cols && h == r.rows) {
		return false
	}
	r.cols, r.rows = c, h
	return true
}

// liveLines is the bottom region: spinner, blank, input box, footer.
func (r *renderer) liveLines() []string {
	r.frame++
	r.tokens += r.rng.IntN(40)
	if r.frame%40 == 0 {
		r.verb = verbs[r.rng.IntN(len(verbs))]
	}
	glyph := spinnerGlyphs[r.frame%len(spinnerGlyphs)]
	secs := int(time.Since(r.start).Seconds())
	r.mu.Lock()
	typed := string(r.typed)
	keys := r.keys
	r.mu.Unlock()

	inner := r.cols - 4
	if inner < 10 {
		inner = 10
	}
	typed = clip("> "+typed, inner)
	bar := strings.Repeat("─", inner+2)
	return []string{
		fmt.Sprintf("\x1b[38;5;208m%s\x1b[0m %s… \x1b[2m(%ds · ↑ %.1fk tokens · esc to interrupt)\x1b[0m",
			glyph, r.verb, secs, float64(r.tokens)/1000),
		"",
		"\x1b[2m╭" + bar + "╮\x1b[0m",
		"\x1b[2m│\x1b[0m " + typed + strings.Repeat(" ", inner-displayLen(typed)) + " \x1b[2m│\x1b[0m",
		"\x1b[2m╰" + bar + "╯\x1b[0m",
		fmt.Sprintf("  \x1b[2m? for shortcuts · %d keys\x1b[0m", keys),
	}
}

// eraseLive moves to the top of the live region and clears to end of screen.
func (r *renderer) eraseLive() {
	if r.liveDrawn > 1 {
		fmt.Fprintf(r.w, "\x1b[%dA", r.liveDrawn-1)
	}
	r.w.WriteString("\r\x1b[J")
	r.liveDrawn = 0
}

func (r *renderer) writeLive() {
	lines := r.liveLines()
	for i, ln := range lines {
		r.w.WriteString("\x1b[2K")
		r.w.WriteString(clipANSI(ln, r.cols))
		if i < len(lines)-1 {
			r.w.WriteString("\r\n")
		}
	}
	r.liveDrawn = len(lines)
}

func (r *renderer) title() {
	glyph := spinnerGlyphs[r.frame%len(spinnerGlyphs)]
	fmt.Fprintf(r.w, "\x1b]0;%s stress pane\x07", glyph)
}

func (r *renderer) drawFrame() error {
	r.w.WriteString(syncBegin)
	r.eraseLive()
	r.writeLive()
	if r.frame%5 == 0 {
		r.title()
	}
	r.w.WriteString(syncEnd)
	return r.w.Flush()
}

func (r *renderer) fullRedraw() error {
	r.w.WriteString(syncBegin)
	r.w.WriteString("\x1b[2J\x1b[3J\x1b[H")
	r.liveDrawn = 0
	r.w.WriteString("\x1b[1;38;5;208m✻ suzuri stress pane\x1b[0m\r\n")
	fmt.Fprintf(r.w, "\x1b[2m  seed %d · %dx%d\x1b[0m\r\n\r\n", r.cfg.Seed, r.cols, r.rows)
	r.writeLive()
	r.title()
	r.w.WriteString(syncEnd)
	return r.w.Flush()
}

// burst commits a block of tool output above the live region. Large bursts go
// out in several synchronized chunks, like a streaming agent.
func (r *renderer) burst() error {
	n := 20 + r.rng.IntN(r.cfg.BurstLines)
	kind := r.rng.IntN(3)
	chunk := 50 + r.rng.IntN(400)
	for done := 0; done < n; {
		r.w.WriteString(syncBegin)
		r.eraseLive()
		if done == 0 {
			fmt.Fprintf(r.w, "\x1b[38;5;114m⏺\x1b[0m \x1b[1m%s\x1b[0m(%d lines)\r\n", []string{"Bash", "Read", "Update"}[kind], n)
		}
		end := min(done+chunk, n)
		for i := done; i < end; i++ {
			r.w.WriteString(clipANSI(r.outputLine(kind, i), r.cols))
			r.w.WriteString("\r\n")
		}
		done = end
		r.writeLive()
		r.w.WriteString(syncEnd)
		if err := r.w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func (r *renderer) outputLine(kind, i int) string {
	words := 3 + r.rng.IntN(14)
	var b strings.Builder
	for j := 0; j < words; j++ {
		if j > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(loremWords[r.rng.IntN(len(loremWords))])
	}
	text := b.String()
	switch kind {
	case 2: // diff
		switch r.rng.IntN(4) {
		case 0:
			return fmt.Sprintf("  \x1b[48;5;22m%4d + %s\x1b[0m", i+1, text)
		case 1:
			return fmt.Sprintf("  \x1b[48;5;52m%4d - %s\x1b[0m", i+1, text)
		}
		return fmt.Sprintf("  \x1b[2m%4d\x1b[0m   %s", i+1, text)
	case 1: // file read with syntax-ish colours
		return fmt.Sprintf("  \x1b[2m%4d\x1b[0m \x1b[38;5;%dm%s\x1b[0m", i+1, 110+r.rng.IntN(100), text)
	}
	return "  ⎿ " + text
}

var loremWords = strings.Fields(`func return err nil if for range struct map string int go defer select
case chan buffer render pane split resize focus close spinner token frame sync burst ConPTY
grid cursor scroll erase line write flush ok true false 0x1b 42 1024 ✓ ✗ → 日本語 漢字 emoji 🙂`)

// clip shortens plain text to n display cells (runes; wide glyphs count once).
func clip(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

func displayLen(s string) int { return len([]rune(s)) }

// clipANSI keeps at most n visible runes, passing escape sequences through.
func clipANSI(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	vis := 0
	inEsc := false
	for _, ch := range s {
		switch {
		case inEsc:
			b.WriteRune(ch)
			if ch >= 0x40 && ch <= 0x7e && ch != '[' {
				inEsc = false
			}
		case ch == 0x1b:
			inEsc = true
			b.WriteRune(ch)
		default:
			if vis >= n-1 { // leave the last column empty (no autowrap)
				continue
			}
			// wide CJK/emoji count as two cells
			w := 1
			if ch >= 0x1100 {
				w = 2
			}
			if vis+w > n-1 {
				vis = n
				continue
			}
			vis += w
			b.WriteRune(ch)
		}
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
