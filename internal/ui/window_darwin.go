//go:build darwin

package ui

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
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
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
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
	}
	ui.chrome = ui.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: cfg}).Model
	if bank, err := chrome.LoadNotesBank(); err != nil {
		log.Warn("notes load failed; using empty bank", "err", err)
	} else {
		ui.chrome = ui.chrome.UpdateChrome(chrome.LoadNotesMsg{Bank: bank}).Model
	}
	ui.alive.Store(true)
	ui.bridge = bridge.NewHost()
	ui.bridge.BindSubmit(ui.enqueueMCPSubmit)
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

type mcpJob struct {
	tabID int
	line  string
	done  chan error
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

	// Deferred work from PTY/MCP goroutines → UI tick.
	jobs    chan func()
	mcpJobs chan mcpJob
	bridge  *bridge.Host

	painter *softwarePainter
	fb      *image.RGBA
	tex     *ebiten.Image

	// Key repeat tracking for held keys.
	heldKeys map[ebiten.Key]time.Time

	// Chrome paint cache.
	chromeDirty  bool
	chromeCols   int
	chromeCells  [][]cellPix
	overlayCells [][]cellPix
	overlayDirty bool

	// Mouse
	mouseDown bool
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
	if dirty {
		u.chromeDirty = true
	}
}

func (u *macUI) markChromeDirty() {
	u.chromeDirty = true
	u.overlayDirty = true
}

func (u *macUI) toast(msg string) {
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg(msg)).Model
	dur := 2500 * time.Millisecond
	if strings.Contains(msg, "update") || strings.Contains(msg, "up to date") ||
		strings.Contains(msg, "installing") {
		dur = 4 * time.Second
	}
	u.statusUntil = time.Now().Add(dur)
	u.markChromeDirty()
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
	u.statusUntil = time.Time{}
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg("")).Model
	u.markChromeDirty()
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

	// Initial logical size from config or defaults.
	w, h := u.cols*cellW+24, u.rows*cellH+48
	if wp := u.cfg.Window; wp.Valid() {
		w, h = wp.Width, wp.Height
		if w < 320 {
			w = 900
		}
		if h < 200 {
			h = 560
		}
	}
	u.width, u.height = int32(w), int32(h)
	u.applyClientSize(u.width, u.height)

	ebiten.SetWindowTitle(appTitle)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(w, h)
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
	u.heldKeys = make(map[ebiten.Key]time.Time)

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

	// Drain deferred jobs (PTY/MCP).
	for {
		select {
		case fn := <-u.jobs:
			if fn != nil {
				fn()
			}
		case job := <-u.mcpJobs:
			u.submitOnUIThread(job.tabID, job.line, job.done)
		default:
			goto drained
		}
	}
drained:

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
	u.handleResize()
	u.handleMouse()
	u.handleKeys()
	u.handleTextInput()
	return nil
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
	if int32(w) == u.width && int32(h) == u.height {
		return
	}
	u.width, u.height = int32(w), int32(h)
	u.applyClientSize(u.width, u.height)
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
	u.chrome.Width = cols
	u.chrome = u.chrome.UpdateChrome(tea.WindowSizeMsg{Width: cols, Height: 24}).Model
	u.markChromeDirty()
	u.chromePx = u.chromePixelHeight()
	// Full height under chrome — per-pane bars are inside each leaf.
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
			// Skip resize while alt-screen is busy (same policy as Windows ConPTY).
			if !g.pane.conPtyResizeOK() {
				continue
			}
			g.pane.resize(g.cols, g.rows)
		}
	}
	// No pages yet: resize flat tabs to full size (init path).
	if len(u.pages) == 0 {
		for _, t := range u.tabs {
			if t == nil || !t.alive.Load() {
				continue
			}
			if !t.conPtyResizeOK() {
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
	_, _ = t.term.Write(data)
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
		t.wasAlt = nowAlt
		log.Info("alt screen", "tab", t.id, "on", nowAlt)
		if u.activeTab() == t {
			u.maybeResizeForInput()
		}
	}
	u.publishBridgeSnapshot()
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
		// Fresh underlay cycle each time settings opens.
		u.settingsPreviewT0 = time.Now()
		u.settingsIntroIdleUntil = time.Time{}
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
	}
	u.syncChrome()
}

