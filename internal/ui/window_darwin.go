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
	tabs      []*tab
	active    int
	nextTabID int
	chrome    chrome.Model

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
	settingsPreviewT0     time.Time
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
	// Shell exited (e.g. user typed `exit`) — close that tab; last tab quits the app.
	u.enqueue(func() {
		if u.tabByID(tabID) == nil {
			return
		}
		log.Info("shell session ended — closing tab", "tab", tabID, "tabs", len(u.tabs))
		u.closeTabUI(tabID)
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
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	w := u.width
	if w < 1 {
		w = int32(u.cols) * cw
	}
	const padX int32 = 8
	promptW := int32(len([]rune(inputBarPrompt))) * cw
	cols := int((w - padX - promptW - padX) / cw)
	if cols < minInputContentWidth {
		cols = minInputContentWidth
	}
	return cols
}

func (u *macUI) syncChrome() {
	tabs := make([]chrome.Tab, len(u.tabs))
	for i, t := range u.tabs {
		title := t.title
		if title == "" {
			title = fmt.Sprintf("shell %d", i+1)
		}
		tabs[i] = chrome.Tab{
			ID:        t.id,
			Title:     title,
			Alive:     t.alive.Load(),
			AltScreen: t.altScreen(),
			Busy:      t.busy(),
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

func (u *macUI) inputBarPixelHeight() int32 {
	if t := u.activeTab(); t != nil && t.altScreen() {
		return 0
	}
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	rows := 1
	if in := u.activeInput(); in != nil {
		rows = in.visualRows(u.inputContentCols())
	}
	if rows < 1 {
		rows = 1
	}
	cwdRows := int32(0)
	if u.inputBarCwd() != "" {
		cwdRows = 1
	}
	hair, topPad, botPad := inputBarVPads(ch)
	return hair + topPad + cwdRows*ch + int32(rows)*ch + botPad
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

func (u *macUI) shellBottomY(clientH int32) int32 {
	bot := clientH - u.inputBarPixelHeight()
	if bot < u.shellPadY()+int32(cellH) {
		bot = clientH
	}
	return bot
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
	// Startup intro curtain (matrix rain by default).
	now := time.Now()
	u.matrixIntroStart = now
	u.matrixIntroSpawnEnd = now.Add(matrixIntroSpawn)
	u.matrixIntroDone = false
	u.matrixIntroClearAt = time.Time{}
	intro := config.Normalize(u.cfg).Intro
	if intro == config.IntroNone {
		// Still run a short delay so the center 硯 can fade in.
		u.matrixIntroDone = false
	}
	log.Info("startup intro", "style", intro, "spawn", matrixIntroSpawn)
	u.heldKeys = make(map[ebiten.Key]time.Time)

	log.Info("starting ebiten window", "w", w, "h", h, "cols", u.cols, "rows", u.rows)
	err := ebiten.RunGame(u)
	u.alive.Store(false)
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
	u.inputPx = u.inputBarPixelHeight()
	shellH := h - u.chromePx - u.inputPx
	if u.inputPx > 0 && shellH < ch*5 {
		shellH = h - u.chromePx - ch*2
	}
	if shellH < ch {
		shellH = ch
	}
	rows := int(shellH / ch)
	if rows < 1 {
		rows = 1
	}
	if u.inputPx > 0 && rows < 5 {
		rows = 5
	}
	if rows > maxTermRows {
		rows = maxTermRows
	}
	if rows != u.rows || cols != u.cols {
		u.cols = cols
		u.rows = rows
		tabs := append([]*tab(nil), u.tabs...)
		for _, t := range tabs {
			if t != nil {
				t.resize(cols, rows)
			}
		}
	} else {
		u.cols = cols
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
	if len(u.tabs) >= maxTabs {
		u.toast("tab limit")
		return
	}
	opts := tabOpts{}
	cfg := u.cfg
	name := profileName
	if name == "" {
		name = cfg.ActiveProfile
	}
	if p := config.FindProfile(cfg, name); p != nil {
		opts.shell = p.Shell
		opts.cwd = p.Cwd
		if p.Name != "" && p.Name != "Default" {
			opts.title = p.Name
		}
	}
	t, err := newTab(u.nextTabID, u.cols, u.rows, opts)
	if err != nil {
		u.toast("new tab failed")
		log.Error("new tab failed", "err", err)
		return
	}
	u.nextTabID++
	u.tabs = append(u.tabs, t)
	u.active = len(u.tabs) - 1
	t.startWorkers(u)
	u.syncChrome()
	u.maybeResizeForInput()
	ebiten.SetWindowTitle("suzuri — " + t.title)
	u.publishBridgeSnapshot()
}

func (u *macUI) closeTabUI(id int) {
	idx := -1
	for i, t := range u.tabs {
		if t != nil && t.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	u.tabs[idx].close()
	u.tabs = append(u.tabs[:idx], u.tabs[idx+1:]...)
	if len(u.tabs) == 0 {
		u.quit = true
		return
	}
	if u.active >= len(u.tabs) {
		u.active = len(u.tabs) - 1
	} else if u.active > idx {
		u.active--
	}
	if t := u.activeTab(); t != nil {
		ebiten.SetWindowTitle("suzuri — " + t.title)
	}
	u.syncChrome()
	u.maybeResizeForInput()
	u.publishBridgeSnapshot()
}

func (u *macUI) switchTab(delta int) {
	if len(u.tabs) == 0 {
		return
	}
	u.active = (u.active + delta + len(u.tabs)) % len(u.tabs)
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
		if t := u.activeTab(); t != nil {
			u.closeTabUI(t.id)
		}
	case chrome.ActionNextTab:
		u.switchTab(1)
	case chrome.ActionPrevTab:
		u.switchTab(-1)
	case chrome.ActionSelectTab:
		if r.Index >= 0 && r.Index < len(u.tabs) {
			u.active = r.Index
			u.selecting = false
			if t := u.activeTab(); t != nil {
				t.sel.clear()
			}
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
		runUpdateCheck(u.postToast)
	}
	u.syncChrome()
}

// replayIntro restarts the configured startup curtain.
func (u *macUI) replayIntro() {
	if u == nil {
		return
	}
	now := time.Now()
	u.matrixIntroStart = now
	u.matrixIntroSpawnEnd = now.Add(matrixIntroSpawn)
	u.matrixIntroDone = false
	u.matrixIntroClearAt = time.Time{}
	style := config.Normalize(u.cfg).Intro
	log.Info("replay intro", "style", style)
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
	if ctrl && !shift && inpututil.IsKeyJustPressed(ebiten.KeyW) {
		if t := u.activeTab(); t != nil {
			u.closeTabUI(t.id)
		}
		return
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
	for i, k := range []ebiten.Key{
		ebiten.Key1, ebiten.Key2, ebiten.Key3, ebiten.Key4, ebiten.Key5,
		ebiten.Key6, ebiten.Key7, ebiten.Key8, ebiten.Key9,
	} {
		if ctrl && inpututil.IsKeyJustPressed(k) && i < len(u.tabs) {
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

	// Overlay owns navigation keys.
	if u.chrome.OverlayOpen() {
		if km := teaKeyFromEbiten(ctrl, shift); km != nil {
			r := u.chrome.UpdateChrome(*km)
			u.chrome = r.Model
			u.markChromeDirty()
			u.applyChromeAction(r)
			u.syncChrome()
			u.chromePx = u.chromePixelHeight()
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
		if u.chrome.PaletteOpen {
			for _, ch := range chars {
				if ch >= 32 && ch != 0x7f {
					km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
					r := u.chrome.UpdateChrome(km)
					u.chrome = r.Model
					u.markChromeDirty()
					u.applyChromeAction(r)
					u.syncChrome()
				}
			}
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
		// Dismiss overlay on shell click.
		if u.chrome.OverlayOpen() && int32(my) >= chromeH {
			u.overlayCells = nil
			u.overlayDirty = true
			r := u.chrome.UpdateChrome(chrome.DismissOverlayMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			u.applyChromeAction(r)
			u.syncChrome()
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

	if pressed && u.selecting {
		tab := u.activeTab()
		if tab != nil {
			x, y := u.pixelToCell(int32(mx), int32(my))
			absY := tab.sb.absLine(y, u.rows, liveExtent(tab.term))
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
	if len(u.tabs) == 0 {
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

	grid := tab.sb.viewCells(tab.term, u.rows)
	if !tab.sel.empty() {
		applySelectionTint(grid, tab, u.rows)
	}
	cur := tab.term.Cursor()
	curVis := tab.altScreen() && tab.term.CursorVisible()
	curAlpha := u.caretAlpha()

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
	var rain []rainCell
	introStyle := config.Normalize(u.cfg).Intro
	now := time.Now()
	cwPx, chPx := cw, ch
	if u.chrome.SettingsOpen {
		// Live-preview the selected intro behind the settings card.
		t0, active := u.settingsUnderlayClock(now)
		if active {
			switch introStyle {
			case config.IntroRipple:
				// Fullwidth columns for 猫/咪 rings.
				rCols := shellCols / 2
				if rCols < 2 {
					rCols = 2
				}
				var drew bool
				rain, drew = rippleCells(rCols, shellRows, cwPx, chPx, t0, matrixIntroSpawn, now)
				// Full play done when spawn elapsed and nothing left on screen.
				if now.Sub(t0) > matrixIntroSpawn && !drew {
					u.settingsUnderlayFinished(now)
				}
				// Safety: never loop longer than intro max + gap trigger.
				if now.Sub(t0) > matrixIntroMaxTotal {
					u.settingsUnderlayFinished(now)
				}
			case config.IntroNone:
				// Quiet dim underlay only (no rain/ripple).
			default:
				// Matrix: continuous loop under settings (Windows parity).
				rain = matrixRainCells(shellCols, shellRows, matrixLoop, t0, 0, now)
			}
		}
		// idle gap: rain stays nil → dim matte only
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
		// No rain — finish after spawn so watermark can fade in.
		if now.After(u.matrixIntroSpawnEnd) {
			u.finishMatrixIntro()
		}
	}

	// Scrollbar metrics (hide on alt-screen / overlays).
	liveRows := liveExtent(tab.term)
	scrollTrack := !tab.altScreen() && !u.chrome.OverlayOpen()
	var thumbY, thumbH int
	if scrollTrack {
		trackH := shellBot - padY - 4
		thumbY, thumbH, scrollTrack = tab.sb.Scrollbar(u.rows, liveRows, trackH)
	}

	inOpts := u.inputBarPaint()
	opts := paintOpts{
		Shell:         grid,
		Chrome:        u.chromeCells,
		Overlay:       overlay,
		PadY:          padY,
		ShellBot:      shellBot,
		CurX:          cur.X,
		CurY:          cur.Y,
		CurVis:        curVis,
		CurAlpha:      curAlpha,
		DimShell:      u.chrome.OverlayOpen(),
		SettingsOpen:  u.chrome.SettingsOpen,
		MatrixCells:   rain,
		WatermarkFade: u.watermarkFade(),
		ScrollFrac:    tab.sb.scrollFrac(),
		ScrollThumbY:  thumbY,
		ScrollThumbH:  thumbH,
		ScrollTrack:   scrollTrack,
		ShowInput:     u.inputBarPixelHeight() > 0,
		CursorStyle:   int(u.cfg.Cursor),
	}
	opts.InputPrompt = inOpts.prompt
	opts.InputLines = inOpts.lines
	opts.InputCaretRow = inOpts.caretRow
	opts.InputCaretCol = inOpts.caretCol
	opts.InputEmpty = inOpts.empty
	opts.InputHint = inOpts.hint
	opts.InputCwd = inOpts.cwd
	opts.InputGhost = inOpts.ghost

	u.painter.paintFrame(u.fb, opts)

	u.tex.WritePixels(u.fb.Pix)
	op := &ebiten.DrawImageOptions{}
	screen.DrawImage(u.tex, op)
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
