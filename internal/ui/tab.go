//go:build windows || darwin

package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/host"
)

// tabHost is the UI surface a tab posts I/O events to (Win32 or AppKit host).
type tabHost interface {
	queueBytes(tabID int)
	queueClosed(tabID int)
	isAlive() bool
	windowReady() bool
}

// tab is one shell session: PTY + VT grid + scrollback + Warp input bar.
type tab struct {
	id    int
	title string
	shell string // launch command line (for MCP diag)
	// cwd is the shell working directory (OSC from quiet prompt + best-effort
	// cd tracking). UI-thread for reads/writes after start.
	cwd string

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
	// closedMsg ensures we only PostMessage session-ended once (read EOF and
	// Wait can both fire; UI close must be idempotent).
	closedMsg atomic.Bool
	closed    bool
	// lastIOUnixNano is updated on every PTY read chunk (activity indicator).
	lastIOUnixNano atomic.Int64
	// titleBusy: OSC window title has a CLI spinner prefix (e.g. Grok while working).
	// Cleared when the title is rewritten without a spinner frame.
	titleBusy atomic.Bool
	// wasAlt tracks ModeAltScreen across PTY drains so the host can resize
	// when a full-screen app (Claude, Grok Build, vim…) enters/leaves.
	wasAlt bool
	// lastCols/lastRows: last ConPTY/VT size applied (skip no-op Resize — rapid
	// focus/layout thrash was hard-crashing the host via ResizePseudoConsole).
	lastCols, lastRows int
}

// busy is true when this tab should show an activity spinner:
//   - OSC title has a braille/spinner prefix (Grok and similar TUIs), or
//   - recent PTY output (shell commands / short jobs).
func (t *tab) busy() bool {
	if t == nil || !t.alive.Load() {
		return false
	}
	if t.titleBusy.Load() {
		return true
	}
	ns := t.lastIOUnixNano.Load()
	if ns == 0 {
		return false
	}
	window := tabBusyWindow
	if t.altScreen() {
		window = tabBusyWindowAlt
	}
	return time.Since(time.Unix(0, ns)) < window
}

// ingestImages attaches images into scrollback under the current shell stream
// (primary buffer only). Alt-screen apps (Grok) are left alone — click
// "[Open Image]" to open a host modal instead of painting over the TUI.
func (t *tab) ingestImages(paths []string, blobs []imageBlob, cellW, cellH, cols int) {
	if t == nil || t.altScreen() {
		return
	}
	if cellH < 1 {
		cellH = 18
	}
	if cellW < 1 {
		cellW = 9
	}
	if cols < 20 {
		cols = 80
	}
	maxW := cols * cellW
	if maxW > 2400 {
		maxW = 2400
	}
	maxH := 64 * cellH
	add := func(im *tabImage) {
		if im == nil {
			return
		}
		_, dh := fitPreferNative(im.pxW, im.pxH, maxW, maxH)
		span := (dh + cellH - 1) / cellH
		if span < 2 {
			span = 2
		}
		if span > 64 {
			span = 64
		}
		span++ // caption row
		t.sb.pushImage(im, span)
		t.sb.stickBottom()
	}
	for _, blob := range blobs {
		im, err := loadImageBytes(blob.name, blob.data)
		if err != nil {
			log.Debug("inline blob decode failed", "name", blob.name, "err", err)
			continue
		}
		add(im)
	}
	for _, ref := range paths {
		abs := resolveImagePath(t.cwd, ref)
		if abs == "" {
			log.Debug("inline path unresolved", "ref", ref, "cwd", t.cwd)
			continue
		}
		im, err := loadImageFile(abs)
		if err != nil {
			log.Debug("inline path load failed", "path", abs, "err", err)
			continue
		}
		add(im)
	}
}

