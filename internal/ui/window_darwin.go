//go:build darwin

package ui

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"image/color"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/caffeine"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/vt"
)

// Run opens a native macOS window with one shell tab (more via Ctrl+Shift+T).
// Chrome (tabs, status, palette) is a Charm Bubble Tea model; the shell is VT.
func Run() error {
	cols, rows := 100, 28
	cfg, err := config.Load()
	if err != nil {
		log.Warn("config load failed; using defaults", "err", err)
		cfg = config.Default()
	}
	cfg = config.Normalize(cfg)
	chrome.ApplyTheme(cfg.Theme)
	SetShellANSIMap(cfg.ShellANSIMap)
	log.Info("ui.Run", "cols", cols, "rows", rows, "font", cfg.FontFace, "fontPx", cfg.FontSizePx,
		"theme", cfg.Theme, "ansi", cfg.ShellANSIMap, "config", config.Path())

	ui := &macUI{
		cols:       cols,
		rows:       rows,
		cfg:        cfg,
		blinkStart: time.Now(),
		nextTabID:  0,
		chrome:     chrome.New(cols),
		painter:    newSoftwarePainter(cfg.FontFace, cfg.FontSizePx),
		jobs:       make(chan func(), 64),
		mcpJobs:    make(chan mcpJob, 8),
		caffeine:   caffeine.New(),
	}
	// Caffeine on by default so long sessions don't sleep under the user.
	if ui.caffeine != nil {
		_ = ui.caffeine.Activate(0)
	}
	ui.chrome = ui.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: cfg}).Model
	ui.chrome = syncCaffeineChrome(ui.chrome, ui.caffeine)
	if bank, err := chrome.LoadNotesBank(); err != nil {
		log.Warn("notes load failed; using empty bank", "err", err)
	} else {
		ui.chrome = ui.chrome.UpdateChrome(chrome.LoadNotesMsg{Bank: bank}).Model
	}
	ui.alive.Store(true)
	ui.bridge = bridge.NewHost()
	ui.bridge.BindSubmit(ui.enqueueMCPSubmit)
	ui.bridge.BindNotes(ui.enqueueMCPNotes)
	ui.bridge.BindWorkspace(ui.enqueueMCPWorkspace)
	if ui.painter != nil {
		ui.metricW, ui.metricH = int32(ui.painter.cellW), int32(ui.painter.cellH)
	}

	prof := config.FindProfile(cfg, cfg.ActiveProfile)
	opts := tabOpts{}
	if prof != nil {
		opts.shell = prof.Shell
		opts.cwd = prof.Cwd
		if prof.Name != "" && prof.Name != "Default" {
			opts.title = prof.Name
		}
	}
	t, err := newTab(ui.nextTabID, cols, rows, opts)
	if err != nil {
		log.Error("first tab failed", "err", err)
		return err
	}
	ui.nextTabID++
	ui.tabs = []*tab{t}
	ui.pages = []*page{newPage(t)}
	ui.active = 0
	ui.syncChrome()
	ui.showSplash = !cfg.FirstRunDone
	return ui.loop()
}

// mcpJob is work from the loopback MCP bridge (HTTP goroutine → UI tick).
type mcpJob struct {
	// Submit (kind empty or "submit")
	tabID int
	line  string
	done  chan error
	// Notes bank CRUD
	notes    bool
	notesReq bridge.NotesRequest
	notesOut chan bridge.NotesResult
	// Shared workspace
	workspace    bool
	workspaceReq bridge.WorkspaceRequest
	workspaceOut chan bridge.WorkspaceResult
}

type macUI struct {
	// pages are chrome-strip tabs; each may hold a split tree of panes.
	// tabs is the flat list of all pane sessions (I/O by id, bridge, teardown).
	pages     []*page
	tabs      []*tab
	active    int
	nextTabID int
	chrome    chrome.Model

	// lastPaneLayout / lastSashes: active page geometry (paint/hit/focus/drag).
	lastPaneLayout []paneGeom
	lastSashes     []sashGeom
	lastShell      struct{ x, y, w, h int32 }
	// sashDrag is non-nil while the user is dragging a shared pane divider.
	sashDrag *sashGeom

	width    int32
	height   int32
	cols     int
	rows     int
	cfg      config.Config
	metricW  int32
	metricH  int32
	chromePx int32
	inputPx  int32

	blinkStart    time.Time
	alive         atomic.Bool
	ready         atomic.Bool
	lastBackspace time.Time
	selecting     bool
	statusUntil   time.Time
	showSplash    bool
	quit          bool

	// Startup curtain (matrix / ripple / none) — same lifecycle as Windows host.
	matrixIntroStart    time.Time
	matrixIntroSpawnEnd time.Time
	matrixIntroDone     bool
	matrixIntroClearAt  time.Time

	// Settings underlay intro preview (loops with a gap between full plays).
	settingsPreviewT0      time.Time
	settingsIntroIdleUntil time.Time
	// settingsShowedIntro tracks whether the previous frame was the Intro
	// showcase so focusing that row restarts a full curtain cycle.
	settingsShowedIntro bool

	// Deferred work from PTY/MCP goroutines → UI tick.
	jobs    chan func()
	mcpJobs chan mcpJob
	bridge  *bridge.Host

	painter *softwarePainter
	fb      *image.RGBA
	tex     *ebiten.Image

	// Key repeat for held arrows / backspace in the Warp bar and notes.
	keyRep *keyRepeat

	// Chrome paint cache.
	chromeDirty  bool
	chromeCols   int
	chromeCells  [][]cellPix
	overlayCells [][]cellPix
	overlayDirty bool

	// inputOnlyDirty: Warp bar text/caret changed but shell cells did not.
	// When shell matrix rain is off, Draw can re-paint only the bar(s).
	inputOnlyDirty bool

	// Mouse
	mouseDown bool
	// shellMulti / notesMulti: double-click word, triple-click line selection.
	shellMulti multiClick
	notesMulti multiClick
	// notesDragging: left button down after a notes body click (extend selection).
	notesDragging bool
	// Link hover: http(s)/www under the cursor in the shell grid.
	hoverLink    linkSpan
	hoverLinkOK  bool
	linkCursorOn bool

	// modalImage: full-window lightbox (click path / Open Image / image block).
	modalImage *tabImage
	// altMouseDown: left button held while reporting clicks to an alt-screen app.
	altMouseDown bool
	// Last SGR motion cell (1-based) sent to alt-screen; avoid flooding the PTY.
	altMouseCol, altMouseRow int

	// Window placement: last captured frame (updated mid-session; flushed on exit).
	// restoreMax is applied once after the ebiten loop creates the native window.
	lastPlacement config.WindowPlacement
	restoreMax    bool
	maxApplied    bool

	// Stay-awake (☕ top-right). Process-local IOPM assertion.
	caffeine *caffeine.Manager
	// lastCaffeineHint drives chrome dirty when the timed label ticks down.
	lastCaffeineHint string

	// Async alt-screen paste (clipboard image dump must not block Draw/Update).
	pasteBusy      atomic.Bool
	pendingPasteMu sync.Mutex
	pendingPaste   []pendingPaste
	// prevPasteChord: edge-detect Meta/Ctrl+V. IsKeyJustPressed can miss
	// Command+letter on some macOS/GLFW frames; IsKeyPressed still goes true.
	prevPasteChord bool
}

func (u *macUI) queueBytes(tabID int) {
	u.enqueue(func() { u.drainAndParse(tabID) })
}
func (u *macUI) queueClosed(tabID int) {
	// Shell exited (e.g. user typed `exit`) — close that pane; last pane of last page quits.
	u.enqueue(func() {
		if u.tabByID(tabID) == nil && findPaneAcrossPages(u, tabID) == nil {
			return
		}
		log.Info("shell session ended — closing pane", "tab", tabID, "panes", u.paneCount())
		u.closePaneUI(tabID, false)
	})
}
func (u *macUI) isAlive() bool     { return u != nil && u.alive.Load() }
func (u *macUI) windowReady() bool { return u != nil && u.ready.Load() }

func (u *macUI) enqueue(fn func()) {
	if u == nil || fn == nil || !u.alive.Load() {
		return
	}
	select {
	case u.jobs <- fn:
	default:
		// Drop if flooded; next PTY chunk will re-post.
	}
}

func (u *macUI) activeTab() *tab {
	if p := u.activePage(); p != nil {
		return p.focused()
	}
	// Legacy fallback during init before pages are set.
	if u.active < 0 || u.active >= len(u.tabs) {
		return nil
	}
	return u.tabs[u.active]
}

func (u *macUI) activeInput() *inputBar {
	if t := u.activeTab(); t != nil {
		return &t.input
	}
	return nil
}

func (u *macUI) inputContentCols() int {
	if g := u.focusedGeom(); g != nil {
		if g.barCols > 0 {
			return g.barCols
		}
		cw := u.metricW
		if cw < 1 {
			cw = cellW
		}
		return paneInputContentCols(g.w, cw)
	}
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	w := u.width
	if w < 1 {
		w = int32(u.cols) * cw
	}
	return paneInputContentCols(w, cw)
}

func (u *macUI) syncChrome() {
	// One chrome strip entry per page (split panes share a strip tab).
	src := u.pages
	if len(src) == 0 {
		tabs := make([]chrome.Tab, len(u.tabs))
		for i, t := range u.tabs {
			if t == nil {
				continue
			}
			title := t.displayTitle()
			if title == "" {
				title = fmt.Sprintf("shell %d", i+1)
			}
			tabs[i] = chrome.Tab{
				ID: t.id, Title: title, Alive: t.alive.Load(),
				AltScreen: t.altScreen(), Busy: t.busy(),
			}
		}
		r := u.chrome.UpdateChrome(chrome.SyncTabsMsg{Tabs: tabs, Active: u.active})
		u.chrome = r.Model
		u.chrome.Width = u.cols
		u.chrome = syncCaffeineChrome(u.chrome, u.caffeine)
		return
	}
	tabs := make([]chrome.Tab, len(src))
	for i, p := range src {
		if p == nil {
			continue
		}
		title := p.title()
		if title == "" {
			title = fmt.Sprintf("shell %d", i+1)
		}
		focus := p.focused()
		alive := p.anyAlive()
		alt := false
		busy := p.anyBusy()
		if focus != nil {
			alt = focus.altScreen()
		}
		tabs[i] = chrome.Tab{
			ID: p.id, Title: title, Alive: alive,
			AltScreen: alt, Busy: busy,
		}
	}
	dirty := u.chromeDirty ||
		u.chrome.Width != u.cols ||
		u.chrome.Active != u.active ||
		len(u.chrome.Tabs) != len(tabs)
	if !dirty {
		for i := range tabs {
			prev, next := u.chrome.Tabs[i], tabs[i]
			if prev.Title != next.Title || prev.ID != next.ID ||
				prev.Alive != next.Alive || prev.AltScreen != next.AltScreen ||
				prev.Busy != next.Busy {
				dirty = true
				break
			}
		}
	}
	r := u.chrome.UpdateChrome(chrome.SyncTabsMsg{Tabs: tabs, Active: u.active})
	u.chrome = r.Model
	u.chrome.Width = u.cols
	u.chrome = syncCaffeineChrome(u.chrome, u.caffeine)
	hint := ""
	if u.caffeine != nil {
		hint = u.caffeine.StripLabel()
	}
	if hint != u.lastCaffeineHint {
		u.lastCaffeineHint = hint
		dirty = true
	}
	if dirty {
		u.chromeDirty = true
	}
}

func (u *macUI) markChromeDirty() {
	u.chromeDirty = true
	u.overlayDirty = true
	u.inputOnlyDirty = false
}

// markShellDirty forces a full shell repaint (PTY output, scroll, resize).
func (u *macUI) markShellDirty() {
	if u != nil {
		u.inputOnlyDirty = false
	}
}

// markInputDirty notes that only the Warp bar needs a refresh.
func (u *macUI) markInputDirty() {
	if u == nil {
		return
	}
	if u.chromeDirty || u.overlayDirty || u.chrome.OverlayOpen() {
		u.inputOnlyDirty = false
		return
	}
	u.inputOnlyDirty = true
}

func (u *macUI) toast(msg string) {
	if u == nil {
		return
	}
	msg = strings.TrimSpace(msg)
	prevRows := u.chrome.RowCount()
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg(msg)).Model
	dur := 2500 * time.Millisecond
	if strings.Contains(msg, "update") || strings.Contains(msg, "up to date") ||
		strings.Contains(msg, "installing") || strings.Contains(msg, "opened") {
		dur = 4 * time.Second
	}
	u.statusUntil = time.Now().Add(dur)
	u.markChromeDirty()
	// Status adds a second strip row — reflow shell so the toast isn't painted
	// over the top of the VT grid (and so clearing it restores height).
	if u.chrome.RowCount() != prevRows {
		u.chromePx = u.chromePixelHeight()
		if u.width > 0 && u.height > 0 {
			u.applyClientSize(u.width, u.height)
		}
	}
	log.Debug("toast", "msg", msg, "rows", u.chrome.RowCount())
}

// postToast queues a toast onto the ebiten UI tick (safe from goroutines).
func (u *macUI) postToast(msg string) {
	if u == nil {
		return
	}
	if u.jobs != nil {
		select {
		case u.jobs <- func() { u.toast(msg) }:
			return
		default:
		}
	}
	u.toast(msg)
}

func (u *macUI) clearToastIfDue() {
	if u.statusUntil.IsZero() || time.Now().Before(u.statusUntil) {
		return
	}
	prevRows := u.chrome.RowCount()
	u.statusUntil = time.Time{}
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg("")).Model
	u.markChromeDirty()
	if u.chrome.RowCount() != prevRows {
		u.chromePx = u.chromePixelHeight()
		if u.width > 0 && u.height > 0 {
			u.applyClientSize(u.width, u.height)
		}
	}
}

func (u *macUI) chromePixelHeight() int32 {
	rows := u.chrome.RowCount()
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	h := int32(rows) * ch
	if h < 1 {
		h = int32(tabBarFallback)
	}
	return h
}

func (u *macUI) shellPadY() int32 { return u.chromePixelHeight() }

// inputBarPixelHeight is kept as a layout signature (sum of active-page pane
// bars). Bars paint inside each leaf; shell region uses full height under chrome.
func (u *macUI) inputBarPixelHeight() int32 {
	if len(u.lastPaneLayout) > 0 {
		return u.sumActivePaneBarHeights()
	}
	if t := u.activeTab(); t != nil && t.altScreen() {
		return 0
	}
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	w := u.width
	if w < 1 {
		w = int32(u.cols) * cw
	}
	return paneInputBarPixelHeight(u.activeTab(), w, cw, ch)
}

func (u *macUI) inputBarCwd() string {
	t := u.activeTab()
	if t == nil {
		return ""
	}
	return displayPath(t.cwd)
}

func (u *macUI) appOwnsKeyboard() bool {
	t := u.activeTab()
	return t != nil && t.altScreen()
}

func (u *macUI) maybeResizeForInput() {
	if u.width > 0 && u.height > 0 {
		u.applyClientSize(u.width, u.height)
	}
}

// shellBottomY is the exclusive bottom of the shell region (client bottom).
// Input bars are painted inside each pane, not as a global strip.
func (u *macUI) shellBottomY(clientH int32) int32 {
	if clientH < u.shellPadY()+int32(cellH) {
		return clientH
	}
	return clientH
}