func (u *macUI) openRenameUI(target chrome.RenameTarget) {
	seed := ""
	if t := u.activeTab(); t != nil {
		if t.userTitle != "" {
			seed = t.userTitle
		} else {
			seed = t.displayTitle()
		}
	}
	r := u.chrome.UpdateChrome(chrome.OpenRenameMsg{Target: target, Seed: seed})
	u.chrome = r.Model
	u.overlayCells = nil
	u.overlayDirty = true
}

func (u *macUI) applyRename(target chrome.RenameTarget, name string) {
	switch target {
	case chrome.RenameTargetTab:
		if p := u.activePage(); p != nil {
			p.setUserTitle(name)
		} else if t := u.activeTab(); t != nil {
			t.setUserTitle(name)
		}
	default:
		// Pane rename (or fallback).
		if t := u.activeTab(); t != nil {
			t.setUserTitle(name)
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
	if style == config.IntroMatrix && u.cfg.ShellMatrix {
		u.matrixIntroStart = now
		u.matrixIntroSpawnEnd = now
		u.matrixIntroDone = true
		u.matrixIntroClearAt = now // watermark at full opacity immediately
		if replay {
			log.Info("replay intro skipped", "reason", "shell matrix on", "style", style)
		} else {
			log.Info("startup intro skipped", "reason", "shell matrix on", "style", style)
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

// shellMatrixOn is true when settings ask for always-on shell rain.
func (u *macUI) shellMatrixOn() bool {
	return u != nil && u.cfg.ShellMatrix
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

func (u *macUI) persistWindowPlacement() {
	if u == nil {
		return
	}
	w, h := int(u.width), int(u.height)
	if w < 320 || h < 200 {
		return
	}
	// ebiten does not expose window position portably — store size only.
	p := config.WindowPlacement{X: 0, Y: 0, Width: w, Height: h}
	if u.cfg.Window == p {
		return
	}
	u.cfg.Window = p
	if err := config.Save(u.cfg); err != nil {
		log.Warn("window placement save failed", "err", err)
	}
}

// --- input ---

func (u *macUI) handleKeys() {
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)
	shift := ebiten.IsKeyPressed(ebiten.KeyShift)
	alt := ebiten.IsKeyPressed(ebiten.KeyAlt)

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
	if ctrl && shift && inpututil.IsKeyJustPressed(ebiten.KeyW) {
		if t := u.activeTab(); t != nil {
			u.closePaneUI(t.id, true)
		}
		return
	}
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyW) {
		if p := u.activePage(); p != nil {
			u.closePageAt(u.active, true)
		} else if t := u.activeTab(); t != nil {
			u.closeTabUI(t.id)
		}
		return
	}
	// Alt+arrows / Ctrl+Alt+arrows: focus pane.
	if (alt || (ctrl && alt)) && !shift {
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

	// Overlay owns navigation / editor keys (palette, notes, rename, …).
	if u.chrome.OverlayOpen() {
		// Notes clipboard + bank shortcuts need host clipboard (atotto).
		if u.chrome.NotesOpen && ctrl && !alt {
			if u.handleNotesHostChord(shift) {
				return
			}
		}
		if km := teaKeyFromEbiten(ctrl, shift); km != nil {
			r := u.chrome.UpdateChrome(*km)
			u.chrome = r.Model
			// Palette / rename / notes: only dirty overlay.
			if u.chrome.PaletteOpen || u.chrome.RenameOpen || u.chrome.NotesOpen {
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

	tab := u.activeTab()

	// Alt-screen: raw PTY keys (after host shortcuts).
	if u.appOwnsKeyboard() && tab != nil {
		if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyC) {
			if !tab.sel.empty() {
				u.copySelection()
			} else {
				u.sendKey([]byte{0x03})
			}
			return
		}
		if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyV) {
			u.pasteClipboard()
			return
		}
		for _, key := range specialKeys {
			if inpututil.IsKeyJustPressed(key) {
				if b := ptyKeyFromEbiten(tab.term, key, ctrl, shift, alt); len(b) > 0 {
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
			u.sendKey([]byte{0x03})
		}
		return
	}
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyV) {
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
		display, payload := expandBarSubmit(line, tab.shell)
		if stringsTrimSpace(display) != "" {
			// Previous command's output → history, then this block header.
			tab.sb.commitLive(tab.term)
			tab.sb.pushBlock(display, u.cols, tab.cwd)
			if next, ok := cwdAfterCommand(tab.cwd, payload); ok {
				tab.setCwd(next)
			}
			tab.echo.arm(payload)
			// clear/cls: commitLive already blanked the host VT, so noteScreen
			// never sees a clear transition — pin here so history stays above.
			if isClearCommand(payload) {
				tab.sb.pinHere()
			}
		}
		if strings.Contains(payload, "\n") {
			payload = strings.ReplaceAll(payload, "\n", "\r")
		}
		u.sendKey([]byte(payload + "\r"))
		tab.sb.stickBottom()
		u.publishBridgeSnapshot()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
		if !in.moveVisualUp(cols) {
			in.historyUp()
		}
		u.maybeResizeForInput()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
		if !in.moveVisualDown(cols) {
			in.historyDown()
		}
		u.maybeResizeForInput()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft) {
		in.moveLeft()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowRight) {
		in.moveRight()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyHome) {
		in.moveHome()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnd) {
		in.moveEnd()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDelete) {
		in.deleteForward()
		u.maybeResizeForInput()
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		tab.sb.scrollBy(u.rows/2, u.rows)
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		tab.sb.scrollBy(-(u.rows / 2), u.rows)
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		if len(in.runes) > 0 || in.histIdx >= 0 {
			in.clear()
			u.maybeResizeForInput()
		}
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
		now := time.Now()
		if now.Sub(u.lastBackspace) < 20*time.Millisecond {
			return
		}
		u.lastBackspace = now
		prevRows := in.visualRows(cols)
		in.backspace()
		if in.visualRows(cols) != prevRows {
			u.maybeResizeForInput()
		}
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		prevRows := in.visualRows(cols)
		shift := ebiten.IsKeyPressed(ebiten.KeyShift)
		if in.complete(tab.cwd, shift) {
			if in.visualRows(cols) != prevRows {
				u.maybeResizeForInput()
			}
		}
		return
	}
}

var specialKeys = []ebiten.Key{
	ebiten.KeyEnter, ebiten.KeyEscape, ebiten.KeyTab, ebiten.KeyBackspace,
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
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl) || ebiten.IsKeyPressed(ebiten.KeyMeta)
	if ctrl {
		return
	}

	if u.chrome.OverlayOpen() {
		// Palette filter, rename, and notes body/title all accept runes.
		if u.chrome.PaletteOpen || u.chrome.RenameOpen || u.chrome.NotesOpen {
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
	for _, ch := range chars {
		if ch >= 32 && ch != 0x7f && unicode.IsPrint(ch) {
			in.insertRune(ch)
		}
	}
	if in.visualRows(u.inputContentCols()) != prevRows {
		u.maybeResizeForInput()
	}
}

func teaKeyFromEbiten(ctrl, shift bool) *tea.KeyMsg {
	// Shift+arrows for notes selection (and rename navigation is fine with shift).
	if shift && !ctrl {
		switch {
		case inpututil.IsKeyJustPressed(ebiten.KeyLeft):
			return &tea.KeyMsg{Type: tea.KeyShiftLeft}
		case inpututil.IsKeyJustPressed(ebiten.KeyRight):
			return &tea.KeyMsg{Type: tea.KeyShiftRight}
		case inpututil.IsKeyJustPressed(ebiten.KeyUp):
			return &tea.KeyMsg{Type: tea.KeyShiftUp}
		case inpututil.IsKeyJustPressed(ebiten.KeyDown):
			return &tea.KeyMsg{Type: tea.KeyShiftDown}
		}
	}
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyEscape):
		return &tea.KeyMsg{Type: tea.KeyEsc}
	case inpututil.IsKeyJustPressed(ebiten.KeyEnter):
		return &tea.KeyMsg{Type: tea.KeyEnter}
	case inpututil.IsKeyJustPressed(ebiten.KeyUp):
		return &tea.KeyMsg{Type: tea.KeyUp}
	case inpututil.IsKeyJustPressed(ebiten.KeyDown):
		return &tea.KeyMsg{Type: tea.KeyDown}
	case inpututil.IsKeyJustPressed(ebiten.KeyLeft):
		return &tea.KeyMsg{Type: tea.KeyLeft}
	case inpututil.IsKeyJustPressed(ebiten.KeyRight):
		return &tea.KeyMsg{Type: tea.KeyRight}
	case inpututil.IsKeyJustPressed(ebiten.KeyHome):
		return &tea.KeyMsg{Type: tea.KeyHome}
	case inpututil.IsKeyJustPressed(ebiten.KeyEnd):
		return &tea.KeyMsg{Type: tea.KeyEnd}
	case inpututil.IsKeyJustPressed(ebiten.KeyDelete):
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
	case inpututil.IsKeyJustPressed(ebiten.KeyBackspace):
		return &tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return nil
}

// handleNotesHostChord processes Ctrl/Cmd chords that need the host clipboard
// or explicit tea.KeyCtrl* messages. Returns true if the chord was handled.
func (u *macUI) handleNotesHostChord(shift bool) bool {
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
		if t := u.activeTab(); t != nil && !t.altScreen() {
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
			t.sb.scrollBy(steps, u.rows)
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

	chH := u.metricH
	if chH < 1 {
		chH = cellH
	}
	chromeH := u.chromePixelHeight()
	tabStripH := int32(chrome.TabStripRows()) * chH

	if justPressed {
		// Sash drag start (multi-pane).
		if !u.chrome.OverlayOpen() {
			if si := hitSash(u.lastSashes, int32(mx), int32(my)); si >= 0 && si < len(u.lastSashes) {
				s := u.lastSashes[si]
				u.sashDrag = &s
				return
			}
			// Click-to-focus pane.
			if id := hitPane(u.lastPaneLayout, int32(mx), int32(my)); id >= 0 {
				_ = u.focusPaneByID(id)
			}
		}
		// Overlay card hits (notes list/title/editor) or dismiss outside.
		if u.chrome.OverlayOpen() && int32(my) >= chromeH {
			if cellX, cellY, ok := u.overlayCellAt(int32(mx), int32(my)); ok {
				// Notes: route click into list / title / editor.
				if u.chrome.NotesOpen {
					r := u.chrome.UpdateChrome(chrome.NotesClickMsg{
						CellX: cellX, CellY: cellY, Cols: u.cols,
					})
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					u.persistNotesIfDirty()
					return
				}
				// Other overlays: keep open when clicking the card band.
				return
			}
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
		x, y := u.pixelToCell(int32(mx), int32(my))
		absY := tab.sb.absLine(y, u.rows, liveExtent(tab.term))
		tab.sel.active = true
		tab.sel.x0, tab.sel.y0 = x, absY
		tab.sel.x1, tab.sel.y1 = x, absY
		u.selecting = true
		u.mouseDown = true
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

	if pressed && u.selecting {
		tab := u.activeTab()
		if tab != nil {
			x, y := u.pixelToCell(int32(mx), int32(my))
			viewRows := u.rows
			if g := u.focusedGeom(); g != nil && g.rows > 0 {
				viewRows = g.rows
			}
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
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX int32 = 4
	padY := u.shellPadY()
	x = int((px - padX) / cw)
	y = int((py - padY) / ch)
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= u.cols {
		x = u.cols - 1
	}
	if y >= u.rows {
		y = u.rows - 1
	}
	return x, y
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
	text, err := clipboard.ReadAll()
	if err != nil || text == "" {
		return
	}
	if u.appOwnsKeyboard() {
		payload := strings.ReplaceAll(text, "\r\n", "\n")
		payload = strings.ReplaceAll(payload, "\n", "\r")
		u.sendKey([]byte(payload))
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
	}
}

// --- paint ---

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

	// Intro underlay: startup curtain, or settings preview of the chosen style.
	// Always-on shell rain is separate (ShellMatrixCells, under glyphs).
	var rain []rainCell
	var shellRain []rainCell
	introStyle := config.Normalize(u.cfg).Intro
	now := time.Now()
	cwPx, chPx := cw, ch
	if u.chrome.SettingsOpen {
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
			case config.IntroNone:
			default:
				rain = matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now)
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
	// Quiet looping rain under the shell while sitting open (settings Rain = On).
	// Not during intro curtain or settings/splash/confirm modals.
	if u.shellMatrixOn() && !u.matrixIntroActive() && !u.dimShellModal() {
		t0 := u.blinkStart
		if t0.IsZero() {
			t0 = now
		}
		shellRain = dimRainCells(
			matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now),
			shellMatrixIntensity,
		)
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
				// Soft blue selection.
				row[x].BR = 40
				row[x].BG = 70
				row[x].BB = 120
			}
		}
		grid[y] = row
	}
}
