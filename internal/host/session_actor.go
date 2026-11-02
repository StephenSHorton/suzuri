package host

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/suzuri/internal/applog"
)

// This file is the ConPTY session actor (Windows handoff §2.5). It has no OS
// build tag, so the state machine is unit-tested on every OS against a fake
// backend; session_windows.go wires it to a real pseudoconsole.
//
// One goroutine per pane owns every native call that touches the
// pseudoconsole or its input pipe: WriteFile, ResizePseudoConsole and the
// close sequence. Those calls can therefore never overlap each other, and none
// of them can run on a handle the close sequence has already released. The
// reader stays on its own goroutine (ReadFile blocks while the pane is idle, so
// it cannot be an actor message); it reports EOF to the actor, which waits for
// it before closing the pipes.

// ptyBackend is what the actor drives. Every method is called only from the
// actor goroutine.
type ptyBackend interface {
	// Write sends input to the console (WriteFile on the input pipe).
	Write(p []byte) (int, error)
	// Resize is ResizePseudoConsole.
	Resize(cols, rows int) error
	// CloseConsole is ClosePseudoConsole. It ends the attached processes; the
	// console's last output then drains and the reader reaches EOF.
	CloseConsole()
	// CancelRead unblocks a reader that did not reach EOF in time (best effort).
	CancelRead()
	// ClosePipes closes this side's pipe handles.
	ClosePipes()
	// KillProcess terminates the shell if it outlived ClosePseudoConsole.
	KillProcess()
	// ReleaseProcess closes the process and thread handles. Called only after
	// the process exit was reported.
	ReleaseProcess()
}

type actorState int32

const (
	stateStarting actorState = iota
	stateRunning
	stateClosing
	stateClosed
)

func (s actorState) String() string {
	switch s {
	case stateStarting:
		return "starting"
	case stateRunning:
		return "running"
	case stateClosing:
		return "closing"
	case stateClosed:
		return "closed"
	}
	return "unknown"
}

// errSessionClosed is returned by Write once the session is closing.
var errSessionClosed = errors.New("session closed")

// actorWriteChunk bounds one WriteFile, so a close request waits at most one
// chunk behind a large paste.
const actorWriteChunk = 4096

type writeReq struct {
	p    []byte
	done chan writeResult
}

type writeResult struct {
	n   int
	err error
}

// actorTiming holds the close-sequence and resize-retry durations (tests
// shorten them).
type actorTiming struct {
	resizeRetry time.Duration // re-check a gated resize after this long
	readerGrace time.Duration // wait for reader EOF after ClosePseudoConsole
	cancelGrace time.Duration // wait again after CancelRead
	exitGrace   time.Duration // wait for the process to exit, then kill
	killGrace   time.Duration // wait after KillProcess
}

var defaultActorTiming = actorTiming{
	resizeRetry: 100 * time.Millisecond,
	readerGrace: 2 * time.Second,
	cancelGrace: 500 * time.Millisecond,
	exitGrace:   3 * time.Second,
	killGrace:   2 * time.Second,
}

// sessionActor serializes the native calls of one pseudoconsole.
type sessionActor struct {
	be     ptyBackend
	timing actorTiming
	// resizeDenied is the timing gate kept until the soak passes (card #253 s4):
	// a resize is postponed, never dropped, while it reports true.
	resizeDenied func() (bool, string)
	// noteResize records a native resize for the process-wide gap gate.
	noteResize func()
	trail      func(where string, kvs ...any)

	state atomic.Int32

	writeCh    chan writeReq
	resizeKick chan struct{}
	closeKick  chan struct{}
	exitCh     chan uint32
	readerCh   chan struct{}

	// Latest requested size (latest wins): packed cols<<32 | rows.
	want    atomic.Uint64
	applied uint64 // actor-only: last size given to the backend

	readerOnce sync.Once
	closeOnce  sync.Once
	exitOnce   sync.Once

	exited   chan struct{} // closed when the process exit is known
	exitCode uint32        // valid once exited is closed
	closed   chan struct{} // the one Closed event
}