func (u *macUI) loop() error {
	runtime.LockOSThread()

	// Initial size/position from last session (Windows placement parity).
	w, h := u.cols*cellW+24, u.rows*cellH+48
	restorePos := false
	var rx, ry int
	if wp := u.cfg.Window; wp.Valid() && placementOnScreenMac(wp) {
		w, h = wp.Width, wp.Height
		if w < 320 {
			w = 900
		}
		if h < 200 {
			h = 560
		}
		rx, ry = wp.X, wp.Y
		restorePos = true
		u.restoreMax = wp.Maximized
		log.Info("restoring window placement", "x", wp.X, "y", wp.Y, "w", wp.Width, "h", wp.Height, "max", wp.Maximized)
	}
	u.width, u.height = int32(w), int32(h)
	u.applyClientSize(u.width, u.height)

	ebiten.SetWindowTitle(appTitle)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(w, h)
	// Set before RunGame so ebiten does not center the window itself.
	if restorePos {
		ebiten.SetWindowPosition(rx, ry)
	}
	ebiten.SetScreenClearedEveryFrame(false)
	ebiten.SetVsyncEnabled(true)
	ebiten.SetTPS(60)

	// Start first tab workers after window setup.
	u.ready.Store(true)
	if t := u.activeTab(); t != nil {
		t.startWorkers(u)
		log.Info("tab started", "id", t.id, "pid", t.sess.Pid())
	}
	if u.bridge != nil {
		if _, err := u.bridge.Start(); err != nil {
			log.Warn("mcp bridge start failed", "err", err)
		} else {
			u.publishBridgeSnapshot()
		}
	}
	if u.showSplash {
		r := u.chrome.UpdateChrome(chrome.OpenSplashMsg{})
		u.chrome = r.Model
		u.markChromeDirty()
		u.showSplash = false
	}
	// Startup intro curtain (matrix rain by default). Skipped when always-on
	// shell rain is already on (Windows beginIntro parity — no double curtain).
	u.beginIntro(false)
	u.keyRep = newKeyRepeat()

	// Startup update check: toast + confirm before install (same as Windows).
	scheduleStartupUpdateCheck(u.postToast, func(ver string) {
		if u.jobs != nil {
			select {
			case u.jobs <- func() {
				r := u.chrome.UpdateChrome(chrome.OpenConfirmUpdateMsg{Version: ver})
				u.chrome = r.Model
				u.overlayCells = nil
				u.overlayDirty = true
				u.markChromeDirty()
			}:
			default:
			}
		}
	})

	log.Info("starting ebiten window", "w", w, "h", h, "cols", u.cols, "rows", u.rows)
	err := ebiten.RunGame(u)
	u.alive.Store(false)
	if u.caffeine != nil {
		u.caffeine.Close()
	}
	u.persistNotes()
	u.ready.Store(false)
	if u.bridge != nil {
		u.bridge.Stop()
	}
	for _, t := range u.tabs {
		t.close()
	}
	if u.painter != nil {
		u.painter.close()
	}
	u.persistWindowPlacement()
	if err != nil && err != ebiten.Termination {
		return err
	}
	return nil
}

// --- ebiten.Game ---

func (u *macUI) Update() error {
	defer applog.Recover("mac.Update", false)
	if u.quit || !u.alive.Load() {
		return ebiten.Termination
	}

	// Maximize after the native window exists (Set before RunGame is a no-op).
	if u.restoreMax && !u.maxApplied {
		u.maxApplied = true
		ebiten.MaximizeWindow()
	}

	// Drain deferred jobs (PTY/MCP).
	for {
		select {
		case fn := <-u.jobs:
			if fn != nil {
				fn()
			}
		case job := <-u.mcpJobs:
			if job.notes {
				res := runNotesOnChrome(&u.chrome, job.notesReq)
				if u.chrome.NotesOpen {
					u.overlayDirty = true
					u.overlayCells = nil
				}
				u.markChromeDirty()
				if job.notesOut != nil {
					job.notesOut <- res
				}
			} else if job.workspace {
				res := runWorkspaceOnChrome(&u.chrome, job.workspaceReq)
				if u.chrome.WorkspaceOpen {
					u.overlayDirty = true
					u.overlayCells = nil
				}
				u.markChromeDirty()
				if job.workspaceOut != nil {
					job.workspaceOut <- res
				}
			} else {
				u.submitOnUIThread(job.tabID, job.line, job.done)
			}
		default:
			goto drained
		}
	}
drained:

	u.drainPendingPaste()
	// Flush Warp-bar queue once the shell has been quiet (no PTY chunk required).
	for _, t := range u.allPanes() {
		if t != nil && !t.altScreen() && t.queueLen() > 0 {
			u.tryFlushCmdQueue(t)
		}
	}

	// Track outer frame placement mid-session (survives crash better than exit-only).
	u.maybePersistWindowPlacement(false)

	// Smooth scroll ease toward wheel/key targets (all tabs; cheap).
	dt := 1.0 / 60.0
	if tps := ebiten.ActualTPS(); tps > 1 {
		dt = 1.0 / tps
	}
	for _, t := range u.tabs {
		if t != nil && t.sb != nil {
			t.sb.tickSmooth(dt)
		}
	}

	u.clearToastIfDue()
	if msg := caffeineTick(u.caffeine); msg != "" {
		u.toast(msg)
		u.markChromeDirty()
	} else if u.caffeine != nil && u.caffeine.Active() {
		// Refresh timed strip caption ("15m" → "14m") without thrashing paint.
		hint := u.caffeine.StripLabel()
		if hint != u.lastCaffeineHint {
			u.lastCaffeineHint = hint
			u.chrome = syncCaffeineChrome(u.chrome, u.caffeine)
			u.markChromeDirty()
		}
	}
	u.handleResize()
	u.handleMouse()
	u.handleKeys()
	u.handleTextInput()
	// File drops: only consume when Send-file prompt is open (see AcceptsFileDrop).
	u.pollTransferFileDrop()
	return nil
}

// pollTransferFileDrop reads ebiten.DroppedFiles only while the send prompt
// accepts drops. Ignoring drops otherwise avoids treating every window drop
// as a transfer (e.g. while using Grok in the shell).
func (u *macUI) pollTransferFileDrop() {
	if !u.chrome.AcceptsFileDrop() {
		return
	}
	fsys := ebiten.DroppedFiles()
	if fsys == nil {
		return
	}
	paths := extractDroppedPaths(fsys)
	if len(paths) == 0 {
		u.toast("drop ignored — could not read path")
		return
	}
	// Brief hover flash is unavailable on ebiten (drop-only API); highlight via drop msg.
	r := u.chrome.UpdateChrome(chrome.TransferDropPathsMsg{Paths: paths})
	u.chrome = r.Model
	u.overlayCells = nil
	u.overlayDirty = true
	u.markChromeDirty()
	u.applyChromeAction(r)
}

func (u *macUI) Draw(screen *ebiten.Image) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("draw panic", "err", fmt.Sprint(r), "stack", string(debug.Stack()))
			applog.Sync()
		}
	}()
	u.paintTo(screen)
}

func (u *macUI) Layout(outsideWidth, outsideHeight int) (int, int) {
	if outsideWidth < 2 {
		outsideWidth = 2
	}
	if outsideHeight < 2 {
		outsideHeight = 2
	}
	return outsideWidth, outsideHeight
}

func (u *macUI) handleResize() {
	w, h := ebiten.WindowSize()
	if w < 2 || h < 2 {
		return
	}
	// Soft magnetic snap to ½ / ⅓ / ¼ / ⅔ / ¾ / full of the monitor work area.
	// Small threshold so the magnet "lets go easily" when dragging past.
	if monW, monH, ok := u.monitorWorkSize(); ok {
		nw, nh := softSnapSize(w, h, monW, monH, softSnapThresholdPx)
		if nw != w || nh != h {
			ebiten.SetWindowSize(nw, nh)
			w, h = nw, nh
		}
	}
	if int32(w) == u.width && int32(h) == u.height {
		return
	}
	u.width, u.height = int32(w), int32(h)
	u.applyClientSize(u.width, u.height)
}

// monitorWorkSize is the current window's monitor size in pixels (best-effort
// work area; ebiten does not expose the menu-bar inset).
func (u *macUI) monitorWorkSize() (w, h int, ok bool) {
	mon := ebiten.Monitor()
	if mon == nil {
		mons := ebiten.AppendMonitors(nil)
		if len(mons) == 0 || mons[0] == nil {
			return 0, 0, false
		}
		mon = mons[0]
	}
	mw, mh := mon.Size()
	if mw < 100 || mh < 100 {
		return 0, 0, false
	}
	return mw, mh, true
}

// applyClientSize updates cols/rows/chrome from a client pixel size.
// Layout: [tab strip] [shell region]. Each leaf stacks [title?][VT][input bar?].
func (u *macUI) applyClientSize(w, h int32) {
	defer applog.Recover("applyClientSize", false)
	if w < 1 || h < 1 {
		return
	}
	u.width, u.height = w, h
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX int32 = 4
	cols := int((w - padX) / cw)
	if cols < 20 {
		cols = 20
	}
	if cols > maxTermCols {
		cols = maxTermCols
	}
	// Full height under chrome — per-pane bars are inside each leaf.
	// Approximate chrome strip first so rows match paint; refined after chromePx.
	shellHApprox := h - int32(chrome.TabStripRows())*ch
	if shellHApprox < ch {
		shellHApprox = ch
	}
	rowsApprox := int(shellHApprox / ch)
	if rowsApprox < 1 {
		rowsApprox = 1
	}
	if rowsApprox > maxTermRows {
		rowsApprox = maxTermRows
	}
	u.chrome.Width = cols
	u.chrome = u.chrome.UpdateChrome(tea.WindowSizeMsg{Width: cols, Height: rowsApprox}).Model
	u.markChromeDirty()
	u.chromePx = u.chromePixelHeight()
	shellH := h - u.chromePx
	if shellH < ch {
		shellH = ch
	}
	rows := int(shellH / ch)
	if rows < 1 {
		rows = 1
	}
	if rows > maxTermRows {
		rows = maxTermRows
	}
	if rows != rowsApprox {
		u.chrome = u.chrome.UpdateChrome(tea.WindowSizeMsg{Width: cols, Height: rows}).Model
	}
	u.cols = cols
	u.rows = rows

	sx, sy, sw, sh := u.shellRect(w, h)
	for _, pg := range u.pages {
		if pg == nil || pg.root == nil {
			continue
		}
		res := layoutPage(pg.root, sx, sy, sw, sh, cw, ch, pg.focusID)
		if pg == u.activePage() {
			u.lastPaneLayout = res.leaves
			u.lastSashes = res.sashes
			u.lastShell.x, u.lastShell.y = res.shellX, res.shellY
			u.lastShell.w, u.lastShell.h = res.shellW, res.shellH
			u.inputPx = u.sumActivePaneBarHeights()
		}
		for _, g := range res.leaves {
			if g.pane == nil || !g.pane.alive.Load() {
				continue
			}
			// Always resize — alt-screen TUIs (Grok, vim) must reflow on split.
			g.pane.resize(g.cols, g.rows)
		}
	}
	// No pages yet: resize flat tabs to full size (init path).
	if len(u.pages) == 0 {
		for _, t := range u.tabs {
			if t == nil || !t.alive.Load() {
				continue
			}
			t.resize(cols, rows)
		}
	}
}

func (u *macUI) sendKey(b []byte) {
	if t := u.activeTab(); t != nil {
		t.sendKey(b)
	}
}

func (u *macUI) barCols(tab *tab) int {
	cols := u.cols
	if tab != nil {
		if g := u.paneGeomFor(tab.id); g != nil && g.cols > 0 {
			return g.cols
		}
	}
	return cols
}

func (u *macUI) submitBarLine(tab *tab, line string) {
	if u == nil || tab == nil {
		return
	}
	submitBarLine(tab, line, u.barCols(tab), u.toast)
	u.publishBridgeSnapshot()
}

func (u *macUI) tryFlushCmdQueue(tab *tab) {
	if u == nil || tab == nil {
		return
	}
	if tryFlushCmdQueue(tab, u.barCols(tab), u.toast) {
		u.publishBridgeSnapshot()
		u.markShellDirty()
	}
}

func (u *macUI) drainAndParse(tabID int) {
	t := u.tabByID(tabID)
	if t == nil || t.term == nil {
		return
	}
	data := t.takeInput()
	if len(data) == 0 {
		return
	}
	data = t.echo.feed(data)
	if clean, path, ok := stripAndTakeCwd(data); ok {
		t.setCwd(path)
		data = clean
		if u.activeTab() == t && !t.altScreen() {
			u.maybeResizeForInput()
		}
	} else {
		data = clean
	}
	// Inline images: iTerm OSC 1337, suzuri OSC 7879, and path heuristics.
	{
		clean, paths, blobs := stripAndTakeImages(data)
		data = clean
		if len(paths) > 0 || len(blobs) > 0 {
			cw, ch := int(u.metricW), int(u.metricH)
			if cw < 1 {
				cw = cellW
			}
			if ch < 1 {
				ch = cellH
			}
			paneCols := u.cols
			if g := u.paneGeomFor(t.id); g != nil && g.cols > 0 {
				paneCols = g.cols
			}
			t.ingestImages(paths, blobs, cw, ch, paneCols)
		}
	}
	if len(data) == 0 {
		// Still re-arm if more buffered.
		t.inMu.Lock()
		more := len(t.inBuf) > 0
		t.inMu.Unlock()
		if more {
			t.postBytes(u)
		}
		return
	}
	// Answer Kitty keyboard / DA probes before VT parse (Grok Shift+Enter).
	t.handleHostQueries(data)
	// Unwrap OSC 8 hyperlinks so markdown link labels stay visible.
	data = vt.StripOSC8Hyperlinks(data)
	// Kitty graphics APCs (Grok image previews): strip + apply against live
	// cursor between VT segments so a=p places at the CSI-H position.
	if t.kittyGfx == nil {
		t.kittyGfx = newKittyGfx()
	}
	data = feedKittyAPCs(t.kittyGfx, data, func(b []byte) {
		_, _ = t.term.Write(b)
	}, func() (col, row int) {
		c := t.term.Cursor()
		return c.X, c.Y
	})
	if len(data) > 0 {
		_, _ = t.term.Write(data)
	}
	t.sb.noteScreen(t.term)
	if t.sb.atBottom() {
		t.sb.stickBottom()
	}
	if title := t.term.Title(); title != "" {
		// Spinner frames don't change the stripped display title — skip thrash.
		if t.applyTitle(title) && u.activeTab() == t {
			ebiten.SetWindowTitle("suzuri — " + t.title)
		}
	}
	nowAlt := t.altScreen()
	if nowAlt != t.wasAlt {
		if t.wasAlt {
			// Leaving alt-screen (clean exit or hard kill): stop mouse inject
			// and drop Kitty placements so the shell doesn't print SGR garbage.
			resetHostAfterAltApp(t.term)
			if t.kittyGfx != nil {
				t.kittyGfx.clear()
			}
			t.markShellIdle()
		}
		t.wasAlt = nowAlt
		log.Info("alt screen", "tab", t.id, "on", nowAlt)
		if u.activeTab() == t {
			u.maybeResizeForInput()
		}
	}
	// Cwd OSC already called markShellIdle; also release on quiet PTY.
	t.maybeReleaseBarAwaiting()
	u.tryFlushCmdQueue(t)
	u.publishBridgeSnapshot()
	u.markShellDirty()
	t.inMu.Lock()
	more := len(t.inBuf) > 0
	t.inMu.Unlock()
	if more {
		t.postBytes(u)
	}
}

func (u *macUI) tabByID(id int) *tab {
	for _, t := range u.tabs {
		if t != nil && t.id == id {
			return t
		}
	}
	return nil
}

func (u *macUI) enqueueMCPSubmit(tabID int, line string) error {
	if !u.alive.Load() {
		return fmt.Errorf("ui not alive")
	}
	done := make(chan error, 1)
	job := mcpJob{tabID: tabID, line: line, done: done}
	select {
	case u.mcpJobs <- job:
	default:
		return fmt.Errorf("mcp queue full")
	}
	return <-done
}

func (u *macUI) enqueueMCPNotes(req bridge.NotesRequest) bridge.NotesResult {
	if !u.alive.Load() {
		return bridge.NotesResult{OK: false, Error: "ui not alive"}
	}
	out := make(chan bridge.NotesResult, 1)
	job := mcpJob{notes: true, notesReq: req, notesOut: out}
	select {
	case u.mcpJobs <- job:
	default:
		return bridge.NotesResult{OK: false, Error: "mcp notes queue full"}
	}
	select {
	case res := <-out:
		return res
	case <-time.After(5 * time.Second):
		return bridge.NotesResult{OK: false, Error: "mcp notes timed out"}
	}
}

func (u *macUI) enqueueMCPWorkspace(req bridge.WorkspaceRequest) bridge.WorkspaceResult {
	if !u.alive.Load() {
		return bridge.WorkspaceResult{OK: false, Error: "ui not alive"}
	}
	out := make(chan bridge.WorkspaceResult, 1)
	job := mcpJob{workspace: true, workspaceReq: req, workspaceOut: out}
	select {
	case u.mcpJobs <- job:
	default:
		return bridge.WorkspaceResult{OK: false, Error: "mcp workspace queue full"}
	}
	select {
	case res := <-out:
		return res
	case <-time.After(5 * time.Second):
		return bridge.WorkspaceResult{OK: false, Error: "mcp workspace timed out"}
	}
}

