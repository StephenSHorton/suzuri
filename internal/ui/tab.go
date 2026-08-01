//go:build windows

package ui

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/host"
)

// tab is one shell session: ConPTY + VT grid + scrollback + Warp input bar.
type tab struct {
	id    int
	title string
	shell string // launch command line (for MCP diag)

	sess *host.Session
	term vt10x.Terminal // UI thread only for Write/Cell
	sb   *scrollback
	sel  cellSel
	// Per-tab command line (draft + history) so switching tabs restores state.
	input inputBar
	// Suppress shell local-echo of the last bar submit (see echo_filter.go).
	echo echoFilter

	writeCh chan []byte
	inMu    sync.Mutex
	inBuf   []byte
	// ptyTail keeps recent raw PTY bytes for MCP diagnostics (escaped in snapshot).
	ptyTail []byte

	alive    atomic.Bool
	bytesMsg atomic.Bool
	closed   bool
	// wasAlt tracks ModeAltScreen across PTY drains so the host can resize
	// when a full-screen app (Claude, Grok Build, vim…) enters/leaves.
	wasAlt bool
}

// altScreen is true when the tab's VT is on the alternate screen buffer.
func (t *tab) altScreen() bool {
	if t == nil || t.term == nil {
		return false
	}
	return t.term.Mode()&vt10x.ModeAltScreen != 0
}

const maxPtyTail = 8192

// tabOpts optional launch recipe (profile).
type tabOpts struct {
	shell string // empty → DefaultShell
	cwd   string // empty → process cwd
	title string // empty → shell N
}

func newTab(id, cols, rows int, opts tabOpts) (*tab, error) {
	shell := opts.shell
	if shell == "" {
		shell = host.DefaultShell()
	}
	sess, err := host.StartSession(shell, cols, rows, opts.cwd)
	if err != nil {
		log.Error("conpty start failed", "tab", id, "shell", shell, "cwd", opts.cwd, "err", err)
		return nil, err
	}
	title := opts.title
	if title == "" {
		title = fmt.Sprintf("shell %d", id+1)
	}
	t := &tab{
		id:      id,
		title:   title,
		shell:   shell,
		sess:    sess,
		term:    vt10x.New(vt10x.WithSize(cols, rows)),
		sb:      newScrollback(),
		input:   inputBar{histIdx: -1},
		writeCh: make(chan []byte, 256),
	}
	t.alive.Store(true)
	log.Info("tab created", "id", id, "shell", shell, "cwd", opts.cwd, "cols", cols, "rows", rows, "pid", sess.Pid())
	return t, nil
}

func (t *tab) startWorkers(u *winUI) {
	go t.writeLoop()
	go t.readLoop(u)
}

func (t *tab) writeLoop() {
	for b := range t.writeCh {
		if _, err := t.sess.Write(b); err != nil {
			log.Warn("pty write failed", "tab", t.id, "err", err)
			return
		}
	}
}

func (t *tab) sendKey(b []byte) {
	if !t.alive.Load() || len(b) == 0 {
		return
	}
	p := append([]byte(nil), b...)
	select {
	case t.writeCh <- p:
	default:
	}
}

func (t *tab) readLoop(u *winUI) {
	buf := make([]byte, 4096)
	for {
		n, err := t.sess.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.inMu.Lock()
			t.inBuf = append(t.inBuf, chunk...)
			if len(t.inBuf) > 1<<20 {
				t.inBuf = t.inBuf[len(t.inBuf)-1<<19:]
			}
			t.ptyTail = append(t.ptyTail, chunk...)
			if len(t.ptyTail) > maxPtyTail {
				t.ptyTail = append([]byte(nil), t.ptyTail[len(t.ptyTail)-maxPtyTail:]...)
			}
			t.inMu.Unlock()
			t.postBytes(u)
		}
		if err != nil {
			t.alive.Store(false)
			log.Info("pty read ended", "tab", t.id, "err", err)
			if u != nil && u.hwnd != 0 && u.alive.Load() {
				// wParam carries tab id for UI thread cleanup.
				postClosed(u, t.id)
			}
			return
		}
	}
}

func (t *tab) postBytes(u *winUI) {
	if u == nil || u.hwnd == 0 || !u.alive.Load() {
		return
	}
	if t.bytesMsg.CompareAndSwap(false, true) {
		postBytes(u, t.id)
	}
}

func (t *tab) takeInput() []byte {
	t.inMu.Lock()
	data := t.inBuf
	t.inBuf = nil
	t.inMu.Unlock()
	t.bytesMsg.Store(false)
	return data
}

func (t *tab) ptyTailCopy() []byte {
	t.inMu.Lock()
	defer t.inMu.Unlock()
	if len(t.ptyTail) == 0 {
		return nil
	}
	return append([]byte(nil), t.ptyTail...)
}

func (t *tab) close() {
	if t.closed {
		return
	}
	t.closed = true
	t.alive.Store(false)
	// Unblock writer
	func() {
		defer func() { _ = recover() }()
		close(t.writeCh)
	}()
	_ = t.sess.Close()
}

func (t *tab) resize(cols, rows int) {
	if t == nil {
		return
	}
	// Never let a bad size or VT panic take down the host (resize used to
	// hard-crash with no trail when ConPTY Resize raced a PTY Read).
	defer func() {
		if r := recover(); r != nil {
			log.Error("tab.resize panic", "tab", t.id, "err", fmt.Sprint(r))
		}
	}()
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols > maxTermCols {
		cols = maxTermCols
	}
	if rows > maxTermRows {
		rows = maxTermRows
	}
	if t.term != nil {
		t.term.Resize(cols, rows)
	}
	// Synchronous, serialized ConPTY resize (see Session.Resize mutex).
	// A fire-and-forget goroutine raced Read and killed the process on mouse-up.
	if t.sess != nil {
		if err := t.sess.Resize(cols, rows); err != nil {
			log.Warn("pty resize failed", "tab", t.id, "cols", cols, "rows", rows, "err", err)
		}
	}
}