func newSessionActor(be ptyBackend, cols, rows int) *sessionActor {
	a := &sessionActor{
		be:           be,
		timing:       defaultActorTiming,
		resizeDenied: func() (bool, string) { return false, "" },
		noteResize:   func() {},
		trail:        applog.Trail,
		writeCh:      make(chan writeReq),
		resizeKick:   make(chan struct{}, 1),
		closeKick:    make(chan struct{}, 1),
		exitCh:       make(chan uint32, 1),
		readerCh:     make(chan struct{}, 1),
		exited:       make(chan struct{}),
		closed:       make(chan struct{}),
	}
	a.state.Store(int32(stateStarting))
	sz := packSize(cols, rows)
	a.want.Store(sz)
	a.applied = sz // the console was created at this size
	return a
}

func packSize(cols, rows int) uint64 { return uint64(uint32(cols))<<32 | uint64(uint32(rows)) }

func unpackSize(v uint64) (cols, rows int) { return int(uint32(v >> 32)), int(uint32(v)) }

func (a *sessionActor) currentState() actorState { return actorState(a.state.Load()) }

func (a *sessionActor) setState(s actorState) {
	a.state.Store(int32(s))
	a.trail("session.actor state", "state", s.String())
}

// start runs the actor goroutine.
func (a *sessionActor) start() { go a.run() }

// Write queues p for the console and waits for the result. Safe from any
// goroutine; returns errSessionClosed once the session is closing.
func (a *sessionActor) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if a.currentState() >= stateClosing {
		return 0, errSessionClosed
	}
	req := writeReq{p: p, done: make(chan writeResult, 1)}
	select {
	case a.writeCh <- req:
	case <-a.closed:
		return 0, errSessionClosed
	}
	select {
	case r := <-req.done:
		return r.n, r.err
	case <-a.closed:
		return 0, errSessionClosed
	}
}

// Resize records the latest wanted size and returns at once. The actor applies
// only the newest size, once the gate allows it.
func (a *sessionActor) Resize(cols, rows int) {
	if st := a.currentState(); st >= stateClosing {
		a.trail("session.actor resize ignored", "state", st.String(), "cols", cols, "rows", rows)
		return
	}
	a.want.Store(packSize(cols, rows))
	select {
	case a.resizeKick <- struct{}{}:
	default: // a kick is already pending; it will read the newest size
	}
}

// Close starts the close sequence on the actor and returns at once.
func (a *sessionActor) Close() {
	a.closeOnce.Do(func() { a.closeKick <- struct{}{} })
}

// ProcessExited reports the shell's exit (from the process waiter).
func (a *sessionActor) ProcessExited(code uint32) {
	a.exitOnce.Do(func() {
		a.exitCode = code
		close(a.exited)
		a.exitCh <- code
	})
}

// ReaderEnded reports that the reader saw EOF or an error.
func (a *sessionActor) ReaderEnded() {
	a.readerOnce.Do(func() { a.readerCh <- struct{}{} })
}

// Exited is closed when the process exit is known; ExitCode is valid then.
func (a *sessionActor) Exited() <-chan struct{} { return a.exited }

func (a *sessionActor) ExitCode() uint32 { return a.exitCode }

// Closed is closed exactly once, after the whole close sequence.
func (a *sessionActor) Closed() <-chan struct{} { return a.closed }

func (a *sessionActor) run() {
	a.setState(stateRunning)
	var retry <-chan time.Time
	readerDone := false
	for {
		// Close and exit win over queued writes and resizes.
		select {
		case <-a.closeKick:
			a.closeSequence("close", readerDone)
			return
		case <-a.exitCh:
			a.closeSequence("exit", readerDone)
			return
		default:
		}
		select {
		case <-a.closeKick:
			a.closeSequence("close", readerDone)
			return
		case <-a.exitCh:
			a.closeSequence("exit", readerDone)
			return
		case <-a.readerCh:
			// Output ended before any close was asked for (console gone).
			readerDone = true
			a.closeSequence("reader", readerDone)
			return
		case req := <-a.writeCh:
			req.done <- a.write(req.p)
		case <-a.resizeKick:
			retry = a.applyResize()
		case <-retry:
			retry = a.applyResize()
		}
	}
}

