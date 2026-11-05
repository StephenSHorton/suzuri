//go:build darwin || linux

// Command ptyrec records a program's raw PTY output for the replay corpus in
// internal/ui/testdata/pty. It runs the program in a PTY of a fixed size,
// drives it with a small script (send keys, wait, resize), and writes:
//
//	<out>.bin   the raw bytes, exactly as read from the PTY
//	<out>.json  the size, the read chunk lengths and where resizes happened
//
// Replay feeds the chunks through tab.ingestPTY in the same order, applying
// each resize before the chunk it precedes.
//
// Script lines (one per line, # comments):
//
//	send <go-quoted string>    write bytes to the program, e.g. send "hi\r"
//	sleep <duration>           wait, recording output
//	quiet <idle> <max>         wait until no output for idle (at most max)
//	expect <go-quoted> <max>   wait until the output so far contains the text
//	resize <cols> <rows>       resize the PTY (SIGWINCH)
//
// A shadow vt10x answers cursor position (CSI 6n) and device attribute
// (CSI c) queries, as a terminal would, so programs that ask do not stall.
//
// -scrub old=new replaces text in the recorded bytes; new must be the same
// length as old so the replayed grid keeps its layout.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

type resizeMark struct {
	Chunk  int `json:"chunk"`  // applied before this chunk index
	Offset int `json:"offset"` // the same point as a byte offset into the .bin
	Cols   int `json:"cols"`
	Rows   int `json:"rows"`
}

type meta struct {
	Name    string       `json:"name"`
	Command string       `json:"command"`
	Cols    int          `json:"cols"`
	Rows    int          `json:"rows"`
	Chunks  []int        `json:"chunks"`
	Resizes []resizeMark `json:"resizes,omitempty"`
}

type scrubs []string

func (s *scrubs) String() string     { return strings.Join(*s, ",") }
func (s *scrubs) Set(v string) error { *s = append(*s, v); return nil }

type recorder struct {
	mu      sync.Mutex
	chunks  [][]byte
	resizes []resizeMark
	all     bytes.Buffer
	last    time.Time
	shadow  vt10x.Terminal
	ptmx    *os.File
}

