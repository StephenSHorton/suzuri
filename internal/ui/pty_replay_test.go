//go:build windows || darwin

package ui

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// The replay corpus: raw PTY byte streams recorded with tools/ptyrec
// (internal/ui/testdata/pty/README.md). Each one is fed through ingestPTY
// at full speed, in the chunks it was read in, and the final grid and
// scrollback are compared to <name>.golden.
//
// Regenerate the goldens after an intended change:
//
//	go test ./internal/ui -run TestPTYReplay -update-replay

var updateReplay = flag.Bool("update-replay", false, "rewrite internal/ui/testdata/pty/*.golden")

const replayDir = "testdata/pty"

type replayResize struct {
	Chunk  int `json:"chunk"`  // applied before this chunk
	Offset int `json:"offset"` // the same point as a byte offset
	Cols   int `json:"cols"`
	Rows   int `json:"rows"`
}

type replayMeta struct {
	Name    string         `json:"name"`
	Command string         `json:"command"`
	Cols    int            `json:"cols"`
	Rows    int            `json:"rows"`
	Chunks  []int          `json:"chunks"`
	Resizes []replayResize `json:"resizes"`
}

type replayStream struct {
	meta   replayMeta
	chunks [][]byte
}

func loadReplay(tb testing.TB, name string) replayStream {
	tb.Helper()
	js, err := os.ReadFile(filepath.Join(replayDir, name+".json"))
	if err != nil {
		tb.Fatal(err)
	}
	var m replayMeta
	if err := json.Unmarshal(js, &m); err != nil {
		tb.Fatalf("%s.json: %v", name, err)
	}
	raw, err := os.ReadFile(filepath.Join(replayDir, name+".bin"))
	if err != nil {
		tb.Fatal(err)
	}
	s := replayStream{meta: m}
	off := 0
	for _, n := range m.Chunks {
		if off+n > len(raw) {
			tb.Fatalf("%s: chunks run past the %d-byte stream", name, len(raw))
		}
		s.chunks = append(s.chunks, raw[off:off+n])
		off += n
	}
	if off != len(raw) {
		tb.Fatalf("%s: chunks cover %d of %d bytes", name, off, len(raw))
	}
	return s
}

func replayNames(tb testing.TB) []string {
	tb.Helper()
	files, err := filepath.Glob(filepath.Join(replayDir, "*.bin"))
	if err != nil {
		tb.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".bin"))
	}
	sort.Strings(names)
	return names
}

// replay feeds s through a fresh tab and returns it.
func (s replayStream) replay() *tab {
	term := vt10x.New(vt10x.WithSize(s.meta.Cols, s.meta.Rows))
	tb := &tab{term: term, sb: newScrollback()}
	ri := 0
	for i, c := range s.chunks {
		for ri < len(s.meta.Resizes) && s.meta.Resizes[ri].Chunk <= i {
			tb.resize(s.meta.Resizes[ri].Cols, s.meta.Resizes[ri].Rows)
			ri++
		}
		tb.ingestPTY(c, ptyHooks{})
	}
	for ; ri < len(s.meta.Resizes); ri++ {
		tb.resize(s.meta.Resizes[ri].Cols, s.meta.Resizes[ri].Rows)
	}
	return tb
}

