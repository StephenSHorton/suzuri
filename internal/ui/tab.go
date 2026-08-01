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

// tab is one shell session: ConPTY + VT grid + scrollback.
type tab struct {
	id    int
	title string

	sess *host.Session
	term vt10x.Terminal // UI thread only for Write/Cell
	sb   *scrollback
	sel  cellSel

	writeCh chan []byte
	inMu    sync.Mutex
	inBuf   []byte

	alive    atomic.Bool
	bytesMsg atomic.Bool
	closed   bool
}

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
		sess:    sess,
		term:    vt10x.New(vt10x.WithSize(cols, rows)),
		sb:      newScrollback(),
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
	t.term.Resize(cols, rows)
	c, r := cols, rows
	sess := t.sess
	go func() { _ = sess.Resize(c, r) }()
}
