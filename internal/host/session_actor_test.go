package host

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBackend records native calls in order and checks that no two overlap.
type fakeBackend struct {
	mu    sync.Mutex
	calls []string

	inflight    atomic.Int32
	maxInflight atomic.Int32

	// writeGate, when set, blocks each Write until it is received from.
	writeGate chan struct{}
	// onCloseConsole runs (in a goroutine) when CloseConsole is called: the
	// real console ends its clients and its output, which looks like this.
	onCloseConsole func()
}

func (f *fakeBackend) enter(call string) {
	if n := f.inflight.Add(1); n > f.maxInflight.Load() {
		f.maxInflight.Store(n)
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeBackend) leave() { f.inflight.Add(-1) }

func (f *fakeBackend) Write(p []byte) (int, error) {
	f.enter(fmt.Sprintf("write:%d", len(p)))
	defer f.leave()
	if f.writeGate != nil {
		<-f.writeGate
	}
	time.Sleep(50 * time.Microsecond) // widen any overlap window
	return len(p), nil
}

func (f *fakeBackend) Resize(cols, rows int) error {
	f.enter(fmt.Sprintf("resize:%dx%d", cols, rows))
	defer f.leave()
	time.Sleep(50 * time.Microsecond)
	return nil
}

func (f *fakeBackend) CloseConsole() {
	f.enter("closeConsole")
	defer f.leave()
	if f.onCloseConsole != nil {
		go f.onCloseConsole()
	}
}

func (f *fakeBackend) CancelRead()     { f.enter("cancelRead"); f.leave() }
func (f *fakeBackend) ClosePipes()     { f.enter("closePipes"); f.leave() }
func (f *fakeBackend) KillProcess()    { f.enter("kill"); f.leave() }
func (f *fakeBackend) ReleaseProcess() { f.enter("release"); f.leave() }

func (f *fakeBackend) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeBackend) count(prefix string) int {
	n := 0
	for _, c := range f.log() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

var fastTiming = actorTiming{
	resizeRetry: 5 * time.Millisecond,
	readerGrace: 50 * time.Millisecond,
	cancelGrace: 20 * time.Millisecond,
	exitGrace:   50 * time.Millisecond,
	killGrace:   20 * time.Millisecond,
}

// newTestActor wires a fake console that, like conhost, ends its output and
// its shell when ClosePseudoConsole runs.
func newTestActor(t *testing.T) (*sessionActor, *fakeBackend) {
	t.Helper()
	f := &fakeBackend{}
	a := newSessionActor(f, 80, 24)
	a.timing = fastTiming
	a.trail = func(string, ...any) {}
	f.onCloseConsole = func() {
		a.ReaderEnded()
		a.ProcessExited(0)
	}
	a.start()
	return a, f
}

func waitClosed(t *testing.T, a *sessionActor) {
	t.Helper()
	select {
	case <-a.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("session never reached Closed")
	}
}

func indexOf(calls []string, c string) int {
	for i, x := range calls {
		if x == c {
			return i
		}
	}
	return -1
}

func TestActorCloseOrder(t *testing.T) {
	a, f := newTestActor(t)
	if _, err := a.Write([]byte("dir\r")); err != nil {
		t.Fatal(err)
	}
	a.Close()
	waitClosed(t, a)
	calls := f.log()
	cc, cp, rel := indexOf(calls, "closeConsole"), indexOf(calls, "closePipes"), indexOf(calls, "release")
	if cc < 0 || cp < cc || rel < cp {
		t.Fatalf("close order wrong: %v", calls)
	}
	if f.count("cancelRead") != 0 || f.count("kill") != 0 {
		t.Fatalf("clean close should not cancel or kill: %v", calls)
	}
	if a.currentState() != stateClosed {
		t.Fatalf("state %v", a.currentState())
	}
}

// After Closed no call reaches the backend: wrong-state calls are no-ops.
func TestActorNoNativeCallAfterClose(t *testing.T) {
	a, f := newTestActor(t)
	a.Close()
	waitClosed(t, a)
	before := len(f.log())
	if _, err := a.Write([]byte("x")); err != errSessionClosed {
		t.Fatalf("Write after close: %v", err)
	}
	a.Resize(100, 30)
	a.Close()
	a.ProcessExited(1)
	a.ReaderEnded()
	time.Sleep(20 * time.Millisecond)
	if after := f.log(); len(after) != before {
		t.Fatalf("native calls after Closed: %v", after[before:])
	}
}

// Close and the shell exiting at once still give one close sequence.
func TestActorOneClosedForCloseAndExit(t *testing.T) {
	for i := 0; i < 50; i++ {
		a, f := newTestActor(t)
		go a.ProcessExited(0)
		go a.Close()
		waitClosed(t, a)
		if n := f.count("closeConsole"); n != 1 {
			t.Fatalf("closeConsole ran %d times", n)
		}
		if n := f.count("closePipes"); n != 1 {
			t.Fatalf("closePipes ran %d times", n)
		}
	}
}

// The shell exiting on its own (user typed exit) runs the same sequence.
func TestActorExitStartsClose(t *testing.T) {
	a, f := newTestActor(t)
	a.ProcessExited(0)
	waitClosed(t, a)
	if indexOf(f.log(), "closeConsole") < 0 || indexOf(f.log(), "release") < 0 {
		t.Fatalf("exit did not close: %v", f.log())
	}
	select {
	case <-a.Exited():
	default:
		t.Fatal("Exited not signalled")
	}
}

// While the gate says no, resizes pile up; only the newest is applied.
func TestActorResizeLatestWins(t *testing.T) {
	a, f := newTestActor(t)
	var allow atomic.Bool
	a.resizeDenied = func() (bool, string) {
		if allow.Load() {
			return false, ""
		}
		return true, "recentIO"
	}
	for c := 81; c <= 90; c++ {
		a.Resize(c, 30)
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if n := f.count("resize:"); n != 0 {
		t.Fatalf("resized while gated: %v", f.log())
	}
	allow.Store(true)
	time.Sleep(50 * time.Millisecond)
	if got := f.log(); f.count("resize:") != 1 || indexOf(got, "resize:90x30") < 0 {
		t.Fatalf("want one resize to 90x30, got %v", got)
	}
	a.Close()
	waitClosed(t, a)
}

// Resizes asked for while the actor is busy coalesce into one.
func TestActorResizeCoalesces(t *testing.T) {
	a, f := newTestActor(t)
	f.writeGate = make(chan struct{})
	done := make(chan struct{})
	go func() { _, _ = a.Write([]byte("busy")); close(done) }()
	time.Sleep(10 * time.Millisecond) // the actor is now inside Write
	for c := 100; c < 120; c++ {
		a.Resize(c, 40)
	}
	f.writeGate <- struct{}{}
	<-done
	f.writeGate = nil
	time.Sleep(20 * time.Millisecond)
	if n := f.count("resize:"); n != 1 || indexOf(f.log(), "resize:119x40") < 0 {
		t.Fatalf("want one resize to 119x40, got %v", f.log())
	}
	a.Close()
	waitClosed(t, a)
}

// The same size as the console already has is not a native call.
func TestActorResizeSameSizeNoop(t *testing.T) {
	a, f := newTestActor(t)
	a.Resize(80, 24)
	time.Sleep(10 * time.Millisecond)
	if n := f.count("resize:"); n != 0 {
		t.Fatalf("no-op resize reached the console: %v", f.log())
	}
	a.Close()
	waitClosed(t, a)
}

// The core guarantee: writes, resizes and close never overlap natively.
func TestActorNativeCallsNeverOverlap(t *testing.T) {
	a, f := newTestActor(t)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = a.Write([]byte("abc"))
			}
		}()
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				a.Resize(80+(i+g)%40, 24+i%10)
			}
		}(g)
	}
	wg.Wait()
	a.Close()
	waitClosed(t, a)
	if m := f.maxInflight.Load(); m != 1 {
		t.Fatalf("native calls overlapped: max in flight %d", m)
	}
	if f.count("resize:") == 0 || f.count("write:") == 0 {
		t.Fatalf("stress did nothing: %d resizes, %d writes", f.count("resize:"), f.count("write:"))
	}
}