// write sends p in chunks, stopping if a close or exit arrives between them.
func (a *sessionActor) write(p []byte) writeResult {
	total := 0
	for len(p) > 0 {
		if total > 0 && (len(a.closeKick) > 0 || len(a.exitCh) > 0) {
			return writeResult{total, errSessionClosed}
		}
		n := len(p)
		if n > actorWriteChunk {
			n = actorWriteChunk
		}
		w, err := a.be.Write(p[:n])
		total += w
		if err != nil {
			return writeResult{total, err}
		}
		p = p[n:]
	}
	return writeResult{total, nil}
}

// applyResize gives the newest wanted size to the backend, or returns a retry
// timer when the gate says not now.
func (a *sessionActor) applyResize() <-chan time.Time {
	want := a.want.Load()
	if want == a.applied {
		return nil
	}
	cols, rows := unpackSize(want)
	if denied, reason := a.resizeDenied(); denied {
		a.trail("session.actor resize wait", "cols", cols, "rows", rows, "reason", reason)
		return time.After(a.timing.resizeRetry)
	}
	a.noteResize()
	a.trail("session.actor resize enter", "cols", cols, "rows", rows)
	err := a.be.Resize(cols, rows)
	if err != nil {
		a.trail("session.actor resize fail", "cols", cols, "rows", rows, "err", err)
	} else {
		a.trail("session.actor resize leave", "cols", cols, "rows", rows)
	}
	// Mark it applied either way: retrying a failed ResizePseudoConsole in a
	// loop helps nothing, and a newer size still goes through.
	a.applied = want
	return nil
}

// closeSequence: stop writes, ClosePseudoConsole, reader EOF, close pipes,
// wait for the process, then one Closed event.
func (a *sessionActor) closeSequence(why string, readerDone bool) {
	a.setState(stateClosing)
	a.trail("session.actor close begin", "why", why)
	// 1. Stop writes: fail anything queued. New Write calls see stateClosing.
	for {
		select {
		case req := <-a.writeCh:
			req.done <- writeResult{0, errSessionClosed}
			continue
		default:
		}
		break
	}
	// 2. ClosePseudoConsole: ends the console and its clients.
	a.be.CloseConsole()
	// 3. Wait for the reader to drain the final output and see EOF.
	if !readerDone {
		readerDone = a.waitReader(a.timing.readerGrace)
		if !readerDone {
			a.trail("session.actor reader slow", "grace", a.timing.readerGrace)
			a.be.CancelRead()
			readerDone = a.waitReader(a.timing.cancelGrace)
			if !readerDone {
				a.trail("session.actor reader stuck")
			}
		}
	}
	// 4. Close this side's pipes.
	a.be.ClosePipes()
	// 5. Wait for the process.
	if !a.waitExit(a.timing.exitGrace) {
		a.trail("session.actor process outlived close; killing")
		a.be.KillProcess()
		if !a.waitExit(a.timing.killGrace) {
			a.trail("session.actor process still running; handles kept")
		}
	}
	select {
	case <-a.exited:
		a.be.ReleaseProcess()
	default:
	}
	// 6. One Closed event.
	a.setState(stateClosed)
	close(a.closed)
	a.trail("session.actor closed", "why", why)
}

func (a *sessionActor) waitReader(d time.Duration) bool {
	select {
	case <-a.readerCh:
		return true
	case <-time.After(d):
		return false
	}
}

func (a *sessionActor) waitExit(d time.Duration) bool {
	select {
	case <-a.exited:
		return true
	case <-time.After(d):
		return false
	}
}