// applyTitle updates the display title and busy flag from a raw OSC title.
// Grok (and other tools) put braille spinner frames in the window title while
// working; we strip those for the tab label but keep titleBusy for the strip.
// Returns true when the *display* title changed (spinner frame ticks alone
// do not — they used to flood the log and thrash SetWindowText every chunk).
func (t *tab) applyTitle(raw string) bool {
	if t == nil {
		return false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	busy := titleReportsBusy(raw)
	t.titleBusy.Store(busy)
	next := shortTitle(raw)
	if t.title == next {
		return false
	}
	t.title = next
	return true
}

// noteIO marks recent PTY activity for the tab strip glyph.
func (t *tab) noteIO() {
	if t == nil {
		return
	}
	t.lastIOUnixNano.Store(time.Now().UnixNano())
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
		log.Error("pty start failed", "tab", id, "shell", shell, "cwd", opts.cwd, "err", err)
		return nil, err
	}
	title := opts.title
	if title == "" {
		title = fmt.Sprintf("shell %d", id+1)
	}
	cwd := opts.cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	t := &tab{
		id:       id,
		title:    title,
		shell:    shell,
		cwd:      cwd,
		sess:     sess,
		term:     vt10x.New(vt10x.WithSize(cols, rows)),
		sb:       newScrollback(),
		input:    inputBar{histIdx: -1},
		writeCh:  make(chan []byte, 256),
		lastCols: cols,
		lastRows: rows,
	}
	t.alive.Store(true)
	log.Info("tab created", "id", id, "shell", shell, "cwd", cwd, "cols", cols, "rows", rows, "pid", sess.Pid())
	return t, nil
}

// setCwd updates the tab working directory (from OSC or cd tracking).
func (t *tab) setCwd(path string) {
	if t == nil {
		return
	}
	path = stringsTrimSpace(path)
	if path == "" || path == t.cwd {
		return
	}
	t.cwd = path
}

func (t *tab) startWorkers(u tabHost) {
	go t.writeLoop()
	go t.readLoop(u)
	// ConPTY Read often does not return when the shell exits; Wait is reliable.
	go t.waitLoop(u)
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

func (t *tab) readLoop(u tabHost) {
	buf := make([]byte, 4096)
	for {
		n, err := t.sess.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.noteIO()
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
			t.notifyClosed(u)
			return
		}
	}
}

// waitLoop closes the pane when the shell process exits (e.g. user typed `exit`).
// ConPTY Read may hang after process death; Wait + Close unblocks teardown.
func (t *tab) waitLoop(u tabHost) {
	if t == nil || t.sess == nil {
		return
	}
	code, err := t.sess.Wait(context.Background())
	log.Info("shell process exited", "tab", t.id, "code", code, "err", err)
	t.alive.Store(false)
	// Unblock a stuck Read so readLoop can exit.
	func() {
		defer func() { _ = recover() }()
		_ = t.sess.Close()
	}()
	t.notifyClosed(u)
}

// notifyClosed posts one session-ended message to the UI host.
func (t *tab) notifyClosed(u tabHost) {
	if t == nil {
		return
	}
	if !t.closedMsg.CompareAndSwap(false, true) {
		return
	}
	if u != nil && u.windowReady() && u.isAlive() {
		u.queueClosed(t.id)
	}
}

func (t *tab) postBytes(u tabHost) {
	if u == nil || !u.windowReady() || !u.isAlive() {
		return
	}
	if t.bytesMsg.CompareAndSwap(false, true) {
		u.queueBytes(t.id)
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
	if t == nil || t.closed {
		return
	}
	t.closed = true
	t.alive.Store(false)
	// Unblock writer
	func() {
		defer func() { _ = recover() }()
		if t.writeCh != nil {
			close(t.writeCh)
		}
	}()
	// ConPTY Close after shell exit is usually a no-op; still isolate native faults.
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("sess.Close panic", "tab", t.id, "err", fmt.Sprint(r))
			}
		}()
		if t.sess != nil {
			_ = t.sess.Close()
		}
	}()
}

func (t *tab) resize(cols, rows int) {
	if t == nil {
		return
	}
	// Never let a bad size or VT panic take down the host.
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
	// Identical size → no VT/ConPTY work (focus switches used to re-Resize every time).
	if cols == t.lastCols && rows == t.lastRows && t.lastCols > 0 {
		return
	}
	if t.term != nil {
		t.term.Resize(cols, rows)
	}
	if t.sess != nil {
		if err := t.sess.Resize(cols, rows); err != nil {
			log.Warn("pty resize failed", "tab", t.id, "cols", cols, "rows", rows, "err", err)
		}
	}
	t.lastCols, t.lastRows = cols, rows
}