func (u *macUI) submitOnUIThread(tabID int, line string, done chan error) {
	var err error
	defer func() {
		if done != nil {
			done <- err
		}
	}()
	t := u.tabByID(tabID)
	if t == nil {
		// Default to active tab when id missing.
		t = u.activeTab()
	}
	if t == nil {
		err = fmt.Errorf("no tab")
		return
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	display, payload := expandBarSubmit(line, t.shell)
	// Fold previous live output into history so this block owns the next output.
	t.sb.commitLive(t.term)
	t.sb.pushBlock(display, u.cols, t.cwd)
	if next, ok := cwdAfterCommand(t.cwd, payload); ok {
		t.setCwd(next)
	}
	t.echo.arm(payload)
	if isClearCommand(payload) {
		t.sb.pinHere()
	}
	if strings.Contains(payload, "\n") {
		payload = strings.ReplaceAll(payload, "\n", "\r")
	}
	t.sendKey([]byte(payload + "\r"))
	t.sb.stickBottom()
	u.publishBridgeSnapshot()
}

func (u *macUI) publishBridgeSnapshot() {
	if u.bridge == nil {
		return
	}
	u.bridge.Publish(u.buildBridgeSnapshot())
}

func (u *macUI) buildBridgeSnapshot() bridge.Snapshot {
	activeID := -1
	if t := u.activeTab(); t != nil {
		activeID = t.id
	}
	s := bridge.Snapshot{
		Cols:      u.cols,
		Rows:      u.rows,
		ActiveTab: activeID,
		Tabs:      make([]bridge.TabSnap, 0, len(u.tabs)),
	}
	for _, t := range u.tabs {
		s.Tabs = append(s.Tabs, u.tabSnap(t))
	}
	return s
}

func (u *macUI) tabSnap(t *tab) bridge.TabSnap {
	armed, cmd, phase := t.echo.status()
	liveText := snapshotLiveText(t.term)
	view := t.sb.view(t.term, u.rows)
	viewLines := make([]string, len(view))
	for i, row := range view {
		viewLines[i] = strings.TrimRight(string(row), " ")
	}
	var hist []bridge.HLine
	for _, hl := range t.sb.historyTail(40) {
		kind := "normal"
		switch hl.kind {
		case histBlockRule:
			kind = "rule"
		case histBlockCmd:
			kind = "cmd"
		}
		hist = append(hist, bridge.HLine{Text: hl.text, Kind: kind})
	}
	var blocks []bridge.Block
	for _, c := range t.sb.recentBlocks(12) {
		blocks = append(blocks, bridge.Block{Command: c})
	}
	return bridge.TabSnap{
		ID:        t.id,
		Title:     t.title,
		Alive:     t.alive.Load(),
		Shell:     t.shell,
		Input:     t.input.text(),
		AltScreen: t.altScreen(),
		Echo:      bridge.EchoStat{Armed: armed, Cmd: cmd, Phase: phase},
		LiveLines: trimLiveLines(liveText),
		Viewport:  viewLines,
		Blocks:    blocks,
		History:   hist,
		PtyTail:   fmt.Sprintf("%q", t.ptyTailCopy()),
	}
}

func trimLiveLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, " ")
	}
	return out
}

func (u *macUI) newTabUI(profileName string) {
	if len(u.pages) >= maxTabs {
		u.toast("tab limit")
		return
	}
	if u.paneCount() >= maxPanesTotal {
		u.toast("max panes")
		return
	}
	opts := splitOptsFromProfile(u.cfg, profileName)
	t, err := newTab(u.nextTabID, u.cols, u.rows, opts)
	if err != nil {
		u.toast("new tab failed")
		log.Error("new tab failed", "err", err)
		return
	}
	u.nextTabID++
	u.addPageWithTab(t)
	t.startWorkers(u)
	u.syncChrome()
	u.maybeResizeForInput()
	ebiten.SetWindowTitle("suzuri — " + t.title)
	u.publishBridgeSnapshot()
}

func (u *macUI) closeTabUI(id int) {
	// Prefer page id match (chrome strip).
	for i, p := range u.pages {
		if p != nil && p.id == id {
			u.closePageAt(i, true)
			return
		}
	}
	// Pane id → close whole page that contains it.
	if pi, _ := u.pageByPaneID(id); pi >= 0 {
		u.closePageAt(pi, true)
		return
	}
}

func (u *macUI) switchTab(delta int) {
	n := len(u.pages)
	if n == 0 {
		n = len(u.tabs)
	}
	if n == 0 {
		return
	}
	u.active = (u.active + delta + n) % n
	u.selecting = false
	if t := u.activeTab(); t != nil {
		t.sel.clear()
		ebiten.SetWindowTitle("suzuri — " + t.title)
	}
	u.syncChrome()
	u.maybeResizeForInput()
}

func (u *macUI) applyChromeAction(r chrome.Result) {
	switch r.Action {
	case chrome.ActionNewTab:
		u.newTabUI("")
	case chrome.ActionNewTabProfile:
		u.newTabUI(r.ProfileName)
	case chrome.ActionNewWindow:
		openNewWindow()
	case chrome.ActionCloseTab:
		if p := u.activePage(); p != nil {
			u.closePageAt(u.active, true)
		} else if t := u.activeTab(); t != nil {
			u.closeTabUI(t.id)
		}
	case chrome.ActionClosePane:
		if t := u.activeTab(); t != nil {
			u.closePaneUI(t.id, true)
		}
	case chrome.ActionSplitRight:
		u.splitActive(splitVert)
	case chrome.ActionSplitDown:
		u.splitActive(splitHoriz)
	case chrome.ActionFocusPaneLeft:
		u.focusPaneDir(0)
	case chrome.ActionFocusPaneRight:
		u.focusPaneDir(1)
	case chrome.ActionFocusPaneUp:
		u.focusPaneDir(2)
	case chrome.ActionFocusPaneDown:
		u.focusPaneDir(3)
	case chrome.ActionNextTab:
		u.switchTab(1)
	case chrome.ActionPrevTab:
		u.switchTab(-1)
	case chrome.ActionSelectTab:
		n := len(u.pages)
		if n == 0 {
			n = len(u.tabs)
		}
		if r.Index >= 0 && r.Index < n {
			u.active = r.Index
			u.selecting = false
			if t := u.activeTab(); t != nil {
				t.sel.clear()
			}
			u.syncChrome()
			u.applyClientSize(u.width, u.height)
		}
	case chrome.ActionQuit:
		u.quit = true
	case chrome.ActionOpenSettings:
		// Fresh underlay cycle each time settings opens (starts on Ambient).
		u.settingsPreviewT0 = time.Now()
		u.settingsIntroIdleUntil = time.Time{}
		u.settingsShowedIntro = false
		if r.Settings.FontFace != "" || r.Settings.FontSizePx > 0 {
			u.applyConfigLive(r.Settings)
		}
	case chrome.ActionSettingsPreview:
		u.applyConfigLive(r.Settings)
	case chrome.ActionSettingsApply:
		u.applyConfigSave(r.Settings)
	case chrome.ActionSettingsCancel:
		if !configVisualEqual(u.cfg, r.Settings) {
			u.applyConfigLive(r.Settings)
		}
		u.overlayCells = nil
		u.overlayDirty = true
		u.chromeDirty = true
	case chrome.ActionSplashDone:
		u.cfg.FirstRunDone = true
		if err := config.Save(u.cfg); err != nil {
			log.Warn("first-run flag save failed", "err", err)
		}
	case chrome.ActionZoomIn:
		u.zoomFont(+1)
	case chrome.ActionZoomOut:
		u.zoomFont(-1)
	case chrome.ActionZoomReset:
		u.zoomFontReset()
	case chrome.ActionReplayIntro:
		u.replayIntro()
	case chrome.ActionCheckUpdates:
		runUpdateCheck(updateCheckHooks{
			toast: u.postToast,
			offerUpdate: func(ver string) {
				if u.jobs != nil {
					select {
					case u.jobs <- func() {
						r := u.chrome.UpdateChrome(chrome.OpenConfirmUpdateMsg{Version: ver})
						u.chrome = r.Model
						u.overlayCells = nil
						u.overlayDirty = true
						u.markChromeDirty()
					}:
					default:
					}
				}
			},
		})
	case chrome.ActionInstallUpdate:
		applyPendingUpdate(u.postToast)
	case chrome.ActionUpdateLater:
		markUpdateLater()
		u.toast("update deferred")
	case chrome.ActionOpenRenamePane:
		u.openRenameUI(chrome.RenameTargetPane)
	case chrome.ActionOpenRenameTab:
		u.openRenameUI(chrome.RenameTargetTab)
	case chrome.ActionApplyRename:
		u.applyRename(r.RenameTarget, r.Name)
	case chrome.ActionCaffeineToggle, chrome.ActionCaffeineFor, chrome.ActionCaffeineOff:
		if msg, ok := applyCaffeineAction(u.caffeine, r.Action, r.Minutes); ok {
			if msg != "" {
				u.toast(msg)
			}
			u.markChromeDirty()
		}
	case chrome.ActionOpenTransferSend:
		r2 := u.chrome.UpdateChrome(chrome.OpenTransferPromptMsg{Mode: chrome.TransferModeSend})
		u.chrome = r2.Model
		u.overlayCells = nil
		u.overlayDirty = true
	case chrome.ActionOpenTransferReceive:
		r2 := u.chrome.UpdateChrome(chrome.OpenTransferPromptMsg{Mode: chrome.TransferModeReceive})
		u.chrome = r2.Model
		u.overlayCells = nil
		u.overlayDirty = true
	case chrome.ActionTransferStart:
		switch r.TransferMode {
		case chrome.TransferModeReceive:
			startTransferReceive(u, r.Name, u.defaultReceiveDir())
		default:
			startTransferSend(u, r.Name)
		}
	case chrome.ActionTransferCancel:
		cancelTransfer()
		u.postTransferStatus(chrome.TransferStatusMsg{
			Active:  true,
			Phase:   "stopped",
			Message: "cancelled",
		})
		u.toast("transfer cancelled")
	case chrome.ActionTransferCopyTicket:
		ticket := r.Name
		if ticket == "" {
			ticket = u.chrome.TransferTicket()
		}
		if ticket != "" {
			u.copyText(ticket)
			u.toast("ticket copied")
		}
	}
	u.syncChrome()
}

// transferHost implementation for macUI.
func (u *macUI) postTransferStatus(msg chrome.TransferStatusMsg) {
	if u == nil {
		return
	}
	fn := func() {
		r := u.chrome.UpdateChrome(msg)
		u.chrome = r.Model
		u.overlayCells = nil
		u.overlayDirty = true
		u.markChromeDirty()
	}
	if u.jobs != nil {
		select {
		case u.jobs <- fn:
			return
		default:
		}
	}
	fn()
}

func (u *macUI) copyText(s string) {
	_ = clipboard.WriteAll(s)
}

func (u *macUI) defaultReceiveDir() string {
	return defaultDownloadDir()
}

// openRenameUI seeds and opens the rename dialog for a pane or strip tab.
func (u *macUI) openRenameUI(target chrome.RenameTarget) {
	seed := ""
	switch target {
	case chrome.RenameTargetTab:
		if p := u.activePage(); p != nil {
			if p.userTitle != "" {
				seed = p.userTitle
			} else {
				seed = p.title()
			}
		} else if t := u.activeTab(); t != nil {
			seed = t.displayTitle()
		}
	default:
		if t := u.activeTab(); t != nil {
			if t.userTitle != "" {
				seed = t.userTitle
			} else {
				seed = t.displayTitle()
			}
		}
	}
	r := u.chrome.UpdateChrome(chrome.OpenRenameMsg{Target: target, Seed: seed})
	u.chrome = r.Model
	u.overlayCells = nil
	u.overlayDirty = true
}

// applyRename sets a custom pane or page title (empty clears the lock).
// Pane renames never touch page.userTitle when multi-pane (Grok/OSC only
// rename panes). Solo pages keep strip in sync with the only pane name.
func (u *macUI) applyRename(target chrome.RenameTarget, name string) {
	switch target {
	case chrome.RenameTargetTab:
		if p := u.activePage(); p != nil {
			p.setUserTitle(name)
		} else if t := u.activeTab(); t != nil {
			t.setUserTitle(name)
		}
	default:
		if t := u.activeTab(); t != nil {
			t.setUserTitle(name)
			// Solo page: keep strip in sync with the only pane name.
			if p := u.activePage(); p != nil && p.leafCount() <= 1 {
				p.setUserTitle(name)
			}
		}
	}
	u.markChromeDirty()
	u.toast("renamed")
}

// beginIntro arms the startup curtain. When shell background rain is already
// on, matrix intro is skipped (redundant with always-on rain).
func (u *macUI) beginIntro(replay bool) {
	if u == nil {
		return
	}
	now := time.Now()
	style := config.Normalize(u.cfg).Intro
	// Persistent shell rain + matrix intro would play the same effect twice.
	if style == config.IntroMatrix && u.shellMatrixOn() {
		u.matrixIntroStart = now
		u.matrixIntroSpawnEnd = now
		u.matrixIntroDone = true
		u.matrixIntroClearAt = now // watermark at full opacity immediately
		if replay {
			log.Info("replay intro skipped", "reason", "shell rain ambient on", "style", style)
		} else {
			log.Info("startup intro skipped", "reason", "shell rain ambient on", "style", style)
		}
		return
	}
	u.matrixIntroStart = now
	u.matrixIntroSpawnEnd = now.Add(matrixIntroSpawn)
	u.matrixIntroDone = false
	u.matrixIntroClearAt = time.Time{}
	if style == config.IntroNone {
		// Short delay path for 硯 fade still uses spawn end clock.
		u.matrixIntroDone = false
	}
	if replay {
		log.Info("replay intro", "style", style)
	} else {
		log.Info("startup intro", "style", style, "spawn", matrixIntroSpawn)
	}
}

// replayIntro restarts the configured startup curtain.
func (u *macUI) replayIntro() {
	u.beginIntro(true)
}

// shellMatrixOn is true when ambient is classic rain.
func (u *macUI) shellMatrixOn() bool {
	return u != nil && u.cfg.ShellAmbient == config.AmbientRain
}

// shellAmbientOn is true for any always-on underlay.
func (u *macUI) shellAmbientOn() bool {
	return u != nil && u.cfg.AmbientActive()
}

func (u *macUI) themeAmbientColors() ambientColors {
	return ambientColors{
		pr: byte(chrome.PrimR), pg: byte(chrome.PrimG), pb: byte(chrome.PrimB),
		sr: byte(chrome.SoftR), sg: byte(chrome.SoftG), sb: byte(chrome.SoftB),
		mr: byte(chrome.MuteR), mg: byte(chrome.MuteG), mb: byte(chrome.MuteB),
		tr: byte(chrome.TextR), tg: byte(chrome.TextG), tb: byte(chrome.TextB),
	}
}

// dimShellModal is true for overlays that replace the live shell (not palette/help).
func (u *macUI) dimShellModal() bool {
	if u == nil {
		return false
	}
	return u.chrome.SettingsOpen || u.chrome.ConfirmOpen || u.chrome.SplashOpen
}

func (u *macUI) matrixIntroActive() bool {
	if u == nil || u.matrixIntroStart.IsZero() || u.matrixIntroDone {
		return false
	}
	if time.Since(u.matrixIntroStart) > matrixIntroMaxTotal {
		u.finishMatrixIntro()
		return false
	}
	return true
}

func (u *macUI) finishMatrixIntro() {
	if u.matrixIntroDone {
		return
	}
	u.matrixIntroDone = true
	if u.matrixIntroClearAt.IsZero() {
		u.matrixIntroClearAt = time.Now()
	}
	log.Debug("matrix intro wind-down complete")
}