func main() {
	cols := flag.Int("cols", 100, "columns")
	rows := flag.Int("rows", 30, "rows")
	out := flag.String("out", "", "output path without extension")
	script := flag.String("script", "", "script file (default: wait until quiet)")
	dump := flag.Bool("dump", false, "print the final screen to stderr")
	var sc scrubs
	flag.Var(&sc, "scrub", "old=new replacement (same length), repeatable")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: ptyrec -out path [-cols N -rows N -script f -scrub a=b] -- cmd args...")
		os.Exit(2)
	}
	// The working directory and the user name always come out, before any
	// -scrub given on the command line.
	var auto scrubs
	if wd, err := os.Getwd(); err == nil && len(wd) > 1 {
		auto = append(auto, wd+"="+fill("/tmp/suzuri-rec/", len(wd)))
		if real, err := filepath.EvalSymlinks(wd); err == nil && real != wd {
			auto = append(auto, real+"="+fill("/tmp/suzuri-rec/", len(real)))
		}
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		auto = append(auto, u.Username+"="+fill("suzuri-tester", len(u.Username)))
	}
	sc = append(auto, sc...)
	for _, s := range sc {
		old, nw, ok := strings.Cut(s, "=")
		if !ok || len(old) != len(nw) {
			fmt.Fprintf(os.Stderr, "scrub %q: need old=new of equal length\n", s)
			os.Exit(2)
		}
	}

	cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(*cols), Rows: uint16(*rows)})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	r := &recorder{ptmx: ptmx, last: time.Now(), shadow: vt10x.New(vt10x.WithSize(*cols, *rows))}
	done := make(chan struct{})
	go r.read(done)

	steps := []string{"quiet 2s 30s"}
	if *script != "" {
		b, err := os.ReadFile(*script)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		steps = strings.Split(string(b), "\n")
	}
	for _, line := range steps {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := r.step(line, done); err != nil {
			fmt.Fprintf(os.Stderr, "step %q: %v\n", line, err)
			break
		}
	}
	_ = cmd.Process.Kill()
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	_ = cmd.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	if *dump {
		cols, rows := r.shadow.Size()
		for y := 0; y < rows; y++ {
			var b strings.Builder
			for x := 0; x < cols; x++ {
				b.WriteRune(r.shadow.Cell(x, y).Char)
			}
			fmt.Fprintln(os.Stderr, strings.TrimRight(b.String(), " \x00"))
		}
	}
	m := meta{Name: baseName(*out), Command: strings.Join(flag.Args(), " "), Cols: *cols, Rows: *rows, Resizes: r.resizes}
	var raw bytes.Buffer
	for _, c := range r.chunks {
		for _, s := range sc {
			old, nw, _ := strings.Cut(s, "=")
			c = bytes.ReplaceAll(c, []byte(old), []byte(nw))
		}
		for i := range m.Resizes {
			if m.Resizes[i].Chunk == len(m.Chunks) {
				m.Resizes[i].Offset = raw.Len()
			}
		}
		m.Chunks = append(m.Chunks, len(c))
		raw.Write(c)
	}
	for i := range m.Resizes {
		if m.Resizes[i].Chunk >= len(m.Chunks) {
			m.Resizes[i].Offset = raw.Len()
		}
	}
	if err := os.WriteFile(*out+".bin", raw.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	js, _ := json.Marshal(m)
	if err := os.WriteFile(*out+".json", append(js, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s: %d bytes in %d chunks, %d resizes\n", *out, raw.Len(), len(m.Chunks), len(m.Resizes))
}

// fill returns a string of exactly n bytes that starts like prefix.
func fill(prefix string, n int) string {
	for len(prefix) < n {
		prefix += "x"
	}
	return prefix[:n]
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (r *recorder) read(done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 64<<10)
	for {
		n, err := r.ptmx.Read(buf)
		if n > 0 {
			c := append([]byte(nil), buf[:n]...)
			r.mu.Lock()
			r.chunks = append(r.chunks, c)
			r.all.Write(c)
			r.last = time.Now()
			_, _ = r.shadow.Write(c)
			reply := r.answer(c)
			r.mu.Unlock()
			if len(reply) > 0 {
				_, _ = r.ptmx.Write(reply)
			}
		}
		if err != nil {
			return
		}
	}
}

// answer replies to the terminal queries a real terminal would.
func (r *recorder) answer(c []byte) []byte {
	var out []byte
	if bytes.Contains(c, []byte("\x1b[6n")) {
		cur := r.shadow.Cursor()
		out = append(out, fmt.Sprintf("\x1b[%d;%dR", cur.Y+1, cur.X+1)...)
	}
	if bytes.Contains(c, []byte("\x1b[c")) || bytes.Contains(c, []byte("\x1b[0c")) {
		out = append(out, "\x1b[?62;22c"...)
	}
	if bytes.Contains(c, []byte("\x1b[>c")) || bytes.Contains(c, []byte("\x1b[>0c")) {
		out = append(out, "\x1b[>0;10;1c"...)
	}
	return out
}

func (r *recorder) step(line string, done <-chan struct{}) error {
	verb, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	switch verb {
	case "send":
		s, err := strconv.Unquote(rest)
		if err != nil {
			return err
		}
		_, err = r.ptmx.Write([]byte(s))
		return err
	case "sleep":
		d, err := time.ParseDuration(rest)
		if err != nil {
			return err
		}
		time.Sleep(d)
	case "quiet":
		f := strings.Fields(rest)
		if len(f) != 2 {
			return fmt.Errorf("quiet <idle> <max>")
		}
		idle, err1 := time.ParseDuration(f[0])
		max, err2 := time.ParseDuration(f[1])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("bad duration")
		}
		deadline := time.Now().Add(max)
		for time.Now().Before(deadline) {
			r.mu.Lock()
			since := time.Since(r.last)
			r.mu.Unlock()
			if since >= idle {
				return nil
			}
			select {
			case <-done:
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
		fmt.Fprintf(os.Stderr, "quiet: still busy after %s\n", max)
	case "expect":
		i := strings.LastIndexByte(rest, ' ')
		if i < 0 {
			return fmt.Errorf("expect <quoted> <max>")
		}
		want, err := strconv.Unquote(strings.TrimSpace(rest[:i]))
		if err != nil {
			return err
		}
		max, err := time.ParseDuration(rest[i+1:])
		if err != nil {
			return err
		}
		deadline := time.Now().Add(max)
		for {
			r.mu.Lock()
			ok := bytes.Contains(r.all.Bytes(), []byte(want)) || screenHas(r.shadow, want)
			r.mu.Unlock()
			if ok {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for %q", want)
			}
			select {
			case <-done:
				return fmt.Errorf("program exited waiting for %q", want)
			case <-time.After(100 * time.Millisecond):
			}
		}
	case "resize":
		f := strings.Fields(rest)
		if len(f) != 2 {
			return fmt.Errorf("resize <cols> <rows>")
		}
		c, _ := strconv.Atoi(f[0])
		rw, _ := strconv.Atoi(f[1])
		r.mu.Lock()
		r.resizes = append(r.resizes, resizeMark{Chunk: len(r.chunks), Cols: c, Rows: rw})
		r.shadow.Resize(c, rw)
		r.mu.Unlock()
		return pty.Setsize(r.ptmx, &pty.Winsize{Cols: uint16(c), Rows: uint16(rw)})
	default:
		return fmt.Errorf("unknown step %q", verb)
	}
	return nil
}

func screenHas(t vt10x.Terminal, want string) bool {
	cols, rows := t.Size()
	var b strings.Builder
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			b.WriteRune(t.Cell(x, y).Char)
		}
		b.WriteByte('\n')
	}
	sc := bufio.NewScanner(strings.NewReader(b.String()))
	for sc.Scan() {
		if strings.Contains(sc.Text(), want) {
			return true
		}
	}
	return false
}