// replayGolden renders what a replay left behind: the live grid (trailing
// blanks trimmed), the cursor, the alt-screen bit, and the scrollback.
func replayGolden(tb *tab) []byte {
	var b bytes.Buffer
	cols, rows := tb.term.Size()
	cur := tb.term.Cursor()
	fmt.Fprintf(&b, "== screen %dx%d cursor %d,%d alt=%v ==\n", cols, rows, cur.X, cur.Y, tb.altScreen())
	for _, line := range snapshotScreenText(tb.term) {
		b.WriteString(strings.TrimRight(line, " "))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "== scrollback %d ==\n", len(tb.sb.lines))
	for _, hl := range tb.sb.lines {
		b.WriteString(strings.TrimRight(hl.text, " "))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// writeSyntheticStreams writes the corpus streams built from the test
// helpers rather than recorded: Claude Code's older inline resize wipe
// (CSI 3J), which the current release no longer sends.
func writeSyntheticStreams(t *testing.T) {
	var transcript []string
	for i := 0; i < 80; i++ {
		transcript = append(transcript, fmt.Sprintf("transcript line %02d", i))
	}
	chunks := [][]byte{
		claudeInlineFrame(107, 32, transcript),
		claudeResizeWipe(32),
		claudeInlineFrame(53, 32, transcript),
		claudeResizeWipe(20),
		claudeInlineFrame(53, 20, transcript),
	}
	m := replayMeta{
		Name:    "claude-wipe-synth",
		Command: "synthetic: claudeInlineFrame + claudeResizeWipe (pty_ingest_test.go)",
		Cols:    107,
		Rows:    32,
	}
	var raw bytes.Buffer
	for i, c := range chunks {
		switch i {
		case 1:
			m.Resizes = append(m.Resizes, replayResize{Chunk: i, Offset: raw.Len(), Cols: 53, Rows: 32})
		case 3:
			m.Resizes = append(m.Resizes, replayResize{Chunk: i, Offset: raw.Len(), Cols: 53, Rows: 20})
		}
		m.Chunks = append(m.Chunks, len(c))
		raw.Write(c)
	}
	js, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, m.Name+".bin"), raw.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replayDir, m.Name+".json"), append(js, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPTYReplayGoldens(t *testing.T) {
	if *updateReplay {
		writeSyntheticStreams(t)
	}
	names := replayNames(t)
	if len(names) == 0 {
		t.Fatal("no streams in " + replayDir)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			s := loadReplay(t, name)
			got := replayGolden(s.replay())
			path := filepath.Join(replayDir, name+".golden")
			if *updateReplay {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update-replay to create it)", err)
			}
			// Git on Windows may check the goldens out with CRLF.
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(got, want) {
				t.Fatalf("%s replay differs from its golden:\n%s", name, lineDiff(string(want), string(got)))
			}
		})
	}
}

// TestPTYReplayDeterministic replays each stream twice: the result must not
// depend on anything but the bytes.
func TestPTYReplayDeterministic(t *testing.T) {
	for _, name := range replayNames(t) {
		s := loadReplay(t, name)
		a, b := replayGolden(s.replay()), replayGolden(s.replay())
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two replays differ:\n%s", name, lineDiff(string(a), string(b)))
		}
	}
}

// lineDiff lists the first lines that differ, enough to see what moved.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl == gl {
			continue
		}
		fmt.Fprintf(&b, "line %d\n  want %q\n  got  %q\n", i+1, wl, gl)
		if shown++; shown == 12 {
			b.WriteString("  ...\n")
			break
		}
	}
	return b.String()
}

// BenchmarkFeed_ClaudeStream is the baseline for the VT engine work (C1):
// the recorded Claude Code streaming session through today's ingest path
// (protocol filters, then vt10x), reported as bytes per second.
func BenchmarkFeed_ClaudeStream(b *testing.B) {
	benchFeed(b, "claude-stream")
}

// BenchmarkFeed_CatLarge is a plain-text firehose for comparison.
func BenchmarkFeed_CatLarge(b *testing.B) {
	benchFeed(b, "cat-large")
}

func benchFeed(b *testing.B, name string) {
	s := loadReplay(b, name)
	total := 0
	for _, c := range s.chunks {
		total += len(c)
	}
	b.SetBytes(int64(total))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.replay()
	}
}

// BenchmarkFeed_ClaudeStreamVT10xOnly writes the same chunks straight into
// vt10x, without ingestPTY's filters and screen snapshots. The gap to
// BenchmarkFeed_ClaudeStream is what the pipeline around the parser costs.
func BenchmarkFeed_ClaudeStreamVT10xOnly(b *testing.B) {
	s := loadReplay(b, "claude-stream")
	total := 0
	for _, c := range s.chunks {
		total += len(c)
	}
	b.SetBytes(int64(total))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		term := vt10x.New(vt10x.WithSize(s.meta.Cols, s.meta.Rows))
		for _, c := range s.chunks {
			_, _ = term.Write(c)
		}
	}
}