// watermarkFade is 0..1 opacity for the center 硯 during/after intro rain.
func (u *macUI) watermarkFade() float64 {
	if u == nil {
		return 1
	}
	if u.matrixIntroStart.IsZero() || u.matrixIntroSpawnEnd.IsZero() {
		return 1
	}
	const (
		afterSpawnDelay = 0.55
		fadeIn          = 1.25
	)
	fadeStart := u.matrixIntroSpawnEnd.Add(time.Duration(afterSpawnDelay * float64(time.Second)))
	now := time.Now()
	if now.Before(fadeStart) {
		return 0
	}
	t := now.Sub(fadeStart).Seconds() / fadeIn
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

func configVisualEqual(a, b config.Config) bool {
	a, b = config.Normalize(a), config.Normalize(b)
	return strings.EqualFold(a.FontFace, b.FontFace) &&
		a.FontSizePx == b.FontSizePx &&
		a.Theme == b.Theme &&
		a.ShellANSIMap == b.ShellANSIMap &&
		a.Cursor == b.Cursor &&
		strings.EqualFold(a.Intro, b.Intro) &&
		strings.EqualFold(a.ShellAmbient, b.ShellAmbient) &&
		a.ShellMatrixOpacity == b.ShellMatrixOpacity &&
		strings.EqualFold(a.ActiveProfile, b.ActiveProfile)
}

func (u *macUI) applyConfigLive(cfg config.Config) {
	cfg = config.Normalize(cfg)
	prev := u.cfg
	u.cfg = cfg
	chrome.ApplyTheme(cfg.Theme)
	SetShellANSIMap(cfg.ShellANSIMap)
	u.chrome = u.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: cfg}).Model
	u.markChromeDirty()
	// Restart settings underlay cycle when intro style changes (live preview).
	if !strings.EqualFold(prev.Intro, cfg.Intro) {
		u.settingsPreviewT0 = time.Now()
		u.settingsIntroIdleUntil = time.Time{}
	}
	fontChanged := prev.FontSizePx != cfg.FontSizePx || !strings.EqualFold(prev.FontFace, cfg.FontFace)
	if fontChanged {
		if u.painter != nil {
			u.painter.close()
		}
		u.painter = newSoftwarePainter(cfg.FontFace, cfg.FontSizePx)
		if u.painter != nil {
			u.metricW, u.metricH = int32(u.painter.cellW), int32(u.painter.cellH)
			log.Info("font applied", "face", cfg.FontFace, "px", cfg.FontSizePx,
				"cell", u.metricW, "x", u.metricH)
		}
		// Drop cached chrome cells so strip re-renders at new metrics.
		u.chromeCells = nil
		u.overlayCells = nil
		u.fb = nil
		u.tex = nil
		u.applyClientSize(u.width, u.height)
	}
}

// settingsUnderlayClock returns the animation origin for the settings preview
// loop. When idle between plays, active is false (no rings/rain for the gap).
func (u *macUI) settingsUnderlayClock(now time.Time) (t0 time.Time, active bool) {
	if u == nil {
		return now, false
	}
	if !u.settingsIntroIdleUntil.IsZero() {
		if now.Before(u.settingsIntroIdleUntil) {
			return u.settingsPreviewT0, false
		}
		// Gap elapsed — start a new full play.
		u.settingsPreviewT0 = now
		u.settingsIntroIdleUntil = time.Time{}
	}
	if u.settingsPreviewT0.IsZero() {
		u.settingsPreviewT0 = now
	}
	return u.settingsPreviewT0, true
}

// settingsUnderlayFinished marks a full intro play done and starts the 3s gap.
func (u *macUI) settingsUnderlayFinished(now time.Time) {
	if u == nil {
		return
	}
	if u.settingsIntroIdleUntil.IsZero() {
		u.settingsIntroIdleUntil = now.Add(settingsIntroGap)
	}
}

func (u *macUI) applyConfigSave(cfg config.Config) {
	cfg = config.Normalize(cfg)
	cfg.Window = u.cfg.Window
	u.applyConfigLive(cfg)
	if err := config.Save(cfg); err != nil {
		log.Error("config save failed", "err", err)
		u.toast("save failed")
		return
	}
	log.Info("config saved", "path", config.Path(), "font", cfg.FontFace, "px", cfg.FontSizePx)
	u.toast("settings saved")
}

// zoomFont steps UI font size by delta (clamped via Normalize) and persists.
func (u *macUI) zoomFont(delta int) {
	if u == nil || delta == 0 {
		return
	}
	cfg := u.cfg
	cfg.FontSizePx += delta
	cfg = config.Normalize(cfg)
	if cfg.FontSizePx == u.cfg.FontSizePx {
		u.toast(fmt.Sprintf("font %dpx (limit)", u.cfg.FontSizePx))
		return
	}
	u.applyConfigSaveQuiet(cfg)
	u.toast(fmt.Sprintf("font %dpx", cfg.FontSizePx))
}

// zoomFontReset restores the shipping default font size (14 for Gohu).
func (u *macUI) zoomFontReset() {
	if u == nil {
		return
	}
	cfg := u.cfg
	if cfg.FontSizePx == config.DefaultFontSizePx {
		u.toast(fmt.Sprintf("font %dpx (default)", config.DefaultFontSizePx))
		return
	}
	cfg.FontSizePx = config.DefaultFontSizePx
	u.applyConfigSaveQuiet(cfg)
	u.toast(fmt.Sprintf("font %dpx (reset)", cfg.FontSizePx))
}

// applyConfigSaveQuiet is applyConfigSave without the "settings saved" toast.
func (u *macUI) applyConfigSaveQuiet(cfg config.Config) {
	cfg = config.Normalize(cfg)
	cfg.Window = u.cfg.Window
	u.chrome.ApplyFontSize(cfg.FontSizePx)
	u.applyConfigLive(cfg)
	if err := config.Save(cfg); err != nil {
		log.Error("zoom save failed", "err", err)
		u.toast("save failed")
		return
	}
	log.Info("zoom saved", "px", cfg.FontSizePx)
}

// captureWindowPlacement reads outer frame size/position via ebiten.
// Position origin is the upper-left of the window's current monitor (GLFW).
func (u *macUI) captureWindowPlacement() (config.WindowPlacement, bool) {
	if u == nil || !u.ready.Load() {
		return config.WindowPlacement{}, false
	}
	// After RunGame returns the window is gone — prefer last in-loop capture.
	w, h := ebiten.WindowSize()
	if w < 2 || h < 2 {
		if u.lastPlacement.Valid() {
			return u.lastPlacement, true
		}
		return config.WindowPlacement{}, false
	}
	x, y := ebiten.WindowPosition()
	p := config.WindowPlacement{
		X:         x,
		Y:         y,
		Width:     w,
		Height:    h,
		Maximized: ebiten.IsWindowMaximized(),
	}
	if !p.Valid() {
		return config.WindowPlacement{}, false
	}
	u.lastPlacement = p
	return p, true
}

// placementOnScreenMac is a soft visibility check so we don't restore a
// frame that no longer intersects any connected display.
func placementOnScreenMac(p config.WindowPlacement) bool {
	if !p.Valid() {
		return false
	}
	// Require at least an 80×40 sliver on some monitor.
	const pad = 80
	const padY = 40
	left, top := p.X, p.Y
	right, bottom := left+p.Width, top+p.Height
	sl := left + pad
	if sl > right {
		sl = right
	}
	st := top + padY
	if st > bottom {
		st = bottom
	}
	mons := ebiten.AppendMonitors(nil)
	if len(mons) == 0 {
		// Monitors not ready yet (pre-RunGame) — trust saved coords.
		return true
	}
	// ebiten positions are relative to the window's monitor, not the virtual
	// desktop. Check against each monitor's size as if the rect lives there.
	for _, m := range mons {
		if m == nil {
			continue
		}
		mw, mh := m.Size()
		if mw < 1 || mh < 1 {
			continue
		}
		// Intersection of placement with [0,mw)×[0,mh) in that monitor's space.
		if right > 0 && bottom > 0 && left < mw && top < mh &&
			sl > 0 && st > 0 && sl < mw && st < mh {
			return true
		}
		// Also accept when the window mostly fits (e.g. negative X slightly off).
		if p.Width <= mw+pad && p.Height <= mh+pad &&
			left > -p.Width+pad && top > -p.Height+padY &&
			left < mw && top < mh {
			return true
		}
	}
	return false
}

// maybePersistWindowPlacement updates config when the outer frame changes.
// force=true logs at info (exit path).
func (u *macUI) maybePersistWindowPlacement(force bool) {
	if u == nil {
		return
	}
	p, ok := u.captureWindowPlacement()
	if !ok {
		// Exit after RunGame: fall back to last in-loop capture.
		if force && u.lastPlacement.Valid() {
			p = u.lastPlacement
			ok = true
		}
	}
	if !ok || !p.Valid() {
		return
	}
	if u.cfg.Window == p {
		return
	}
	u.cfg.Window = p
	if err := config.Save(u.cfg); err != nil {
		log.Warn("window placement save failed", "err", err)
		return
	}
	if force {
		log.Info("window placement saved", "x", p.X, "y", p.Y, "w", p.Width, "h", p.Height, "max", p.Maximized)
	} else {
		log.Debug("window placement saved", "x", p.X, "y", p.Y, "w", p.Width, "h", p.Height, "max", p.Maximized)
	}
}

func (u *macUI) persistWindowPlacement() {
	u.maybePersistWindowPlacement(true)
}

// --- input ---

func (u *macUI) handleKeys() {
	// ctrl = "primary host modifier" (Cmd or Ctrl) for shortcuts like Cmd+K.
	// realCtrl / meta / alt are explicit for text navigation.
	meta := modMeta()
	alt := modAlt()
	realCtrl := modControl()
	ctrl := realCtrl || meta
	shift := modShift()
	now := time.Now()
	if u.keyRep == nil {
		u.keyRep = newKeyRepeat()
	}

	// Meta/Ctrl shortcuts (just pressed).
	if inpututil.IsKeyJustPressed(ebiten.KeyComma) && ctrl && !shift {
		r := u.chrome.UpdateChrome(chrome.OpenSettingsMsg{Config: u.cfg})
		u.chrome = r.Model
		u.markChromeDirty()
		u.applyChromeAction(r)
		return
	}
	if ctrl && !shift && (inpututil.IsKeyJustPressed(ebiten.KeyK) || inpututil.IsKeyJustPressed(ebiten.KeyP)) {
		r := u.chrome.UpdateChrome(chrome.OpenPaletteMsg{})
		u.chrome = r.Model
		u.markChromeDirty()
		u.applyChromeAction(r)
		return
	}
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeySlash) {
		r := u.chrome.UpdateChrome(chrome.OpenHelpMsg{})
		u.chrome = r.Model
		u.markChromeDirty()
		u.overlayCells = nil
		u.overlayDirty = true
		return
	}
	// Zoom: Cmd (or Ctrl) + / - / 0 — works even while overlays are open.
	// Use Meta for macOS Command; also accept Control for muscle memory.
	zoomMod := (ebiten.IsKeyPressed(ebiten.KeyMeta) || ebiten.IsKeyPressed(ebiten.KeyControl)) && !alt
	if zoomMod && !shift {
		// Equal key is "+" when Shift held; without Shift still zoom-in (browser style).
		if inpututil.IsKeyJustPressed(ebiten.KeyEqual) || inpututil.IsKeyJustPressed(ebiten.KeyKPAdd) {
			u.zoomFont(+1)
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyMinus) || inpututil.IsKeyJustPressed(ebiten.KeyKPSubtract) {
			u.zoomFont(-1)
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.Key0) || inpututil.IsKeyJustPressed(ebiten.KeyNumpad0) {
			u.zoomFontReset()
			return
		}
	}
	// Shift+= is the physical "+" key on US keyboards (Cmd+Shift+=).
	if zoomMod && shift && inpututil.IsKeyJustPressed(ebiten.KeyEqual) {
		u.zoomFont(+1)
		return
	}

	// Ctrl+Shift+M — toggle notes (works even while notes overlay is open).
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyM) {
		r := u.chrome.UpdateChrome(chrome.ToggleNotesMsg{})
		u.chrome = r.Model
		u.overlayCells = nil
		u.overlayDirty = true
		u.markChromeDirty()
		u.persistNotesIfDirty()
		return
	}
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyT) {
		u.newTabUI("")
		return
	}
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyN) {
		openNewWindow()
		return
	}
	// Split panes (match Windows host shortcuts).
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyD) {
		u.splitActive(splitVert)
		return
	}
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyE) {
		u.splitActive(splitHoriz)
		return
	}
	// ⌘W / Ctrl+W closes the focused pane. Last pane in a multi-pane tab
	// collapses the chrome tab; last pane of the last tab arms confirm-quit
	// (see closePaneUI → closePageAt). There is no separate "close tab" chord.
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyW) {
		if t := u.activeTab(); t != nil {
			u.closePaneUI(t.id, true)
		}
		return
	}
	// Pane focus is after the overlay block: when notes/palette is open,
	// arrows stay with the dialog. ⌘⌥+arrows focus panes; bare Option is word-jump.
	if ctrl && inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		if shift {
			u.switchTab(-1)
		} else {
			u.switchTab(1)
		}
		return
	}
	// Ctrl+1..9
	nTabs := len(u.pages)
	if nTabs == 0 {
		nTabs = len(u.tabs)
	}
	for i, k := range []ebiten.Key{
		ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4, ebiten.Key5,
		ebiten.Key6, ebiten.Key7, ebiten.Key8, ebiten.Key9,
	} {
		if ctrl && inpututil.IsKeyJustPressed(k) && i < nTabs {
			u.active = i
			u.selecting = false
			if t := u.activeTab(); t != nil {
				t.sel.clear()
				ebiten.SetWindowTitle("suzuri — " + t.title)
			}
			u.syncChrome()
			u.maybeResizeForInput()
			return
		}
	}
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyC) {
		u.copySelection()
		return
	}
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		u.pasteClipboard()
		return
	}

	// Overlay owns navigation / editor keys (palette, notes, rename, transfer, …).
	if u.chrome.OverlayOpen() {
		// Notes clipboard + bank shortcuts need host clipboard (atotto).
		if u.chrome.NotesOpen && ctrl && !alt {
			if u.handleNotesHostChord(shift) {
				return
			}
		}
		// Transfer path/ticket prompt: Cmd/Ctrl+V paste (tickets are long).
		if u.chrome.TransferPromptOpen && u.pasteChordJustPressed(meta || realCtrl, shift, alt) {
			u.pasteClipboard()
			return
		}
		// Workspace compose: Cmd/Ctrl+V paste into message field.
		if u.chrome.WorkspaceOpen && u.pasteChordJustPressed(meta || realCtrl, shift, alt) {
			u.pasteClipboard()
			return
		}
		// Notes: Option/Ctrl word-jump; Cmd line ends; ⌘⌥ is host pane focus (outside).
		if u.chrome.NotesOpen {
			if u.handleNotesNavKeys(now, realCtrl, meta, alt, shift) {
				return
			}
		}
		if km := teaKeyFromEbiten(realCtrl || meta, shift, alt, u.keyRep, now); km != nil {
			r := u.chrome.UpdateChrome(*km)
			u.chrome = r.Model
			// Text-entry overlays: only dirty the floating card.
			if u.chrome.PaletteOpen || u.chrome.RenameOpen || u.chrome.NotesOpen ||
				u.chrome.WorkspaceOpen ||
				u.chrome.TransferPromptOpen || u.chrome.TransferPanelOpen {
				u.overlayDirty = true
				u.overlayCells = nil
			} else {
				u.markChromeDirty()
				u.syncChrome()
				u.chromePx = u.chromePixelHeight()
			}
			u.applyChromeAction(r)
			u.persistNotesIfDirty()
		}
		return
	}

	// ⌘⌥+arrows: focus pane (bare Option is word-jump). Ctrl+Option is a synonym.
	// After overlay so notes/palette keep their own arrow keys.
	if alt && !shift && (meta || realCtrl) {
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft) {
			u.focusPaneDir(0)
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowRight) {
			u.focusPaneDir(1)
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
			u.focusPaneDir(2)
			return
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
			u.focusPaneDir(3)
			return
		}
	}

	tab := u.activeTab()

	// Image lightbox owns Esc before alt-screen apps (and before bar clear).
	if u.modalImage != nil && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		u.modalImage = nil
		u.markShellDirty()
		return
	}

	// Alt-screen: raw PTY keys (after host shortcuts).
	if u.appOwnsKeyboard() && tab != nil {
		// Ctrl alone for interrupt so Cmd+Enter can be Super+Enter.
		// Cmd+C/V match macOS paste/copy; Ctrl+C/V still work for Windows muscle memory.
		// ⌘⌥ is pane focus (above); Option/Ctrl+arrows word-jump via encodeArrow.
		super := meta
		opt := alt // Option+arrows → CSI Alt (Grok word jump)
		// Escape: JustPressed only — never hold-repeat into the PTY.
		// Holding Esc after notes/palette dismiss used to leak ESC into Grok.
		if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
			if b := ptyKeyFromEbiten(tab.term, &tab.kitty, ebiten.KeyEscape, realCtrl, shift, opt, super); len(b) > 0 {
				if t := u.activeTab(); t != nil {
					t.sendKey(b)
				} else {
					u.sendKey(b)
				}
			}
			return
		}
		if !shift && inpututil.IsKeyJustPressed(ebiten.KeyC) {
			if realCtrl && !super {
				if !tab.sel.empty() {
					u.copySelection()
				} else {
					u.sendKey([]byte{0x03})
				}
				return
			}
			if super && !realCtrl {
				// Cmd+C: copy selection only (never send interrupt as ⌘C).
				if !tab.sel.empty() {
					u.copySelection()
				}
				return
			}
		}
		// Ctrl+V or Cmd+V → host paste (images need Suzuri's Apple Events
		// entitlement; Grok's own osascript probe fails under Hardened Runtime).
		// Edge-detect with IsKeyPressed: IsKeyJustPressed alone can miss
		// Command+letter on some macOS/GLFW frames while Control+V still fires.
		if u.pasteChordJustPressed(meta || realCtrl, shift, alt) {
			u.pasteClipboard()
			return
		}
		// Hold-to-repeat for arrows / backspace / delete (Grok text fields).
		for _, key := range specialKeys {
			if !u.keyRep.fire(key, now) {
				continue
			}
			if b := ptyKeyFromEbiten(tab.term, &tab.kitty, key, realCtrl, shift, opt, super); len(b) > 0 {
				// Prefer focused-pane write when splits (sendKey uses active).
				if t := u.activeTab(); t != nil {
					t.sendKey(b)
				} else {
					u.sendKey(b)
				}
			}
		}
		return
	}

	// Ctrl+C / Ctrl+V in bar mode.
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyC) {
		in := u.activeInput()
		if tab != nil && !tab.sel.empty() {
			u.copySelection()
		} else if in != nil && len(in.runes) > 0 {
			in.clear()
			u.maybeResizeForInput()
		} else {
			// Interrupt + drop queued bar lines so they don't fire after ^C.
			if tab != nil {
				if n := tab.clearCmdQueue(); n > 0 {
					u.toast(fmt.Sprintf("cleared %d queued", n))
				}
			}
			u.sendKey([]byte{0x03})
		}
		return
	}
	// Bar mode: same Cmd/Ctrl+V edge as alt-screen (image → path when app owns kb).
	if u.pasteChordJustPressed(ctrl, shift, alt) {
		u.pasteClipboard()
		return
	}

	in := u.activeInput()
	if in == nil || tab == nil {
		return
	}
	cols := u.inputContentCols()

	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
		if shift {
			prevRows := in.visualRows(cols)
			in.insertNewline()
			if in.visualRows(cols) != prevRows {
				u.maybeResizeForInput()
			}
			return
		}
		line := in.submit()
		u.maybeResizeForInput()
		u.submitBarLine(tab, line)
		return
	}
	// Navigation with hold-to-repeat (IsKeyJustPressed alone never auto-repeats).
	// Option/Ctrl+←/→ word jump · Cmd+←/→ line home/end · Cmd+↑/↓ buffer.
	// ⌘⌥+arrows are pane focus (handled above).
	wordMod := (alt && !meta && !realCtrl) || (realCtrl && !meta && !alt)

	// ⌘⌫ / ⌘⌦ — clear entire Warp bar (macOS "delete line" muscle memory).
	// Handled with JustPressed first so Meta+Backspace is not missed when
	// key-repeat state is odd under modifiers; also accept Delete key.
	if meta && !alt && !realCtrl && !shift {
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) ||
			inpututil.IsKeyJustPressed(ebiten.KeyDelete) ||
			u.keyRep.fire(ebiten.KeyBackspace, now) ||
			u.keyRep.fire(ebiten.KeyDelete, now) {
			if len(in.runes) > 0 || in.histIdx >= 0 {
				in.clearLine()
				u.maybeResizeForInput()
				u.markInputDirty()
			}
			return
		}
	}

	if u.keyRep.fire(ebiten.KeyArrowUp, now) || u.keyRep.fire(ebiten.KeyUp, now) {
		if meta && !alt {
			// Cmd+Up: start of buffer (macOS text field).
			in.moveDocHome()
		} else if !in.moveVisualUp(cols) {
			in.historyUp()
		}
		u.maybeResizeForInput()
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyArrowDown, now) || u.keyRep.fire(ebiten.KeyDown, now) {
		if meta && !alt {
			in.moveDocEnd()
		} else if !in.moveVisualDown(cols) {
			in.historyDown()
		}
		u.maybeResizeForInput()
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyArrowLeft, now) || u.keyRep.fire(ebiten.KeyLeft, now) {
		switch {
		case meta && !alt:
			in.moveHome() // Cmd+Left = beginning of line
		case wordMod:
			in.moveWordLeft() // Option/Ctrl+Left
		default:
			in.moveLeft()
		}
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyArrowRight, now) || u.keyRep.fire(ebiten.KeyRight, now) {
		switch {
		case meta && !alt:
			in.moveEnd()
		case wordMod:
			in.moveWordRight()
		default:
			// zsh-autosuggest: → at EOL accepts the ghost suggestion.
			if in.cursor >= len(in.runes) {
				if t := u.activeTab(); t != nil && in.acceptGhost(t.cwd) {
					u.maybeResizeForInput()
					u.markInputDirty()
					return
				}
			}
			in.moveRight()
		}
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyHome, now) {
		if meta || realCtrl {
			in.moveDocHome()
		} else {
			in.moveHome()
		}
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyEnd, now) {
		if meta || realCtrl {
			in.moveDocEnd()
		} else {
			in.moveEnd()
		}
		u.markInputDirty()
		return
	}
	if u.keyRep.fire(ebiten.KeyDelete, now) {
		if wordMod {
			in.deleteWordRight()
		} else {
			in.deleteForward()
		}
		u.maybeResizeForInput()
		u.markInputDirty()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		tab.sb.scrollBy(u.rows/2, u.rows)
		u.markShellDirty()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		tab.sb.scrollBy(-(u.rows / 2), u.rows)
		u.markShellDirty()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if u.modalImage != nil {
			u.modalImage = nil
			u.markShellDirty()
			return
		}
		if len(in.runes) > 0 || in.histIdx >= 0 {
			in.clear()
			u.maybeResizeForInput()
		}
		return
	}
	if u.keyRep.fire(ebiten.KeyBackspace, now) {
		prevRows := in.visualRows(cols)
		// Meta+Backspace already handled above; here: Option/Ctrl word, else char.
		if wordMod {
			in.deleteWordLeft()
		} else {
			in.backspace()
		}
		if in.visualRows(cols) != prevRows {
			u.maybeResizeForInput()
			u.markShellDirty()
		} else {
			u.markInputDirty()
		}
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		prevRows := in.visualRows(cols)
		if in.complete(tab.cwd, shift) {
			if in.visualRows(cols) != prevRows {
				u.maybeResizeForInput()
			}
		}
		return
	}
}