func TestActorWriteChunks(t *testing.T) {
	a, f := newTestActor(t)
	n, err := a.Write(make([]byte, 10000))
	if err != nil || n != 10000 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got := f.count("write:"); got != 3 {
		t.Fatalf("want 3 chunks, got %v", f.log())
	}
	a.Close()
	waitClosed(t, a)
}

// A reader that never sees EOF is cancelled; the pipes still close.
func TestActorStuckReader(t *testing.T) {
	f := &fakeBackend{}
	a := newSessionActor(f, 80, 24)
	a.timing = fastTiming
	a.trail = func(string, ...any) {}
	f.onCloseConsole = func() { a.ProcessExited(0) } // output never ends
	a.start()
	a.Close()
	waitClosed(t, a)
	calls := f.log()
	if ci, cp := indexOf(calls, "cancelRead"), indexOf(calls, "closePipes"); ci < 0 || cp < ci {
		t.Fatalf("want cancelRead then closePipes: %v", calls)
	}
}

// A shell that outlives ClosePseudoConsole is killed; its handles are kept if
// it still will not die (never closed under a live waiter).
func TestActorProcessOutlivesClose(t *testing.T) {
	f := &fakeBackend{}
	a := newSessionActor(f, 80, 24)
	a.timing = fastTiming
	a.trail = func(string, ...any) {}
	f.onCloseConsole = func() { a.ReaderEnded() } // the process never exits
	a.start()
	a.Close()
	waitClosed(t, a)
	if f.count("kill") != 1 {
		t.Fatalf("want one kill: %v", f.log())
	}
	if f.count("release") != 0 {
		t.Fatalf("released handles of a live process: %v", f.log())
	}
}

// Output ending by itself (console gone) also closes, once.
func TestActorReaderEndCloses(t *testing.T) {
	a, f := newTestActor(t)
	a.ReaderEnded()
	waitClosed(t, a)
	if f.count("closeConsole") != 1 || f.count("cancelRead") != 0 {
		t.Fatalf("reader-end close: %v", f.log())
	}
}

func TestActorStateNames(t *testing.T) {
	for s, want := range map[actorState]string{stateStarting: "starting", stateRunning: "running", stateClosing: "closing", stateClosed: "closed"} {
		if s.String() != want {
			t.Errorf("%d = %q", s, s.String())
		}
	}
}
