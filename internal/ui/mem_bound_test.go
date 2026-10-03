//go:build windows || darwin

package ui

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

type fakeTabHost struct {
	accept bool
	n      int
}

func (h *fakeTabHost) queueBytes(int) bool {
	h.n++
	return h.accept
}
func (h *fakeTabHost) queueClosed(int) {}
func (h *fakeTabHost) isAlive() bool      { return true }
func (h *fakeTabHost) windowReady() bool  { return true }

func TestTakeInputChunks(t *testing.T) {
	tab := &tab{}
	tab.inBuf = bytes.Repeat([]byte("a"), ptyIngestChunk+100)
	a := tab.takeInput()
	if len(a) != ptyIngestChunk {
		t.Fatalf("chunk %d", len(a))
	}
	if !tab.inputWaiting() {
		t.Fatal("remainder missing")
	}
	if tab.bytesMsg.Load() {
		t.Fatal("bytesMsg should clear so postBytes can re-queue")
	}
	b := tab.takeInput()
	if len(b) != 100 {
		t.Fatalf("rest %d", len(b))
	}
	if tab.inputWaiting() || tab.bytesMsg.Load() {
		t.Fatal("should be empty after the tail drain")
	}
}

func TestPostBytesRetriesAfterDrop(t *testing.T) {
	tab := &tab{inBuf: []byte("x")}
	h := &fakeTabHost{accept: false}
	tab.postBytes(h)
	if h.n != 1 {
		t.Fatalf("first queue %d", h.n)
	}
	if tab.bytesMsg.Load() {
		t.Fatal("bytesMsg stuck after a dropped wake-up")
	}
	if !tab.ingestStalled() {
		t.Fatal("stalled helper should match a waiting buffer with no job")
	}
	h.accept = true
	tab.postBytes(h)
	if h.n != 2 || !tab.bytesMsg.Load() {
		t.Fatalf("retry n=%d bytesMsg=%v", h.n, tab.bytesMsg.Load())
	}
}

func TestPendingPasteCapped(t *testing.T) {
	var dst []pendingPaste
	for i := 0; i < 20; i++ {
		dst = appendPendingPaste(dst, pendingPaste{toast: strconv.Itoa(i)})
	}
	if len(dst) != maxPendingPaste {
		t.Fatalf("len %d", len(dst))
	}
	if dst[0].toast != "12" || dst[len(dst)-1].toast != "19" {
		t.Fatalf("kept %+v", dst)
	}
}

func TestIngestStormStaysBounded(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(80, 24))
	tab := &tab{term: term, sb: newScrollback()}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc

	openLink := []byte("\x1b]8;;https://example.com/x\x07")
	// One ~4MiB dump with sync + an open hyperlink, then smaller bursts.
	// Without caps this held the whole stream in syncBuf/capture.
	big := append([]byte("\x1b[?2026h"), openLink...)
	big = append(big, bytes.Repeat([]byte("storm line of agent output\r\n"), 140000)...)
	tab.inMu.Lock()
	tab.inBuf = append(tab.inBuf, big...)
	if len(tab.inBuf) > maxInBuf {
		tab.inBuf = trimInBufPreferKitty(tab.inBuf, maxInBuf/2)
	}
	tab.inMu.Unlock()
	for tab.inputWaiting() {
		tab.ingestPTY(tab.takeInput(), ptyHooks{})
		if len(tab.modes.syncBuf) > maxSyncBuf {
			t.Fatalf("syncBuf %d", len(tab.modes.syncBuf))
		}
		if len(tab.modes.capture) > maxCapture {
			t.Fatalf("capture %d", len(tab.modes.capture))
		}
	}
	line := bytes.Repeat([]byte("storm line of agent output\r\n"), 400)
	for i := 0; i < 40; i++ {
		chunk := append([]byte("\x1b[?2026h"), line...)
		chunk = append(chunk, []byte("\x1b[?2026l")...)
		tab.inMu.Lock()
		tab.inBuf = append(tab.inBuf, chunk...)
		tab.inMu.Unlock()
		for tab.inputWaiting() {
			tab.ingestPTY(tab.takeInput(), ptyHooks{})
		}
		if len(tab.modes.syncBuf) > maxSyncBuf {
			t.Fatalf("syncBuf %d", len(tab.modes.syncBuf))
		}
		if len(tab.inBuf) > maxInBuf {
			t.Fatalf("inBuf %d", len(tab.inBuf))
		}
	}
	if tab.sb != nil && len(tab.sb.lines) > tab.sb.max {
		t.Fatalf("scrollback %d > %d", len(tab.sb.lines), tab.sb.max)
	}
	runtime.GC()
	runtime.ReadMemStats(&ms)
	// Generous slack: VT grid + 5000-line scrollback + test overhead.
	// The failure mode this guards is unbounded growth into hundreds of MiB.
	if ms.HeapAlloc > base+96<<20 {
		t.Fatalf("heap grew too much: base=%d now=%d", base, ms.HeapAlloc)
	}
	if !strings.Contains(snapshotScreenText(term)[0], "storm") && tab.sb != nil && len(tab.sb.lines) == 0 {
		t.Fatal("storm produced no grid or history")
	}
}