// handleNotesNavKeys handles hold-to-repeat nav + Option/Ctrl/Cmd combos for notes.
// Returns true when the event was consumed.
// Word jump is Option or Ctrl+←/→; ⌘⌥ is pane focus outside notes (not handled here).
func (u *macUI) handleNotesNavKeys(now time.Time, realCtrl, meta, alt, shift bool) bool {
	if u == nil || !u.chrome.NotesOpen {
		return false
	}
	// Word ops: Option or Ctrl (not Cmd alone).
	wordMod := (alt && !meta && !realCtrl) || (realCtrl && !meta && !alt)
	optArrow := alt && !meta && !realCtrl
	ctrlArrow := realCtrl && !meta && !alt
	fireNav := func(key ebiten.Key, altKey ebiten.Key) bool {
		return u.keyRep.fire(key, now) || (altKey != key && u.keyRep.fire(altKey, now))
	}
	// Option/Ctrl+Backspace/Delete: delete word.
	if wordMod && !shift {
		if fireNav(ebiten.KeyBackspace, ebiten.KeyBackspace) {
			m := u.chrome
			m.NotesDeleteWord(-1)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			u.persistNotesIfDirty()
			return true
		}
		if fireNav(ebiten.KeyDelete, ebiten.KeyDelete) {
			m := u.chrome
			m.NotesDeleteWord(1)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			u.persistNotesIfDirty()
			return true
		}
	}
	// Cmd+←/→ = line home/end; Option/Ctrl+←/→ = word. Plain arrows with repeat.
	if fireNav(ebiten.KeyArrowLeft, ebiten.KeyLeft) {
		var km tea.KeyMsg
		switch {
		case meta && !alt && !shift:
			km = tea.KeyMsg{Type: tea.KeyHome}
		case meta && !alt && shift:
			km = tea.KeyMsg{Type: tea.KeyShiftHome}
		case optArrow && shift:
			km = tea.KeyMsg{Type: tea.KeyShiftLeft, Alt: true}
		case optArrow:
			km = tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
		case ctrlArrow && shift:
			km = tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}
		case ctrlArrow:
			km = tea.KeyMsg{Type: tea.KeyCtrlLeft}
		case shift:
			km = tea.KeyMsg{Type: tea.KeyShiftLeft}
		default:
			km = tea.KeyMsg{Type: tea.KeyLeft}
		}
		r := u.chrome.UpdateChrome(km)
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	if fireNav(ebiten.KeyArrowRight, ebiten.KeyRight) {
		var km tea.KeyMsg
		switch {
		case meta && !alt && !shift:
			km = tea.KeyMsg{Type: tea.KeyEnd}
		case meta && !alt && shift:
			km = tea.KeyMsg{Type: tea.KeyShiftEnd}
		case optArrow && shift:
			km = tea.KeyMsg{Type: tea.KeyShiftRight, Alt: true}
		case optArrow:
			km = tea.KeyMsg{Type: tea.KeyRight, Alt: true}
		case ctrlArrow && shift:
			km = tea.KeyMsg{Type: tea.KeyCtrlShiftRight}
		case ctrlArrow:
			km = tea.KeyMsg{Type: tea.KeyCtrlRight}
		case shift:
			km = tea.KeyMsg{Type: tea.KeyShiftRight}
		default:
			km = tea.KeyMsg{Type: tea.KeyRight}
		}
		r := u.chrome.UpdateChrome(km)
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	if fireNav(ebiten.KeyArrowUp, ebiten.KeyUp) {
		var km tea.KeyMsg
		if meta && !alt {
			km = tea.KeyMsg{Type: tea.KeyCtrlHome} // doc home
		} else if shift {
			km = tea.KeyMsg{Type: tea.KeyShiftUp}
		} else {
			km = tea.KeyMsg{Type: tea.KeyUp}
		}
		r := u.chrome.UpdateChrome(km)
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	if fireNav(ebiten.KeyArrowDown, ebiten.KeyDown) {
		var km tea.KeyMsg
		if meta && !alt {
			km = tea.KeyMsg{Type: tea.KeyCtrlEnd}
		} else if shift {
			km = tea.KeyMsg{Type: tea.KeyShiftDown}
		} else {
			km = tea.KeyMsg{Type: tea.KeyDown}
		}
		r := u.chrome.UpdateChrome(km)
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	return false
}

// specialKeys are hold-to-repeat keys for alt-screen apps.
// Escape is intentionally omitted — it is sent once on JustPressed only
// so dismissing notes/palette never auto-repeats ESC into the PTY.
var specialKeys = []ebiten.Key{
	ebiten.KeyEnter, ebiten.KeyTab, ebiten.KeyBackspace,
	ebiten.KeyDelete, ebiten.KeyInsert, ebiten.KeyHome, ebiten.KeyEnd,
	ebiten.KeyPageUp, ebiten.KeyPageDown,
	ebiten.KeyArrowUp, ebiten.KeyArrowDown, ebiten.KeyArrowLeft, ebiten.KeyArrowRight,
	ebiten.KeyF1, ebiten.KeyF2, ebiten.KeyF3, ebiten.KeyF4, ebiten.KeyF5, ebiten.KeyF6,
	ebiten.KeyF7, ebiten.KeyF8, ebiten.KeyF9, ebiten.KeyF10, ebiten.KeyF11, ebiten.KeyF12,
}

func (u *macUI) handleTextInput() {
	// Printable runes from the OS IME / keyboard.
	chars := ebiten.AppendInputChars(nil)
	if len(chars) == 0 {
		return
	}
	// Cmd/Ctrl chords are shortcuts — never insert. Option (Alt) alone still
	// types special characters (e.g. Option+e accents) when not used with arrows.
	if modMeta() || modControl() {
		return
	}

	if u.chrome.OverlayOpen() {
		// Palette filter, rename, notes, workspace compose, transfer accept runes.
		// Workspace was missing here — keys never reached handleWorkspaceKey.
		if u.chrome.PaletteOpen || u.chrome.RenameOpen || u.chrome.NotesOpen ||
			u.chrome.WorkspaceOpen || u.chrome.TransferPromptOpen {
			for _, ch := range chars {
				if ch >= 32 && ch != 0x7f {
					km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
					r := u.chrome.UpdateChrome(km)
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					u.applyChromeAction(r)
				}
			}
			u.persistNotesIfDirty()
		}
		return
	}

	if u.appOwnsKeyboard() {
		for _, ch := range chars {
			if b := ptyRuneUTF8(ch); len(b) > 0 {
				u.sendKey(b)
			}
		}
		return
	}

	in := u.activeInput()
	if in == nil {
		return
	}
	prevRows := in.visualRows(u.inputContentCols())
	// Batch insert (one slice rebuild) — notes-style: avoid per-rune work.
	rs := make([]rune, 0, len(chars))
	for _, ch := range chars {
		if ch >= 32 && ch != 0x7f && unicode.IsPrint(ch) {
			rs = append(rs, ch)
		}
	}
	if len(rs) == 0 {
		return
	}
	in.insertRunes(rs)
	if in.visualRows(u.inputContentCols()) != prevRows {
		u.maybeResizeForInput()
		u.markShellDirty() // bar height change reflows shell
	} else {
		u.markInputDirty()
	}
}

// teaKeyFromEbiten maps one-shot overlay keys (palette, settings, help).
// Notes navigation with hold-repeat is handled by handleNotesNavKeys first.
// rep/now optional; when nil, only JustPressed is used.
func teaKeyFromEbiten(ctrl, shift, alt bool, rep *keyRepeat, now time.Time) *tea.KeyMsg {
	just := func(k ebiten.Key) bool {
		if rep != nil {
			return rep.fire(k, now)
		}
		return inpututil.IsKeyJustPressed(k)
	}
	left := func() bool { return just(ebiten.KeyArrowLeft) || just(ebiten.KeyLeft) }
	right := func() bool { return just(ebiten.KeyArrowRight) || just(ebiten.KeyRight) }
	up := func() bool { return just(ebiten.KeyArrowUp) || just(ebiten.KeyUp) }
	down := func() bool { return just(ebiten.KeyArrowDown) || just(ebiten.KeyDown) }

	// Option+arrows / Option+Backspace for overlay KeyMsg.Alt (notes word jump).
	if !shift && !ctrl && alt {
		switch {
		case left():
			return &tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
		case right():
			return &tea.KeyMsg{Type: tea.KeyRight, Alt: true}
		case up():
			return &tea.KeyMsg{Type: tea.KeyUp, Alt: true}
		case down():
			return &tea.KeyMsg{Type: tea.KeyDown, Alt: true}
		case just(ebiten.KeyBackspace):
			return &tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}
		case just(ebiten.KeyDelete):
			return &tea.KeyMsg{Type: tea.KeyDelete, Alt: true}
		}
	}
	if shift && alt && !ctrl {
		switch {
		case left():
			return &tea.KeyMsg{Type: tea.KeyShiftLeft, Alt: true}
		case right():
			return &tea.KeyMsg{Type: tea.KeyShiftRight, Alt: true}
		}
	}
	if shift && !ctrl && !alt {
		switch {
		case left():
			return &tea.KeyMsg{Type: tea.KeyShiftLeft}
		case right():
			return &tea.KeyMsg{Type: tea.KeyShiftRight}
		case up():
			return &tea.KeyMsg{Type: tea.KeyShiftUp}
		case down():
			return &tea.KeyMsg{Type: tea.KeyShiftDown}
		}
	}
	if ctrl && !alt {
		switch {
		case left():
			if shift {
				return &tea.KeyMsg{Type: tea.KeyCtrlShiftLeft}
			}
			return &tea.KeyMsg{Type: tea.KeyCtrlLeft}
		case right():
			if shift {
				return &tea.KeyMsg{Type: tea.KeyCtrlShiftRight}
			}
			return &tea.KeyMsg{Type: tea.KeyCtrlRight}
		case just(ebiten.KeyHome):
			if shift {
				return &tea.KeyMsg{Type: tea.KeyCtrlShiftHome}
			}
			return &tea.KeyMsg{Type: tea.KeyCtrlHome}
		case just(ebiten.KeyEnd):
			if shift {
				return &tea.KeyMsg{Type: tea.KeyCtrlShiftEnd}
			}
			return &tea.KeyMsg{Type: tea.KeyCtrlEnd}
		}
	}
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyEscape):
		return &tea.KeyMsg{Type: tea.KeyEsc}
	case inpututil.IsKeyJustPressed(ebiten.KeyEnter):
		return &tea.KeyMsg{Type: tea.KeyEnter}
	case up():
		return &tea.KeyMsg{Type: tea.KeyUp}
	case down():
		return &tea.KeyMsg{Type: tea.KeyDown}
	case left():
		return &tea.KeyMsg{Type: tea.KeyLeft}
	case right():
		return &tea.KeyMsg{Type: tea.KeyRight}
	case just(ebiten.KeyHome):
		return &tea.KeyMsg{Type: tea.KeyHome}
	case just(ebiten.KeyEnd):
		return &tea.KeyMsg{Type: tea.KeyEnd}
	case just(ebiten.KeyDelete):
		return &tea.KeyMsg{Type: tea.KeyDelete}
	case inpututil.IsKeyJustPressed(ebiten.KeyPageUp):
		return &tea.KeyMsg{Type: tea.KeyPgUp}
	case inpututil.IsKeyJustPressed(ebiten.KeyPageDown):
		return &tea.KeyMsg{Type: tea.KeyPgDown}
	case inpututil.IsKeyJustPressed(ebiten.KeyTab):
		if shift {
			return &tea.KeyMsg{Type: tea.KeyShiftTab}
		}
		return &tea.KeyMsg{Type: tea.KeyTab}
	case just(ebiten.KeyBackspace):
		return &tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return nil
}

// handleNotesHostChord processes Ctrl/Cmd chords that need the host clipboard
// or explicit tea.KeyCtrl* messages. Returns true if the chord was handled.
func (u *macUI) handleNotesHostChord(shift bool) bool {
	// Undo / redo (Cmd+Z / Cmd+Shift+Z / Cmd+Y) — allow shift for redo.
	if inpututil.IsKeyJustPressed(ebiten.KeyZ) {
		m := u.chrome
		if shift {
			_ = m.NotesRedo()
		} else {
			_ = m.NotesUndo()
		}
		u.chrome = m
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	if !shift && inpututil.IsKeyJustPressed(ebiten.KeyY) {
		m := u.chrome
		_ = m.NotesRedo()
		u.chrome = m
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	if shift {
		return false
	}
	// Ctrl+C / X / V / A / N while notes is open.
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyC):
		if s := u.chrome.NotesSelectedText(); s != "" {
			_ = clipboard.WriteAll(s)
		}
		r := u.chrome.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlC})
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyX):
		if s := u.chrome.NotesSelectedText(); s != "" {
			_ = clipboard.WriteAll(s)
		}
		r := u.chrome.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlX})
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyV):
		if s, err := clipboard.ReadAll(); err == nil && s != "" {
			m := u.chrome
			m.NotesPaste(s)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			u.persistNotesIfDirty()
		}
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyA):
		r := u.chrome.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlA})
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		return true
	case inpututil.IsKeyJustPressed(ebiten.KeyN):
		// New note (list or editor) — not Ctrl+Shift+N (new window).
		r := u.chrome.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlN})
		u.chrome = r.Model
		u.overlayDirty = true
		u.overlayCells = nil
		u.persistNotesIfDirty()
		return true
	}
	return false
}

// persistNotesIfDirty flushes the notes bank to notes.json when dirty.
func (u *macUI) persistNotesIfDirty() {
	if u == nil || !u.chrome.NotesDirty() {
		return
	}
	u.persistNotes()
}

// persistNotes always writes the bank (exit path / force).
func (u *macUI) persistNotes() {
	if u == nil {
		return
	}
	m := u.chrome
	bank := m.NotesSnapshot()
	if err := chrome.SaveNotesBank(bank); err != nil {
		log.Warn("notes save failed", "err", err, "path", chrome.NotesPath())
		return
	}
	m.ClearNotesDirty()
	u.chrome = m
}

func (u *macUI) handleMouse() {
	_, wheelY := ebiten.Wheel()
	if wheelY != 0 {
		mx, my := ebiten.CursorPosition()
		// Scroll the pane under the cursor (not only the focused one).
		t := u.tabUnderPoint(int32(mx), int32(my))
		if t == nil {
			t = u.activeTab()
		}
		if t != nil {
			// One notch ≈ a few lines; keep it small so pin-reveal after clear
			// is progressive (not a full-history flash).
			steps := int(wheelY * 3)
			if steps == 0 {
				if wheelY > 0 {
					steps = 1
				} else {
					steps = -1
				}
			}
			// ebiten: positive wheel is away from user → scroll history up.
			viewRows := u.rows
			if g := u.paneGeomFor(t.id); g != nil && g.rows > 0 {
				viewRows = g.rows
			}
			if t.altScreen() {
				// Full-screen apps own scroll — never host history under them.
				// Forward wheel as SGR mouse (if tracking) or arrow keys.
				cx, cy, _ := u.pixelToCellInPane(int32(mx), int32(my), t)
				if b := encodeMouseWheel(t.term, cx+1, cy+1, steps); len(b) > 0 {
					t.sendKey(b) // that pane's PTY, even if unfocused
				}
			} else {
				t.sb.scrollBy(steps, viewRows)
				u.markShellDirty()
			}
		}
	}

	mx, my := ebiten.CursorPosition()
	pressed := ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	justPressed := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	justReleased := inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft)
	rightUp := inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonRight)

	if rightUp {
		u.pasteClipboard()
		return
	}

	// Link hover + pointer cursor (even on alt-screen).
	u.updateLinkHover(mx, my)

	// Alt-screen TUIs (Grok): SGR motion for button hover (1003) / drag (1002).
	// Clicks alone are not enough — hover styles need CSI < 35;c;r M reports.
	if !u.chrome.OverlayOpen() {
		if t := u.activeTab(); t != nil && t.altScreen() {
			left := pressed || u.altMouseDown
			u.maybeSendAltMouseMotion(t, mx, my, left)
		} else {
			u.altMouseCol, u.altMouseRow = 0, 0
			// Hard-killed TUIs leave mouse mode on; disarm so we don't inject
			// SGR reports into the shell (prints as "35;c;rM…" garbage).
			if t := u.activeTab(); t != nil && t.term != nil && mouseTracking(t.term) {
				resetHostMouseModes(t.term)
			}
		}
	}

	chH := u.metricH
	if chH < 1 {
		chH = cellH
	}
	chromeH := u.chromePixelHeight()
	tabStripH := int32(chrome.TabStripRows()) * chH

	if justPressed {
		// Image lightbox: any click closes.
		if u.modalImage != nil {
			u.modalImage = nil
			u.markShellDirty()
			return
		}
		// Cmd+click (or Ctrl+click) on a link → open in browser.
		meta := ebiten.IsKeyPressed(ebiten.KeyMeta)
		ctrlOnly := ebiten.IsKeyPressed(ebiten.KeyControl) && !meta
		if (meta || ctrlOnly) && !u.chrome.OverlayOpen() {
			if url := u.linkURLAt(mx, my); url != "" {
				openURLInBrowser(url)
				u.toast("opened link")
				return
			}
		}
		// Sash drag start (multi-pane).
		if !u.chrome.OverlayOpen() {
			layouts := u.computeActiveLayout()
			if si := hitSash(u.lastSashes, int32(mx), int32(my)); si >= 0 && si < len(u.lastSashes) {
				s := u.lastSashes[si]
				u.sashDrag = &s
				return
			}
			// Click-to-focus: hitPane returns a layout index, not pane id.
			if hi := hitPane(layouts, int32(mx), int32(my)); hi >= 0 && layouts[hi].pane != nil {
				g := layouts[hi]
				// Input bar: focus only (no shell selection).
				if g.barH > 0 && int32(my) >= g.barY && int32(my) < g.barY+g.barH {
					_ = u.focusPaneByID(g.pane.id)
					return
				}
				_ = u.focusPaneByID(g.pane.id)
				// Fall through so a drag can still start selection on the new focus.
			}
		}
		// Overlay card hits (notes list/title/editor) or dismiss outside.
		if u.chrome.OverlayOpen() && int32(my) >= chromeH {
			if cellX, cellY, ok := u.overlayCellAt(int32(mx), int32(my)); ok {
				// Notes: route click into list / title / editor (start drag-select).
				if u.chrome.NotesOpen {
					n := u.notesMulti.bump(cellX, cellY, time.Now())
					r := u.chrome.UpdateChrome(chrome.NotesClickMsg{
						CellX: cellX, CellY: cellY, Cols: u.cols, ClickCount: n,
					})
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					u.notesDragging = r.StartNotesDrag
					u.persistNotesIfDirty()
					return
				}
				// Other overlays: keep open when clicking the card band.
				return
			}
			u.notesDragging = false
			u.overlayCells = nil
			u.overlayDirty = true
			r := u.chrome.UpdateChrome(chrome.DismissOverlayMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			u.applyChromeAction(r)
			u.syncChrome()
			u.persistNotesIfDirty()
			return
		}
		if int32(my) < chromeH {
			if int32(my) < tabStripH {
				if u.hitCaffeine(int32(mx)) {
					if msg, ok := applyCaffeineAction(u.caffeine, chrome.ActionCaffeineToggle, 0); ok {
						if msg != "" {
							u.toast(msg)
						}
						u.markChromeDirty()
						u.syncChrome()
					}
					return
				}
				if u.hitPlus(int32(mx)) {
					u.newTabUI("")
					return
				}
				if i := u.hitTab(int32(mx)); i >= 0 {
					u.active = i
					u.selecting = false
					if t := u.activeTab(); t != nil {
						t.sel.clear()
						ebiten.SetWindowTitle("suzuri — " + t.title)
					}
					u.syncChrome()
					u.maybeResizeForInput()
				}
			}
			return
		}
		if int32(my) >= u.shellBottomY(u.height) {
			return
		}
		tab := u.activeTab()
		if tab == nil {
			return
		}
		// Don't start shell selection on the focused pane's bar region.
		if g := u.focusedGeom(); g != nil && g.barH > 0 && int32(my) >= g.barY {
			return
		}
		// Grok / alt-screen: click path or "[Open Image]"; primary: image block.
		if u.tryOpenImageModalAt(mx, my) {
			u.markShellDirty()
			return
		}
		// Alt-screen TUIs: forward mouse clicks (buttons, lists) when tracking is on.
		if tab.altScreen() {
			if u.sendAltMouse(tab, mx, my, true) {
				u.altMouseDown = true
			}
			return
		}
		x, y, viewRows := u.pixelToCellInPane(int32(mx), int32(my), tab)
		absY := tab.sb.absLine(y, viewRows, liveExtent(tab.term))
		n := u.shellMulti.bump(x, absY, time.Now())
		applyShellMultiClick(&tab.sel, tab.sb, tab.term, x, absY, n)
		u.selecting = true
		u.mouseDown = true
		u.markShellDirty()
		return
	}

	// Sash drag.
	if pressed && u.sashDrag != nil {
		applySashDrag(*u.sashDrag, int32(mx), int32(my))
		u.computeActiveLayout()
		u.applyClientSize(u.width, u.height)
		return
	}
	if justReleased && u.sashDrag != nil {
		u.sashDrag = nil
		u.applyClientSize(u.width, u.height)
		return
	}

	// Notes drag-select.
	if pressed && u.notesDragging && u.chrome.NotesOpen {
		if cellX, cellY, ok := u.overlayCellAt(int32(mx), int32(my)); ok {
			r := u.chrome.UpdateChrome(chrome.NotesDragMsg{
				CellX: cellX, CellY: cellY, Cols: u.cols,
			})
			u.chrome = r.Model
			u.overlayDirty = true
			u.overlayCells = nil
		}
	}
	if justReleased && u.notesDragging {
		u.notesDragging = false
		u.persistNotesIfDirty()
	}
	// Alt-screen mouse release (SGR …m).
	if justReleased && u.altMouseDown {
		u.altMouseDown = false
		if t := u.activeTab(); t != nil && t.altScreen() {
			u.sendAltMouse(t, mx, my, false)
		}
	}

	if pressed && u.selecting {
		tab := u.activeTab()
		if tab != nil {
			x, y, viewRows := u.pixelToCellInPane(int32(mx), int32(my), tab)
			absY := tab.sb.absLine(y, viewRows, liveExtent(tab.term))
			tab.sel.x1, tab.sel.y1 = x, absY
		}
	}
	if justReleased && u.selecting {
		u.selecting = false
		u.mouseDown = false
	}
}

// hitTab maps an x pixel to a tab index using chrome.TabBounds (same layout as View).
func (u *macUI) hitTab(px int32) int {
	n := len(u.pages)
	if n == 0 {
		n = len(u.tabs)
	}
	if n == 0 {
		return -1
	}
	u.syncChrome()
	cellX := u.pixelToChromeCol(px)
	if cellX < 0 {
		return -1
	}
	for i, b := range u.chrome.TabBounds() {
		if cellX >= b[0] && cellX < b[1] {
			return i
		}
	}
	return -1
}

// hitPlus is true when the pixel x hits the "+" new-tab chip.
func (u *macUI) hitPlus(px int32) bool {
	u.syncChrome()
	cellX := u.pixelToChromeCol(px)
	if cellX < 0 {
		return false
	}
	b := u.chrome.PlusBounds()
	return cellX >= b[0] && cellX < b[1]
}

// hitCaffeine is true when the pixel x hits the top-right coffee chip.
func (u *macUI) hitCaffeine(px int32) bool {
	u.syncChrome()
	cellX := u.pixelToChromeCol(px)
	if cellX < 0 {
		return false
	}
	b := u.chrome.CaffeineBounds()
	return cellX >= b[0] && cellX < b[1]
}

// pixelToChromeCol maps a client-x pixel to a chrome cell column
// (matches paint padX of 4 and cell pitch).
func (u *macUI) pixelToChromeCol(px int32) int {
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	const padX int32 = 4
	cellX := int((px - padX) / cw)
	if cellX < 0 {
		return -1
	}
	return cellX
}

func (u *macUI) pixelToCell(px, py int32) (x, y int) {
	x, y, _ = u.pixelToCellInPane(px, py, u.activeTab())
	return x, y
}

// tabUnderPoint returns the leaf pane under client pixels on the active page,
// or nil when the cursor is over chrome/sash/empty space.
func (u *macUI) tabUnderPoint(px, py int32) *tab {
	if u == nil {
		return nil
	}
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		layouts = u.computeActiveLayout()
	}
	if hi := hitPane(layouts, px, py); hi >= 0 && layouts[hi].pane != nil {
		return layouts[hi].pane
	}
	// Single-pane: any point in the shell band counts as the only leaf.
	if t := u.activeTab(); t != nil && len(layouts) <= 1 {
		chromeH := u.chromePixelHeight()
		if py >= chromeH && py < u.shellBottomY(u.height) {
			return t
		}
	}
	return nil
}

// pixelToCellInPane maps client pixels to cell coords within a pane's layout.
// viewRows is the pane viewport height for scrollback absLine.
func (u *macUI) pixelToCellInPane(px, py int32, tab *tab) (x, y, viewRows int) {
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	viewRows = u.rows
	cols := u.cols
	padX := int32(4)
	padY := u.shellPadY()
	if tab != nil {
		if g := u.paneGeomFor(tab.id); g != nil {
			padX, padY = g.x, g.y
			if g.rows > 0 {
				viewRows = g.rows
			}
			if g.cols > 0 {
				cols = g.cols
			}
		}
	}
	x = int((px - padX) / cw)
	y = int((py - padY) / ch)
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if cols > 0 && x >= cols {
		x = cols - 1
	}
	if viewRows > 0 && y >= viewRows {
		y = viewRows - 1
	}
	return x, y, viewRows
}

func (u *macUI) copySelection() {
	tab := u.activeTab()
	if tab == nil || tab.sel.empty() {
		return
	}
	text := tab.sel.text(tab.sb, tab.term)
	if text == "" {
		return
	}
	_ = clipboard.WriteAll(text)
	u.toast("copied")
}

func (u *macUI) pasteClipboard() {
	// Alt-screen (Grok, …): host delivers images. osascript PNG dump is slow
	// (~300–800ms) — never block the ebiten UI thread; finish on a worker
	// and inject bracketed paste when ready.
	if u.appOwnsKeyboard() {
		if u.pasteBusy.Swap(true) {
			return // already dumping a clipboard image
		}
		go u.pasteAltScreenAsync()
		return
	}
	text, err := clipboard.ReadAll()
	if err != nil || text == "" {
		return
	}
	// Transfer send/receive prompt owns clipboard paste while open.
	if u.chrome.TransferPromptOpen {
		m := u.chrome
		m.TransferPaste(text)
		u.chrome = m
		u.overlayDirty = true
		u.overlayCells = nil
		return
	}
	// Workspace compose line.
	if u.chrome.WorkspaceOpen {
		m := u.chrome
		m.WorkspacePaste(text)
		u.chrome = m
		u.overlayDirty = true
		u.overlayCells = nil
		return
	}
	in := u.activeInput()
	if in == nil {
		return
	}
	prevRows := in.visualRows(u.inputContentCols())
	in.insertRunes([]rune(text))
	if in.visualRows(u.inputContentCols()) != prevRows {
		u.maybeResizeForInput()
		u.markShellDirty()
		return
	}
	u.markInputDirty()
}

// pasteChordJustPressed is true on the rising edge of Meta/Ctrl+V (no Shift/Alt).
// Updates prevPasteChord so a held chord does not re-fire every frame.
func (u *macUI) pasteChordJustPressed(mod, shift, alt bool) bool {
	if u == nil {
		return false
	}
	held := mod && !shift && !alt && ebiten.IsKeyPressed(ebiten.KeyV)
	just := held && !u.prevPasteChord
	// Also accept IsKeyJustPressed for the common case (and tests).
	if !just && mod && !shift && !alt && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		just = true
	}
	u.prevPasteChord = held
	return just
}

// pasteAltScreenAsync reads the pasteboard off-thread and queues PTY inject.
func (u *macUI) pasteAltScreenAsync() {
	defer u.pasteBusy.Store(false)
	if imgPath, err := readClipboardImageFile(); err == nil && imgPath != "" {
		log.Info("paste clipboard image", "path", imgPath)
		u.pendingPasteMu.Lock()
		u.pendingPaste = append(u.pendingPaste, pendingPaste{payload: bracketedPaste(imgPath), toast: "image pasted"})
		u.pendingPasteMu.Unlock()
		return
	} else if err != nil {
		log.Debug("clipboard image read failed", "err", err)
	}
	text, _ := clipboard.ReadAll()
	// No host raster: Super+V so Grok can probe image+text (dual boards,
	// browser "Copy Image" with a URL caption). Text payload is the fallback
	// when Kitty keyboard is not active.
	payload := bracketedPaste(text)
	u.pendingPasteMu.Lock()
	u.pendingPaste = append(u.pendingPaste, pendingPaste{
		payload: payload, preferSuperV: true,
	})
	u.pendingPasteMu.Unlock()
}

// drainPendingPaste injects async paste results on the UI thread.
func (u *macUI) drainPendingPaste() {
	if u == nil {
		return
	}
	u.pendingPasteMu.Lock()
	batch := u.pendingPaste
	u.pendingPaste = nil
	u.pendingPasteMu.Unlock()
	for _, p := range batch {
		if p.preferSuperV {
			if t := u.activeTab(); t != nil && t.kitty.active() {
				t.sendKey(kittyCSIU(118, kittyMods(false, false, false, true)))
				continue
			}
		}
		if len(p.payload) > 0 {
			u.sendKey(p.payload)
		}
		if p.toast != "" {
			u.toast(p.toast)
		}
	}
}

// --- paint ---

// tryPaintInputOnly re-paints only Warp bars over the previous frame when the
// shell grid is unchanged (notes-style scoping). Returns true if a full paint
// was skipped. Stays in this mode until markShellDirty / markChromeDirty.
func (u *macUI) tryPaintInputOnly(screen *ebiten.Image, tab *tab, w, h int) bool {
	if u == nil || !u.inputOnlyDirty || u.painter == nil || u.fb == nil || u.tex == nil {
		return false
	}
	if u.chromeDirty || u.overlayDirty || u.chrome.OverlayOpen() {
		return false
	}
	// Intro / dim modals need a full composite. Shell rain freezes while we
	// stay in bar-only mode (same tradeoff as notes overlay scoping) — worth
	// it so typing doesn't re-rasterize a huge idle grid every keystroke.
	if u.matrixIntroActive() || u.dimShellModal() {
		return false
	}
	if tab == nil || tab.altScreen() {
		return false
	}
	if u.fb.Bounds().Dx() != w || u.fb.Bounds().Dy() != h {
		return false
	}
	layouts := u.computeActiveLayout()
	if len(layouts) == 0 {
		sx, sy, sw, sh := u.shellRect(u.width, u.height)
		layouts = []paneGeom{{
			pane: tab, x: sx, y: sy, w: sw, h: sh,
			cols: u.cols, rows: u.rows, focused: true,
		}}
	}
	// Re-draw each leaf bar (caret alpha updates here too).
	for _, g := range layouts {
		if g.barH > 0 {
			u.paintPaneInputIntoFB(g)
		}
	}
	// Single-pane fallback if layout omitted barH.
	if len(layouts) == 1 && layouts[0].barH < 1 {
		g := layouts[0]
		shellBot := int(u.shellBottomY(u.height))
		g.barY = int32(shellBot) - u.inputBarPixelHeight()
		if g.barY < u.shellPadY() {
			g.barY = u.shellPadY()
		}
		g.barH = u.height - g.barY
		g.x = 0
		g.w = u.width
		g.barCols = u.inputContentCols()
		if g.barH > 0 {
			u.paintPaneInputIntoFB(g)
		}
	}
	u.tex.WritePixels(u.fb.Pix)
	op := &ebiten.DrawImageOptions{}
	screen.DrawImage(u.tex, op)
	return true
}

func (u *macUI) paintTo(screen *ebiten.Image) {
	tab := u.activeTab()
	if tab == nil || u.painter == nil {
		screen.Fill(color.RGBA{R: chrome.VoidR, G: chrome.VoidG, B: chrome.VoidB, A: 255})
		return
	}
	w, h := int(u.width), int(u.height)
	if w < 2 || h < 2 {
		return
	}
	if u.fb == nil || u.fb.Bounds().Dx() != w || u.fb.Bounds().Dy() != h {
		u.fb = image.NewRGBA(image.Rect(0, 0, w, h))
		u.tex = ebiten.NewImage(w, h)
		u.inputOnlyDirty = false
	}

	// Notes-style scoping for the Warp bar: when only bar text/caret changed
	// and shell matrix rain is not animating, re-paint bars over the last frame.
	if u.tryPaintInputOnly(screen, tab, w, h) {
		return
	}

	u.ensureChromeCells()
	overlay := u.ensureOverlayCells()

	layouts := u.computeActiveLayout()
	if len(layouts) == 0 {
		// Single-pane fallback geometry = full shell under chrome.
		sx, sy, sw, sh := u.shellRect(u.width, u.height)
		layouts = []paneGeom{{
			pane: tab, x: sx, y: sy, w: sw, h: sh,
			cols: u.cols, rows: u.rows, focused: true,
		}}
		u.lastPaneLayout = layouts
	}

	padY := int(u.shellPadY())
	shellBot := int(u.shellBottomY(u.height))
	cw := int(u.metricW)
	if cw < 1 {
		cw = cellW
	}
	ch := int(u.metricH)
	if ch < 1 {
		ch = cellH
	}
	shellRows := (shellBot - padY) / ch
	if shellRows < 1 {
		shellRows = u.rows
	}
	shellCols := (w - 4) / cw
	if shellCols < 1 {
		shellCols = u.cols
	}

	// Underlays: settings previews Ambient by default; Intro only when that
	// row is focused. Startup curtain is separate. Always-on ambient uses
	// ShellMatrixCells / CRT scanlines outside of dim modals.
	var rain []rainCell
	var shellRain []rainCell
	crtIntensity := 0.0
	introStyle := config.Normalize(u.cfg).Intro
	now := time.Now()
	cwPx, chPx := cw, ch
	col := u.themeAmbientColors()
	if u.chrome.SettingsOpen {
		showIntro := u.chrome.SettingsShowcaseIntro()
		if showIntro && !u.settingsShowedIntro {
			// Just focused Intro — restart a full curtain play.
			u.settingsPreviewT0 = now
			u.settingsIntroIdleUntil = time.Time{}
		}
		u.settingsShowedIntro = showIntro
		if showIntro {
			// Focused Intro row → loop the chosen startup curtain.
			t0, active := u.settingsUnderlayClock(now)
			if active {
				switch introStyle {
				case config.IntroRipple:
					rCols := shellCols / 2
					if rCols < 2 {
						rCols = 2
					}
					var drew bool
					rain, drew = rippleCells(rCols, shellRows, cwPx, chPx, t0, matrixIntroSpawn, now)
					if now.Sub(t0) > matrixIntroSpawn && !drew {
						u.settingsUnderlayFinished(now)
					}
					if now.Sub(t0) > matrixIntroMaxTotal {
						u.settingsUnderlayFinished(now)
					}
				case config.IntroInkWash:
					rain = inkWashCells(shellCols, shellRows, t0, matrixIntroSpawn, now, col)
					if now.Sub(t0) > matrixIntroSpawn && len(rain) == 0 {
						u.settingsUnderlayFinished(now)
					}
				case config.IntroCRT:
					rain = crtIntroCells(shellCols, shellRows, t0, matrixIntroSpawn, now, col)
					crtIntensity = 0.45
					if now.Sub(t0) > matrixIntroSpawn {
						u.settingsUnderlayFinished(now)
						crtIntensity = 0
					}
				case config.IntroNone:
					// Matte only (paint path fills when SettingsOpen).
				default:
					rain = matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now)
				}
			}
		} else if u.shellAmbientOn() {
			// Default settings underlay (and Ambient / Intensity fields):
			// showcase the always-on ambient style + intensity.
			t0 := u.blinkStart
			if t0.IsZero() {
				t0 = now
			}
			intensity := settingsAmbientShowcaseIntensity(u.cfg)
			switch u.cfg.ShellAmbient {
			case config.AmbientRain:
				rain = dimRainCells(
					matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now),
					intensity,
				)
			case config.AmbientCRT:
				crtIntensity = intensity * 0.9
			default:
				rain = ambientGlyphCells(u.cfg.ShellAmbient, shellCols, shellRows, t0, now, col, intensity)
			}
		}
	} else if u.matrixIntroActive() && introStyle != config.IntroNone {
		mode := matrixSpawn
		if now.After(u.matrixIntroSpawnEnd) {
			mode = matrixWindDown
		}
		switch introStyle {
		case config.IntroRipple:
			rCols := shellCols / 2
			if rCols < 2 {
				rCols = 2
			}
			var drew bool
			rain, drew = rippleCells(rCols, shellRows, cwPx, chPx, u.matrixIntroStart, matrixIntroSpawn, now)
			if mode == matrixWindDown && !drew {
				u.finishMatrixIntro()
			}
		case config.IntroInkWash:
			rain = inkWashCells(shellCols, shellRows, u.matrixIntroStart, matrixIntroSpawn, now, col)
			if mode == matrixWindDown && len(rain) == 0 {
				u.finishMatrixIntro()
			}
		case config.IntroCRT:
			rain = crtIntroCells(shellCols, shellRows, u.matrixIntroStart, matrixIntroSpawn, now, col)
			crtIntensity = 0.55
			if mode == matrixWindDown && len(rain) == 0 {
				u.finishMatrixIntro()
				crtIntensity = 0
			}
		default:
			rain = matrixRainCells(shellCols, shellRows, mode, u.matrixIntroStart, matrixIntroSpawn, now)
			if mode == matrixWindDown && len(rain) == 0 {
				u.finishMatrixIntro()
			}
		}
	} else if introStyle == config.IntroNone && u.matrixIntroActive() {
		if now.After(u.matrixIntroSpawnEnd) {
			u.finishMatrixIntro()
		}
	}
	// Always-on ambient under empty/default-bg cells (settings Ambient).
	// Freezes while input-only typing path is sticky (tryPaintInputOnly).
	if u.shellAmbientOn() && !u.matrixIntroActive() && !u.dimShellModal() {
		t0 := u.blinkStart
		if t0.IsZero() {
			t0 = now
		}
		intensity := effectiveAmbientIntensity(u.cfg, tab.altScreen())
		switch u.cfg.ShellAmbient {
		case config.AmbientRain:
			shellRain = dimRainCells(
				matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now),
				intensity,
			)
		case config.AmbientCRT:
			crtIntensity = intensity * 0.9
		default:
			shellRain = ambientGlyphCells(u.cfg.ShellAmbient, shellCols, shellRows, t0, now, col, intensity)
		}
	}

	// Paint focused pane as primary shell (watermark/intro/chrome/overlay via paintFrame).
	// Additional panes are blitted after with paintPaneGrid.
	focusGrid := tab.sb.viewCells(tab.term, u.rows)
	if g := u.focusedGeom(); g != nil && g.rows > 0 {
		focusGrid = tab.sb.viewCells(tab.term, g.rows)
	}
	if !tab.sel.empty() {
		applySelectionTint(focusGrid, tab, len(focusGrid))
	}
	// Hovered hyperlink → theme primary (Cmd/Ctrl+click opens).
	if u.hoverLinkOK {
		applyLinkHoverTint(focusGrid, u.hoverLink)
	}
	cur := tab.term.Cursor()
	curVis := tab.altScreen() && tab.term.CursorVisible()
	curAlpha := u.caretAlpha()

	// Scrollbar for focused pane only (hide on alt-screen / overlays).
	liveRows := liveExtent(tab.term)
	viewRows := u.rows
	if g := u.focusedGeom(); g != nil && g.rows > 0 {
		viewRows = g.rows
	}
	scrollTrack := !tab.altScreen() && !u.chrome.OverlayOpen() && len(layouts) <= 1
	var thumbY, thumbH int
	if scrollTrack {
		trackH := shellBot - padY - 4
		thumbY, thumbH, scrollTrack = tab.sb.Scrollbar(viewRows, liveRows, trackH)
	}

	// Global ShowInput only for single-pane; multi-pane paints per-leaf bars.
	showGlobalInput := len(layouts) <= 1 && u.inputBarPixelHeight() > 0
	// When using layout bars, shell region is full client; paintFrame input uses
	// focused geom's bar region via ShowInput=false + post paint.
	inOpts := u.inputBarPaint()
	opts := paintOpts{
		Shell:            focusGrid,
		Chrome:           u.chromeCells,
		Overlay:          overlay,
		PadY:             padY,
		ShellBot:         shellBot,
		CurX:             cur.X,
		CurY:             cur.Y,
		CurVis:           curVis,
		CurAlpha:         curAlpha,
		// Dim matte only for settings/confirm/splash — palette, help, notes,
		// rename float over the live shell (Windows dimShellModal parity).
		DimShell:         u.dimShellModal(),
		SettingsOpen:     u.chrome.SettingsOpen,
		MatrixCells:      rain,
		ShellMatrixCells: shellRain,
		CRTScanlines:     crtIntensity,
		WatermarkFade:    u.watermarkFade(),
		ScrollFrac:       tab.sb.scrollFrac(),
		ScrollThumbY:     thumbY,
		ScrollThumbH:     thumbH,
		ScrollTrack:      scrollTrack,
		ShowInput:        false, // always paint bars via layout (per-pane or focused)
		CursorStyle:      int(u.cfg.Cursor),
	}
	opts.InputPrompt = inOpts.prompt
	opts.InputLines = inOpts.lines
	opts.InputCaretRow = inOpts.caretRow
	opts.InputCaretCol = inOpts.caretCol
	opts.InputEmpty = inOpts.empty
	opts.InputHint = inOpts.hint
	opts.InputCwd = inOpts.cwd
	opts.InputGhost = inOpts.ghost
	if !tab.altScreen() && len(layouts) <= 1 {
		opts.Images = tab.sb.visibleImages(tab.term, viewRows)
	}

	// For multi-pane: don't paint the focused shell full-window — clear shell
	// and draw each leaf into its rect. Keep ShellMatrixCells so rain shows
	// through empty cells under all panes (shared backdrop).
	if len(layouts) > 1 {
		opts.Shell = nil
		opts.Images = nil
		opts.ScrollTrack = false
		opts.WatermarkFade = 0 // watermark under multi-pane is noisy
	}

	u.painter.paintFrame(u.fb, opts)

	// Grok prompt image previews (Kitty graphics APC) — over the cell grid.
	if tab.kittyGfx != nil {
		if places := tab.kittyGfx.snapshotPlacements(); len(places) > 0 {
			u.painter.paintKittyPlacements(u.fb, places, tab.kittyGfx, padY, shellBot)
		}
	}

	dimModal := u.chrome.OverlayOpen() && (u.chrome.SettingsOpen || u.chrome.ConfirmOpen || u.chrome.SplashOpen)
	if !dimModal {
		if len(layouts) > 1 {
			for _, g := range layouts {
				if g.pane == nil {
					continue
				}
				u.paintPaneIntoFB(g, curAlpha)
			}
			u.painter.paintPaneTitles(u.fb, layouts, cw, ch)
			u.painter.paintPaneSashes(u.fb, u.lastSashes, u.lastShell)
		} else if showGlobalInput || (len(layouts) == 1 && layouts[0].barH > 0) {
			// Single pane: paint input bar into leaf bar region (or full width).
			g := layouts[0]
			if g.barH < 1 {
				// Fallback full-width bar at bottom if layout omitted it.
				g.barY = int32(shellBot) - u.inputBarPixelHeight()
				if g.barY < int32(padY) {
					g.barY = int32(padY)
				}
				g.barH = u.height - g.barY
				g.x = 0
				g.w = u.width
				g.barCols = u.inputContentCols()
			}
			u.paintPaneInputIntoFB(g)
		}
		// Multi-pane always paints each bar in paintPaneIntoFB.
		if len(layouts) > 1 {
			for _, g := range layouts {
				if g.barH > 0 {
					u.paintPaneInputIntoFB(g)
				}
			}
		}
	}

	// Re-paint overlay on top of pane content (paintFrame already drew it once
	// before multi-pane grids; draw again so cards float above shells).
	if len(layouts) > 1 && len(overlay) > 0 {
		u.painter.paintOverlayOnly(u.fb, overlay, padY, shellBot)
	}

	// Notes caret (block/underline/bar) over the overlay grid.
	if u.chrome.NotesOpen && u.painter != nil && len(overlay) > 0 {
		u.paintNotesCaret(u.fb, overlay, padY, shellBot)
	}

	// Image lightbox on top of everything.
	if u.modalImage != nil {
		u.paintImageModal(u.fb)
	}

	u.tex.WritePixels(u.fb.Pix)
	op := &ebiten.DrawImageOptions{}
	screen.DrawImage(u.tex, op)
}

// overlayOriginY matches paintFrame / paintOverlayOnly placement.
func (u *macUI) overlayOriginY(padY, shellBot, overlayRows int) int {
	ch := int(u.metricH)
	if ch < 1 {
		ch = cellH
	}
	oh := overlayRows * ch
	shellH := shellBot - padY
	oy := padY + (shellH-oh)/4
	if oy+oh > shellBot {
		oy = shellBot - oh
	}
	if oy < padY {
		oy = padY
	}
	return oy
}

// overlayCellAt maps a client pixel to a cell in the floating overlay grid.
// ok is false when the click is outside the painted overlay block.
func (u *macUI) overlayCellAt(px, py int32) (cellX, cellY int, ok bool) {
	overlay := u.ensureOverlayCells()
	if len(overlay) == 0 {
		return 0, 0, false
	}
	cw, ch := int(u.metricW), int(u.metricH)
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	padY := int(u.shellPadY())
	shellBot := int(u.shellBottomY(u.height))
	oy := u.overlayOriginY(padY, shellBot, len(overlay))
	// Overlay paint uses ox=0 (full-width lipgloss-centered grid).
	if int(py) < oy || int(py) >= oy+len(overlay)*ch {
		return 0, 0, false
	}
	cellY = (int(py) - oy) / ch
	cellX = int(px) / cw
	if cellY < 0 || cellY >= len(overlay) {
		return 0, 0, false
	}
	row := overlay[cellY]
	if cellX < 0 || cellX >= len(row) {
		return 0, 0, false
	}
	// Transparent gutter: treat as outside the card so click dismisses.
	cell := row[cellX]
	empty := cell.Ch == 0 || cell.Ch == ' '
	if empty && isTransparentOverlayBG(cell.BR, cell.BG, cell.BB) {
		return 0, 0, false
	}
	return cellX, cellY, true
}

// paintNotesCaret draws the notes body/title caret using cfg.Cursor.
func (u *macUI) paintNotesCaret(dst *image.RGBA, overlay [][]cellPix, padY, shellBot int) {
	if u == nil || u.painter == nil || dst == nil || !u.chrome.NotesOpen {
		return
	}
	cols := u.cols
	if cols < 20 {
		cols = 20
	}
	cx, cy, ok := u.chrome.NotesCaretCell(cols)
	if !ok {
		return
	}
	cw, ch := int(u.metricW), int(u.metricH)
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	oy := u.overlayOriginY(padY, shellBot, len(overlay))
	// Overlay grid is painted at ox=0 (see paintFrame).
	x := cx * cw
	y := oy + cy*ch
	a := u.caretAlpha()
	if a <= 0 {
		return
	}
	style := int(u.cfg.Cursor)
	// Block: reverse-video the insertion cell so the glyph under the caret
	// stays readable (match Windows paintNotesCaret).
	if style == 0 || style == int(config.CursorBlock) {
		var cell cellPix
		have := false
		if cy >= 0 && cy < len(overlay) && cx >= 0 && cx < len(overlay[cy]) {
			cell = overlay[cy][cx]
			have = true
		}
		bgR, bgG, bgB := chrome.PanelR, chrome.PanelG, chrome.PanelB
		if have && (cell.BR != 0 || cell.BG != 0 || cell.BB != 0) {
			bgR, bgG, bgB = cell.BR, cell.BG, cell.BB
		}
		fillA := a
		if fillA < 0.92 {
			fillA = 0.92
		}
		fr, fg, fb := blendRGB(bgR, bgG, bgB, chrome.PrimR, chrome.PrimG, chrome.PrimB, fillA)
		fillRectRGBA(dst, x, y, cw, ch, fr, fg, fb)
		if have && cell.Ch != 0 && cell.Ch != ' ' {
			glR, glG, glB := chrome.OnPrimR, chrome.OnPrimG, chrome.OnPrimB
			if glR == 0 && glG == 0 && glB == 0 {
				glR, glG, glB = 12, 12, 14
			}
			u.painter.drawGlyph(dst, x, y, cell.Ch, glR, glG, glB)
		}
		return
	}
	u.painter.paintInputCaret(dst, x, y, style, a)
}

// paintPaneIntoFB draws one leaf's VT grid (+ images) into the framebuffer.
func (u *macUI) paintPaneIntoFB(g paneGeom, curAlpha float64) {
	if u.painter == nil || g.pane == nil {
		return
	}
	t := g.pane
	viewRows := g.rows
	if viewRows < 1 {
		viewRows = u.rows
	}
	grid := t.sb.viewCells(t.term, viewRows)
	if t == u.activeTab() && !t.sel.empty() {
		applySelectionTint(grid, t, viewRows)
	}
	if t == u.activeTab() && u.hoverLinkOK {
		applyLinkHoverTint(grid, u.hoverLink)
	}
	cur := t.term.Cursor()
	curVis := t.altScreen() && t.term.CursorVisible() && g.focused
	u.painter.paintPaneGrid(u.fb, grid, g, cur.X, cur.Y, curVis, curAlpha)
	if !t.altScreen() {
		vis := t.sb.visibleImages(t.term, viewRows)
		u.painter.paintPaneImages(u.fb, vis, g)
	}
}

// paintPaneInputIntoFB draws one pane's Warp bar into g.barY/g.barH.
func (u *macUI) paintPaneInputIntoFB(g paneGeom) {
	if u.painter == nil || g.pane == nil || g.barH < 1 {
		return
	}
	t := g.pane
	in := &t.input
	cols := g.barCols
	if cols < 1 {
		cols = paneInputContentCols(g.w, u.metricW)
	}
	po := paintOpts{
		InputPrompt: inputBarPrompt,
		InputHint:   chrome.InputBarPlaceholder(),
		InputEmpty:  len(in.runes) == 0,
		InputCwd:    displayPath(t.cwd),
		CursorStyle: int(u.cfg.Cursor),
		CurAlpha:    u.caretAlpha(),
	}
	if !po.InputEmpty {
		view, caretRow, caretCol := in.visibleWindow(cols, maxInputVisualRows)
		po.InputLines = view
		po.InputCaretRow = caretRow
		po.InputCaretCol = caretCol
		po.InputGhost = in.ghostSuffix(t.cwd)
	}
	// Only the focused pane shows a live caret.
	if !g.focused {
		po.CurAlpha = 0
	}
	u.painter.paintPaneInputBar(u.fb, po, g)
}

type inputBarPaint struct {
	prompt             string
	lines              []string
	caretRow, caretCol int
	empty              bool
	hint               string
	cwd                string
	ghost              string
}

func (u *macUI) inputBarPaint() inputBarPaint {
	out := inputBarPaint{
		prompt: inputBarPrompt,
		hint:   chrome.InputBarPlaceholder(),
		empty:  true,
		cwd:    u.inputBarCwd(),
	}
	in := u.activeInput()
	if in == nil {
		return out
	}
	if len(in.runes) == 0 {
		return out
	}
	out.empty = false
	cols := u.inputContentCols()
	view, caretRow, caretCol := in.visibleWindow(cols, maxInputVisualRows)
	out.lines = view
	out.caretRow = caretRow
	out.caretCol = caretCol
	if t := u.activeTab(); t != nil {
		out.ghost = in.ghostSuffix(t.cwd)
	}
	return out
}

func (u *macUI) caretAlpha() float64 {
	// Soft pulse while focused; ebiten windows are typically focused when receiving ticks.
	elapsed := time.Since(u.blinkStart).Seconds()
	period := cursorBlinkPeriod.Seconds()
	return 0.35 + 0.65*(0.5+0.5*math.Sin(2*math.Pi*elapsed/period))
}

func (u *macUI) ensureChromeCells() {
	if !u.chromeDirty && u.chromeCells != nil && u.chromeCols == u.cols {
		return
	}
	term := chrome.RenderToTerm(u.chrome, u.cols)
	u.chromeCells = termToCells(term)
	u.chromeCols = u.cols
	u.chromeDirty = false
}

func (u *macUI) ensureOverlayCells() [][]cellPix {
	if !u.chrome.OverlayOpen() {
		return nil
	}
	if !u.overlayDirty && u.overlayCells != nil {
		return u.overlayCells
	}
	term := chrome.RenderOverlayToTerm(u.chrome, u.cols)
	u.overlayCells = termToCells(term)
	u.overlayDirty = false
	return u.overlayCells
}

func termToCells(term vt10x.Terminal) [][]cellPix {
	if term == nil {
		return nil
	}
	cols, rows := term.Size()
	out := make([][]cellPix, rows)
	for y := 0; y < rows; y++ {
		row := make([]cellPix, cols)
		for x := 0; x < cols; x++ {
			row[x] = glyphToCell(term.Cell(x, y))
		}
		out[y] = row
	}
	return out
}

func applySelectionTint(grid [][]cellPix, tab *tab, viewRows int) {
	if tab == nil || tab.sel.empty() {
		return
	}
	live := liveExtent(tab.term)
	for y, row := range grid {
		absY := tab.sb.absLine(y, viewRows, live)
		for x := range row {
			if tab.sel.containsAbs(x, absY) {
				// Soft blue selection field + white ink (Windows blitGrid
				// parity). Tint-only BG left reverse-video / dark-ink cells
				// unreadable — multi-line selections looked like a solid bar
				// with the top line "missing".
				row[x].BR = 40
				row[x].BG = 70
				row[x].BB = 120
				row[x].FR = 255
				row[x].FG = 255
				row[x].FB = 255
			}
		}
		grid[y] = row
	}
}

// updateLinkHover finds an http(s)/www URL under the cursor and tracks it for paint.
func (u *macUI) updateLinkHover(mx, my int) {
	if u == nil {
		return
	}
	clear := func() {
		if u.hoverLinkOK || u.linkCursorOn {
			u.hoverLinkOK = false
			u.hoverLink = linkSpan{}
			if u.linkCursorOn {
				ebiten.SetCursorShape(ebiten.CursorShapeDefault)
				u.linkCursorOn = false
			}
			u.markShellDirty()
		}
	}
	if u.chrome.OverlayOpen() || u.selecting || u.sashDrag != nil || u.notesDragging {
		clear()
		return
	}
	tab := u.activeTab()
	if tab == nil {
		clear()
		return
	}
	// Outside shell / on input bar → no link hover.
	if g := u.focusedGeom(); g != nil && g.barH > 0 && int32(my) >= g.barY {
		clear()
		return
	}
	x, y, viewRows := u.pixelToCellInPane(int32(mx), int32(my), tab)
	if viewRows < 1 {
		viewRows = u.rows
	}
	grid := tab.sb.viewCells(tab.term, viewRows)
	spans := findLinksInGrid(grid)
	span, ok := linkAt(spans, x, y)
	if !ok {
		clear()
		return
	}
	changed := !u.hoverLinkOK || u.hoverLink.url != span.url ||
		u.hoverLink.row != span.row || u.hoverLink.x0 != span.x0 || u.hoverLink.x1 != span.x1
	u.hoverLink = span
	u.hoverLinkOK = true
	if !u.linkCursorOn {
		ebiten.SetCursorShape(ebiten.CursorShapePointer)
		u.linkCursorOn = true
	}
	if changed {
		u.markShellDirty()
	}
}

// sendAltMouse forwards a left-button press/release to an alt-screen app
// (Grok action buttons, list selection, …) when mouse tracking is enabled.
func (u *macUI) sendAltMouse(t *tab, mx, my int, press bool) bool {
	if t == nil || t.term == nil || !mouseTracking(t.term) {
		return false
	}
	cx, cy, _ := u.pixelToCellInPane(int32(mx), int32(my), t)
	col, row := cx+1, cy+1
	b := encodeMouseButton(t.term, col, row, 0, press)
	if len(b) == 0 {
		return false
	}
	u.altMouseCol, u.altMouseRow = col, row
	t.sendKey(b)
	return true
}

// maybeSendAltMouseMotion reports pointer moves for hover (1003) or drag (1002).
func (u *macUI) maybeSendAltMouseMotion(t *tab, mx, my int, leftDown bool) {
	if u == nil || t == nil || t.term == nil || !t.altScreen() {
		return
	}
	if !mouseAnyMotion(t.term) && !(mouseDragMotion(t.term) && leftDown) {
		return
	}
	if g := u.paneGeomFor(t.id); g != nil {
		if int32(mx) < g.x || int32(mx) >= g.x+g.w || int32(my) < g.y || int32(my) >= g.y+g.h {
			return
		}
		if g.barH > 0 && int32(my) >= g.barY {
			return
		}
	}
	cx, cy, _ := u.pixelToCellInPane(int32(mx), int32(my), t)
	col, row := cx+1, cy+1
	if col == u.altMouseCol && row == u.altMouseRow {
		return
	}
	b := encodeMouseMotion(t.term, col, row, leftDown)
	if len(b) == 0 {
		return
	}
	u.altMouseCol, u.altMouseRow = col, row
	t.sendKey(b)
}

// linkURLAt returns the URL under client pixels, or "".
func (u *macUI) linkURLAt(mx, my int) string {
	if u == nil || u.chrome.OverlayOpen() {
		return ""
	}
	tab := u.activeTab()
	if tab == nil {
		return ""
	}
	if g := u.focusedGeom(); g != nil && g.barH > 0 && int32(my) >= g.barY {
		return ""
	}
	x, y, viewRows := u.pixelToCellInPane(int32(mx), int32(my), tab)
	if viewRows < 1 {
		viewRows = u.rows
	}
	grid := tab.sb.viewCells(tab.term, viewRows)
	span, ok := linkAt(findLinksInGrid(grid), x, y)
	if !ok {
		return ""
	}
	return span.url
}
