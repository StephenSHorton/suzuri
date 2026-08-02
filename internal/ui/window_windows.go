//go:build windows

package ui

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/bridge"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

const (
	className = "SuzuriTerminalClass"

	// UI-thread work items (never do ConPTY/VT work from foreign threads).
	wmSuzuriBytes  = win.WM_APP + 1 // drain incoming PTY byte queue into vt10x
	wmSuzuriBlink  = win.WM_APP + 2
	wmSuzuriClosed = win.WM_APP + 3 // session read ended
	wmSuzuriMCP    = win.WM_APP + 4 // MCP bridge submit jobs
	// wmSuzuriLayoutSettle runs AFTER WM_EXITSIZEMOVE returns. Heavy layout +
	// ConPTY resize during EXITSIZEMOVE itself was hard-killing the process
	// (no Go panic — native AV on the size-move stack).
	wmSuzuriLayoutSettle = win.WM_APP + 5
	// wmSuzuriSaveFinish runs after settings Enter/save returns — GDI cache drop,
	// palette rebuild, and toast must not run on the keydown stack.
	wmSuzuriSaveFinish = win.WM_APP + 6
	// wmSuzuriOpenPalette rebuilds the bubbles list off the Ctrl+K keydown stack
	// (rebuild + first View on KEYDOWN hard-crashed for some users).
	wmSuzuriOpenPalette = win.WM_APP + 7
)

// Run opens a native Win32 window with one shell tab (more via Ctrl+Shift+T).
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
	ui := &winUI{
		cols:       cols,
		rows:       rows,
		cfg:        cfg,
		blinkStart: time.Now(),
		nextTabID:  0,
		chrome:     chrome.New(cols),
	}
	ui.chrome = ui.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: cfg}).Model
	ui.alive.Store(true)
	ui.bridge = bridge.NewHost()
	ui.mcpJobs = make(chan mcpJob, 8)
	ui.bridge.BindSubmit(ui.enqueueMCPSubmit)
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

// mcpJob is a submit request from the loopback MCP bridge (HTTP goroutine → UI thread).
type mcpJob struct {
	tabID int
	line  string
	done  chan error
}

type winUI struct {
	tabs      []*tab
	active    int
	nextTabID int
	chrome    chrome.Model // Charm UI: tabs, status, palette

	hwnd     win.HWND
	font     win.HFONT
	fontBold win.HFONT
	// CJK-capable mono fallback (MS Gothic, etc.) for 硯 / 猫 / shell JP text.
	// Primary UI fonts (Gohu, Cascadia) typically lack Han glyphs.
	cjkFont win.HFONT
	width   int32
	height  int32
	cols    int
	rows    int
	cfg     config.Config
	// last measured cell size (for hit-testing)
	metricW int32
	metricH int32
	chromePx int32 // pixel height of Charm chrome
	inputPx  int32 // pixel height of Warp-style bottom input bar

	blinkStart    time.Time
	alive         atomic.Bool
	lastBackspace time.Time // rate-limit BS so a queued KEYDOWN burst cannot wipe the line
	selecting     bool
	statusUntil   time.Time // clear toast Status after this (zero = none)
	showSplash    bool      // open first-run card after window is ready
	// Startup rain: spawn until matrixIntroSpawnEnd, then wind-down until clear.
	matrixIntroStart    time.Time
	matrixIntroSpawnEnd time.Time
	matrixIntroDone     bool      // true once wind-down drew nothing
	matrixIntroClearAt  time.Time // when rain finished — watermark fade-in origin

	// Reused double-buffer (recreated on resize) to avoid GDI thrash.
	// memOldBmp is the object that was in memDC before memBmp — must be
	// re-selected before DeleteObject(memBmp) or GDI can AV later.
	memDC     win.HDC
	memBmp    win.HBITMAP
	memOldBmp win.HGDIOBJ
	memW      int32
	memH      int32

	// User is dragging or resizing the frame (WM_ENTERSIZEMOVE … EXITSIZEMOVE).
	// During this window we must not thrash ConPTY / GDI: every WM_SIZE used to
	// resize all tabs + recreate the backbuffer, which hard-crashed mid-drag.
	inSizeMove bool
	// layoutSettlePosted coalesces deferred post-resize layout messages.
	layoutSettlePosted bool
	// saveFinishPosted / saveNeedFontLayout: deferred settings-save cleanup.
	saveFinishPosted   bool
	saveNeedFontLayout bool

	// Chrome paint cache: RenderToTerm+Lip Gloss every WM_PAINT is expensive
	// and stress-tests GDI when the window is reactivated after idle.
	chromeDirty   bool
	chromeCols    int
	chromeCells   [][]cellPix // strip [row][col]
	overlayCells  [][]cellPix
	overlayDirty  bool

	// MCP bridge: loopback HTTP for spawn-on-demand stdio MCP (see internal/bridge).
	bridge  *bridge.Host
	mcpJobs chan mcpJob
}

func (u *winUI) queueBytes(tabID int)  { postBytes(u, tabID) }
func (u *winUI) queueClosed(tabID int) { postClosed(u, tabID) }
func (u *winUI) isAlive() bool         { return u != nil && u.alive.Load() }
func (u *winUI) windowReady() bool     { return u != nil && u.hwnd != 0 }

func (u *winUI) activeTab() *tab {
	if u.active < 0 || u.active >= len(u.tabs) {
		return nil
	}
	return u.tabs[u.active]
}

// activeInput returns the Warp bar for the active tab (nil if none).
func (u *winUI) activeInput() *inputBar {
	if t := u.activeTab(); t != nil {
		return &t.input
	}
	return nil
}

// inputContentCols is the character width available for command text (excl. prompt).
func (u *winUI) inputContentCols() int {
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

func (u *winUI) syncChrome() {
	tabs := make([]chrome.Tab, len(u.tabs))
	for i, t := range u.tabs {
		title := t.title
		if title == "" {
			title = fmt.Sprintf("shell %d", i+1)
		}
		tabs[i] = chrome.Tab{ID: t.id, Title: title}
	}
	// Only invalidate the chrome cell cache when something visible changed.
	// (Calling this every paint used to force a full Lip Gloss re-render.)
	dirty := u.chromeDirty ||
		u.chrome.Width != u.cols ||
		u.chrome.Active != u.active ||
		len(u.chrome.Tabs) != len(tabs)
	if !dirty {
		for i := range tabs {
			if u.chrome.Tabs[i].Title != tabs[i].Title || u.chrome.Tabs[i].ID != tabs[i].ID {
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

func (u *winUI) markChromeDirty() {
	u.chromeDirty = true
	u.overlayDirty = true
}

// applyConfigLive updates fonts/theme/ANSI map from cfg without writing disk.
// Safe to call from settings left/right preview (must not GDI-AV or thrash chrome).
// Never ConPTY-resizes on the caller stack — posts layout settle when metrics change.
func (u *winUI) applyConfigLive(cfg config.Config) {
	defer applog.Recover("applyConfigLive", false)

	cfg = config.Normalize(cfg)
	prev := u.cfg
	u.cfg = cfg
	chrome.ApplyTheme(cfg.Theme)
	SetShellANSIMap(cfg.ShellANSIMap)

	// Sync chrome model config; SyncConfigMsg skips palette rebuild while
	// settings is open so profile left/right does not thrash list state.
	u.chrome = u.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: cfg}).Model
	u.markChromeDirty()

	needFont := !strings.EqualFold(prev.FontFace, cfg.FontFace) || prev.FontSizePx != cfg.FontSizePx
	if needFont {
		// Create new fonts first; never DeleteObject a font still selected into
		// the backbuffer DC (GDI AV / freeze when live-previewing settings).
		newFace := createFontFor(cfg, false)
		newBold := createFontFor(cfg, true)
		if newFace == 0 {
			log.Warn("font create failed; keeping previous", "face", cfg.FontFace)
		} else {
			// Drop backbuffer before deleting fonts that may be selected into it.
			u.releaseBackbuffer()
			oldFace, oldBold, oldCJK := u.font, u.fontBold, u.cjkFont
			u.font, u.fontBold = newFace, newBold
			u.cjkFont = createCJKFont(cfg.FontSizePx)
			if oldFace != 0 {
				win.DeleteObject(win.HGDIOBJ(oldFace))
			}
			if oldBold != 0 {
				win.DeleteObject(win.HGDIOBJ(oldBold))
			}
			if oldCJK != 0 {
				win.DeleteObject(win.HGDIOBJ(oldCJK))
			}
			u.probeKeyGlyphs()
			got := fontFaceName(u.font)
			// Remeasure + ConPTY resize off this stack (click/key/preview).
			u.metricW, u.metricH = 0, 0
			u.postLayoutSettle()
			log.Info("font applied", "face", cfg.FontFace, "px", cfg.FontSizePx, "got", got,
				"cjk", fontFaceName(u.cjkFont))
			if got != "" && !strings.EqualFold(got, cfg.FontFace) {
				u.toast("font fallback: " + got)
			}
		}
	}
	if prev.Theme != cfg.Theme {
		log.Info("theme applied", "theme", cfg.Theme)
	}
	if prev.ShellANSIMap != cfg.ShellANSIMap {
		log.Info("shell ANSI map", "mode", cfg.ShellANSIMap)
	}
	if prev.ActiveProfile != cfg.ActiveProfile {
		log.Info("active profile", "name", cfg.ActiveProfile)
	}
}

func (u *winUI) applyConfigSave(cfg config.Config) {
	defer applog.Recover("applyConfigSave", false)

	cfg = config.Normalize(cfg)
	fontChanged := !strings.EqualFold(u.cfg.FontFace, cfg.FontFace) || u.cfg.FontSizePx != cfg.FontSizePx

	// Enter-stack work must stay light. Theme/ANSI globals only — no palette
	// rebuild, GDI cache drop, ConPTY settle, or toast here (those hard-crashed
	// after "config saved" on the keydown/paint path).
	// Keep live window placement — settings edit snapshot may be stale.
	cfg.Window = u.cfg.Window
	u.cfg = cfg
	chrome.ApplyTheme(cfg.Theme)
	SetShellANSIMap(cfg.ShellANSIMap)
	u.markChromeDirty()

	if err := config.Save(cfg); err != nil {
		log.Error("config save failed", "err", err)
		// Fall back to full live apply so the UI isn't half-updated.
		u.applyConfigLive(cfg)
		u.toast("save failed")
		return
	}
	log.Info("config saved", "path", config.Path(), "theme", cfg.Theme, "fontChanged", fontChanged)
	applog.Sync()

	u.saveNeedFontLayout = fontChanged
	u.postSaveFinish()
}

// openPaletteSafe builds the command palette off the Ctrl+K keydown stack.
func (u *winUI) openPaletteSafe() {
	defer applog.Recover("openPaletteSafe", false)
	if u == nil || !u.alive.Load() {
		return
	}
	log.Info("open palette begin")
	applog.Sync()
	// Ensure lastCfg is sane before rebuildPalette (corrupt load → empty defaults).
	if u.cfg.FontFace == "" {
		u.cfg = config.Normalize(u.cfg)
	}
	r := u.chrome.UpdateChrome(chrome.OpenPaletteMsg{})
	u.chrome = r.Model
	// Warm the list View once here under recover — first paint then only composites.
	func() {
		defer applog.Recover("openPaletteSafe.view", false)
		_ = u.chrome.OverlayView()
	}()
	u.markChromeDirty()
	u.chromePx = u.chromePixelHeight()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
	log.Info("open palette done", "open", u.chrome.PaletteOpen)
	applog.Sync()
}

// postSaveFinish queues one UI-thread pass for post-save GDI/UI cleanup.
func (u *winUI) postSaveFinish() {
	if u == nil || u.hwnd == 0 || !u.alive.Load() {
		return
	}
	if u.saveFinishPosted {
		return
	}
	u.saveFinishPosted = true
	if win.PostMessage(u.hwnd, wmSuzuriSaveFinish, 0, 0) == 0 {
		u.saveFinishPosted = false
		log.Warn("postSaveFinish PostMessage failed — running inline")
		u.finishConfigSave()
	}
}

// finishConfigSave drops paint caches, applies font if needed, toasts, repaints.
// Runs on wmSuzuriSaveFinish (not Enter keydown).
func (u *winUI) finishConfigSave() {
	defer applog.Recover("finishConfigSave", false)
	u.saveFinishPosted = false
	if u == nil || !u.alive.Load() {
		return
	}
	log.Info("settings save finish begin", "needFontLayout", u.saveNeedFontLayout, "intro", u.cfg.Intro)
	applog.Sync()

	// Never keep a mid-flight startup curtain after settings close — switching
	// intro style mid-paint was a crash path (matrix → ripple thrash).
	u.finishMatrixIntro()

	// Drop caches from live-preview churn before the next full paint.
	u.overlayCells = nil
	u.overlayDirty = true
	u.chromeCells = nil
	u.chromeDirty = true
	u.releaseBackbuffer()

	// Palette rebuild + lastCfg sync (skipped on Enter stack). Settings is closed.
	func() {
		defer applog.Recover("finishConfigSave.sync", false)
		u.chrome = u.chrome.UpdateChrome(chrome.SyncConfigMsg{Config: u.cfg}).Model
	}()

	// Font swap (if any) off the key stack.
	if u.saveNeedFontLayout {
		u.applyConfigLive(u.cfg)
		u.metricW, u.metricH = 0, 0
	}
	u.saveNeedFontLayout = false
	// Reflow for toast status row and any font metric change.
	u.postLayoutSettle()

	// Status line without a second nested invalidate storm.
	func() {
		defer applog.Recover("finishConfigSave.toast", false)
		u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg("settings saved")).Model
		u.statusUntil = time.Now().Add(2500 * time.Millisecond)
		u.markChromeDirty()
	}()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
	log.Info("settings save complete")
	applog.Sync()
}

// toast sets a short-lived status line under the tab strip.
func (u *winUI) toast(msg string) {
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg(msg)).Model
	u.statusUntil = time.Now().Add(2500 * time.Millisecond)
	u.markChromeDirty()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

func (u *winUI) clearToastIfDue() {
	if u.statusUntil.IsZero() {
		return
	}
	if time.Now().Before(u.statusUntil) {
		return
	}
	u.statusUntil = time.Time{}
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg("")).Model
	u.markChromeDirty()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

func (u *winUI) chromePixelHeight() int32 {
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

// shellPadY is the top of the shell viewport (tabs stay at the top).
func (u *winUI) shellPadY() int32 {
	return u.chromePixelHeight()
}

// inputBarPixelHeight grows with wrapped / multi-line content (capped).
// Hidden (0) while the active tab is on the alternate screen — full-screen
// apps (Claude, Grok Build, vim…) own the keyboard like Warp.
//
// Vertical budget is symmetric: hairline + top pad + N·cellH + bottom pad.
// (Older code used min 2·cellH with only a light top inset, so single-line
// bars looked bottom-heavy — not because of multi-line prep.)
func (u *winUI) inputBarPixelHeight() int32 {
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
	hair, topPad, botPad := inputBarVPads(ch)
	return hair + topPad + int32(rows)*ch + botPad
}

// inputBarVPads returns hairline, top content inset, and bottom inset (symmetric).
func inputBarVPads(ch int32) (hair, topPad, botPad int32) {
	hair = ch / 10
	if hair < 1 {
		hair = 1
	}
	topPad = ch / 5
	if topPad < 2 {
		topPad = 2
	}
	botPad = topPad
	return hair, topPad, botPad
}

// appOwnsKeyboard is true when the active tab's full-screen app should receive
// raw keys (alt-screen). Host chrome shortcuts still win first.
func (u *winUI) appOwnsKeyboard() bool {
	t := u.activeTab()
	return t != nil && t.altScreen()
}

// maybeResizeForInput recomputes shell rows when the bar height changes.
func (u *winUI) maybeResizeForInput() {
	if u.width > 0 && u.height > 0 {
		u.applyClientSize(u.width, u.height)
	}
}

// shellBottomY is the exclusive bottom of the shell viewport (above input bar).
func (u *winUI) shellBottomY(clientH int32) int32 {
	bot := clientH - u.inputBarPixelHeight()
	if bot < u.shellPadY()+int32(cellH) {
		bot = clientH
	}
	return bot
}

func (u *winUI) applyChromeAction(r chrome.Result) {
	switch r.Action {
	case chrome.ActionNewTab:
		u.newTabUI("")
	case chrome.ActionNewTabProfile:
		u.newTabUI(r.ProfileName)
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
		if u.hwnd != 0 {
			u.persistWindowPlacement(true)
			win.DestroyWindow(u.hwnd)
		}
	case chrome.ActionOpenSettings:
		if r.Settings.FontFace != "" || r.Settings.FontSizePx > 0 {
			u.applyConfigLive(r.Settings)
		}
	case chrome.ActionSettingsPreview:
		u.applyConfigLive(r.Settings)
	case chrome.ActionSettingsApply:
		u.applyConfigSave(r.Settings)
	case chrome.ActionSettingsCancel:
		// Dismiss/Esc restores snap. Skip no-op reapply (open → click off with
		// no edits) — re-running font/theme apply on the click stack has AVed.
		if !configVisualEqual(u.cfg, r.Settings) {
			log.Info("settings cancel restore", "theme", r.Settings.Theme, "font", r.Settings.FontFace)
			u.applyConfigLive(r.Settings)
		} else {
			log.Debug("settings cancel no-op (unchanged)")
		}
		// Drop modal paint cache either way.
		u.overlayCells = nil
		u.overlayDirty = true
		u.chromeDirty = true
	case chrome.ActionSplashDone:
		u.cfg.FirstRunDone = true
		if err := config.Save(u.cfg); err != nil {
			log.Warn("first-run flag save failed", "err", err)
		} else {
			log.Info("first-run complete")
		}
	case chrome.ActionReplayIntro:
		u.replayIntro()
	case chrome.ActionCheckUpdates:
		runUpdateCheck(u.toast)
	}
	u.syncChrome()
}

// replayIntro restarts the configured startup curtain (matrix / ripple / none).
func (u *winUI) replayIntro() {
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
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

func (u *winUI) loop() error {
	// Must stay on this OS thread for the life of the HWND (see main).
	runtime.LockOSThread()

	hInst := win.GetModuleHandle(nil)
	if hInst == 0 {
		return lastErr("GetModuleHandle")
	}

	cname, _ := syscall.UTF16PtrFromString(className)
	title, _ := syscall.UTF16PtrFromString(appTitle)

	// Stable callback: keep ui pinned via global map so GC never collects it
	// while Win32 still has the WndProc pointer.
	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		LpfnWndProc:   wndProcCallback,
		HInstance:     hInst,
		LpszClassName: cname,
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_IBEAM)),
		HbrBackground: win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)),
	}
	if atom := win.RegisterClassEx(&wc); atom == 0 {
		if errno := windows.GetLastError(); errno != windows.ERROR_CLASS_ALREADY_EXISTS {
			return lastErr("RegisterClassEx")
		}
	}

	// Restore last frame placement when valid and still on a visible monitor.
	x, y := int32(win.CW_USEDEFAULT), int32(win.CW_USEDEFAULT)
	cw, ch := int32(u.cols*cellW+24), int32(u.rows*cellH+48)
	wantMax := false
	if wp := u.cfg.Window; wp.Valid() && placementOnScreen(wp) {
		x, y = int32(wp.X), int32(wp.Y)
		cw, ch = int32(wp.Width), int32(wp.Height)
		wantMax = wp.Maximized
		log.Info("restoring window placement", "x", wp.X, "y", wp.Y, "w", wp.Width, "h", wp.Height, "max", wp.Maximized)
	}
	hwnd := win.CreateWindowEx(
		0,
		cname,
		title,
		win.WS_OVERLAPPEDWINDOW, // show after create so maximize restore is clean
		x,
		y,
		cw,
		ch,
		0,
		0,
		hInst,
		nil,
	)
	if hwnd == 0 {
		return lastErr("CreateWindowEx")
	}
	u.hwnd = hwnd
	u.font = createFontFor(u.cfg, false)
	u.fontBold = createFontFor(u.cfg, true)
	u.cjkFont = createCJKFont(u.cfg.FontSizePx)
	u.probeKeyGlyphs()
	face := fontFaceName(u.font)
	log.Info("window created", "hwnd", uintptr(hwnd), "font", face, "want", u.cfg.FontFace,
		"cjk", fontFaceName(u.cjkFont))
	registerUI(hwnd, u)
	// WM_SIZE during CreateWindow arrives before registerUI, so the real
	// client size was never applied (logs showed w=0 h=0 forever). Sync now.
	var rc win.RECT
	if win.GetClientRect(hwnd, &rc) {
		u.applyClientSize(rc.Right-rc.Left, rc.Bottom-rc.Top)
		log.Info("initial client size", "w", u.width, "h", u.height, "cols", u.cols, "rows", u.rows)
	}

	if wantMax {
		win.ShowWindow(hwnd, win.SW_SHOWMAXIMIZED)
	} else {
		win.ShowWindow(hwnd, win.SW_SHOW)
	}
	win.UpdateWindow(hwnd)

	// Startup curtain (matrix / ripple / none) — spawn window then wind-down.
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

	// Start I/O for the first tab; more tabs start in newTabUI.
	if t := u.activeTab(); t != nil {
		t.startWorkers(u)
		log.Info("tab started", "id", t.id, "pid", t.sess.Pid())
	}
	// Loopback MCP bridge (stdio MCP attaches here; not an always-on daemon).
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
		win.InvalidateRect(hwnd, nil, false)
	}
	go u.blinkLoop()

	var msg win.MSG
	for {
		ret := win.GetMessage(&msg, 0, 0, 0)
		if ret == 0 {
			log.Info("WM_QUIT — message loop exit")
			break
		}
		if ret == -1 {
			err := lastErr("GetMessage")
			log.Error("GetMessage failed", "err", err)
			return err
		}
		win.TranslateMessage(&msg)
		win.DispatchMessage(&msg)
	}
	return nil
}

// --- hwnd → *winUI map (WndProc cannot close over a Go method safely long-term)

var (
	uiMu  sync.Mutex
	uiMap = map[win.HWND]*winUI{}
)

func registerUI(hwnd win.HWND, u *winUI) {
	uiMu.Lock()
	uiMap[hwnd] = u
	uiMu.Unlock()
}

func unregisterUI(hwnd win.HWND) {
	uiMu.Lock()
	delete(uiMap, hwnd)
	uiMu.Unlock()
}

func uiFor(hwnd win.HWND) *winUI {
	uiMu.Lock()
	defer uiMu.Unlock()
	return uiMap[hwnd]
}

//export-style fixed callback (function, not method).
var wndProcCallback = syscall.NewCallback(wndProcMain)

func wndProcMain(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	// A panic in WndProc would otherwise kill the process with no trail.
	defer applog.Recover("wndproc", false)
	u := uiFor(hwnd)
	if u == nil {
		return win.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	return u.handle(hwnd, msg, wParam, lParam)
}

func postBytes(u *winUI, tabID int) {
	if u.hwnd == 0 {
		return
	}
	win.PostMessage(u.hwnd, wmSuzuriBytes, uintptr(tabID), 0)
}

func postClosed(u *winUI, tabID int) {
	if u.hwnd == 0 {
		return
	}
	win.PostMessage(u.hwnd, wmSuzuriClosed, uintptr(tabID), 0)
}

func (u *winUI) sendKey(b []byte) {
	if t := u.activeTab(); t != nil {
		t.sendKey(b)
	}
}

func (u *winUI) blinkLoop() {
	t := time.NewTicker(cursorBlinkTick)
	defer t.Stop()
	for range t.C {
		if !u.alive.Load() || u.hwnd == 0 {
			return
		}
		// Only blink when we are the foreground window — idle background
		// invalidates were a major source of "sit for a bit → frozen".
		if win.GetForegroundWindow() != u.hwnd {
			continue
		}
		win.PostMessage(u.hwnd, wmSuzuriBlink, 0, 0)
	}
}

// drainAndParse runs ONLY on the UI thread for the given tab id.
func (u *winUI) drainAndParse(tabID int) {
	t := u.tabByID(tabID)
	if t == nil {
		return
	}
	data := t.takeInput()
	// Drop shell local-echo of the bar-submitted command (ANSI-colored on PS).
	data = t.echo.feed(data)
	if len(data) == 0 {
		// More may have been queued; re-arm if needed.
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
		t.title = shortTitle(title)
	}
	// Alt-screen enter/leave: grow/shrink shell (hide Warp bar) like Warp.
	nowAlt := t.altScreen()
	if nowAlt != t.wasAlt {
		t.wasAlt = nowAlt
		log.Info("alt screen", "tab", t.id, "on", nowAlt)
		if u.activeTab() == t {
			u.maybeResizeForInput()
		}
	}
	u.publishBridgeSnapshot()
	// Only repaint if this is the visible tab.
	if u.activeTab() == t {
		if title := t.term.Title(); title != "" {
			setWindowTitle(u.hwnd, "suzuri — "+title)
		}
		win.InvalidateRect(u.hwnd, nil, false)
	}
	t.inMu.Lock()
	more := len(t.inBuf) > 0
	t.inMu.Unlock()
	if more {
		t.postBytes(u)
	}
}

func (u *winUI) tabByID(id int) *tab {
	for _, t := range u.tabs {
		if t.id == id {
			return t
		}
	}
	return nil
}

// enqueueMCPSubmit is called from the bridge HTTP goroutine; work runs on the UI thread.
func (u *winUI) enqueueMCPSubmit(tabID int, line string) error {
	if u == nil || !u.alive.Load() || u.hwnd == 0 {
		return fmt.Errorf("suzuri UI not ready")
	}
	job := mcpJob{
		tabID: tabID,
		line:  line,
		done:  make(chan error, 1),
	}
	select {
	case u.mcpJobs <- job:
	default:
		return fmt.Errorf("mcp submit queue full")
	}
	if win.PostMessage(u.hwnd, wmSuzuriMCP, 0, 0) == 0 {
		return fmt.Errorf("post mcp job failed")
	}
	select {
	case err := <-job.done:
		return err
	case <-time.After(5 * time.Second):
		return fmt.Errorf("mcp submit timed out")
	}
}

func (u *winUI) drainMCPJobs() {
	for {
		select {
		case job := <-u.mcpJobs:
			err := u.submitOnUIThread(job.tabID, job.line)
			if job.done != nil {
				job.done <- err
			}
		default:
			return
		}
	}
}

// submitOnUIThread mirrors the Warp bar Enter path (block + echo arm + PTY write).
func (u *winUI) submitOnUIThread(tabID int, line string) error {
	t := u.tabByID(tabID)
	if t == nil {
		t = u.activeTab()
	}
	if t == nil {
		return fmt.Errorf("no tab")
	}
	if !t.alive.Load() {
		return fmt.Errorf("tab not alive")
	}
	// Prefer bar path so draft/history stay consistent when line matches.
	if stringsTrimSpace(line) != "" {
		// Fold previous live output into history so this block owns the next run.
		t.sb.commitLive(t.term)
		t.sb.pushBlock(line, u.cols)
		t.echo.arm(line)
		if isClearCommand(line) {
			t.sb.pinHere()
		}
	}
	payload := line
	if strings.Contains(payload, "\n") {
		payload = strings.ReplaceAll(payload, "\n", "\r\n")
	}
	t.sendKey([]byte(payload + "\r"))
	t.sb.stickBottom()
	u.publishBridgeSnapshot()
	win.InvalidateRect(u.hwnd, nil, false)
	return nil
}

func (u *winUI) publishBridgeSnapshot() {
	if u.bridge == nil {
		return
	}
	u.bridge.Publish(u.buildBridgeSnapshot())
}

func (u *winUI) buildBridgeSnapshot() bridge.Snapshot {
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

func (u *winUI) tabSnap(t *tab) bridge.TabSnap {
	armed, cmd, phase := t.echo.status()
	// Live text (effective extent).
	liveText := snapshotLiveText(t.term)
	// Viewport as the user sees it (rune grid → strings).
	view := t.sb.view(t.term, u.rows)
	viewLines := make([]string, len(view))
	for i, row := range view {
		viewLines[i] = strings.TrimRight(string(row), " ")
	}
	// History tail with kinds.
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

func shortTitle(s string) string {
	// basename-ish for tab strip
	s = strings.TrimSpace(s)
	if s == "" {
		return "shell"
	}
	if i := strings.LastIndexAny(s, `/\`); i >= 0 && i+1 < len(s) {
		s = s[i+1:]
	}
	if len(s) > 18 {
		return s[:16] + "…"
	}
	return s
}

// newTabUI opens a tab. profileName empty uses ActiveProfile from config.
func (u *winUI) newTabUI(profileName string) {
	if len(u.tabs) >= maxTabs {
		log.Warn("max tabs reached", "max", maxTabs)
		u.toast("max tabs")
		return
	}
	if profileName == "" {
		profileName = u.cfg.ActiveProfile
	}
	opts := tabOpts{}
	if p := config.FindProfile(u.cfg, profileName); p != nil {
		opts.shell = p.Shell
		opts.cwd = p.Cwd
		if p.Name != "" && !strings.EqualFold(p.Name, "Default") {
			opts.title = p.Name
		}
		if p.Theme != "" {
			u.cfg.Theme = p.Theme
			chrome.ApplyTheme(p.Theme)
			u.markChromeDirty()
		}
	}
	t, err := newTab(u.nextTabID, u.cols, u.rows, opts)
	if err != nil {
		log.Error("new tab failed", "err", err)
		u.toast("new tab failed")
		return
	}
	u.nextTabID++
	u.tabs = append(u.tabs, t)
	u.active = len(u.tabs) - 1
	t.startWorkers(u)
	u.selecting = false
	setWindowTitle(u.hwnd, "suzuri — "+t.title)
	u.syncChrome()
	u.maybeResizeForInput()
	if profileName != "" {
		u.toast("tab: " + profileName)
	}
	win.InvalidateRect(u.hwnd, nil, false)
}

func (u *winUI) closeTabUI(id int) {
	idx := -1
	for i, t := range u.tabs {
		if t.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	// Last tab → confirm quit (Enter quits; Esc keeps the tab).
	if len(u.tabs) == 1 {
		log.Info("last tab close — confirm quit")
		r := u.chrome.UpdateChrome(chrome.OpenConfirmQuitMsg{})
		u.chrome = r.Model
		u.markChromeDirty()
		win.InvalidateRect(u.hwnd, nil, false)
		return
	}
	u.tabs[idx].close()
	u.tabs = append(u.tabs[:idx], u.tabs[idx+1:]...)
	if u.active >= len(u.tabs) {
		u.active = len(u.tabs) - 1
	} else if u.active > idx {
		u.active--
	}
	if t := u.activeTab(); t != nil {
		setWindowTitle(u.hwnd, "suzuri — "+t.title)
	}
	u.syncChrome()
	u.maybeResizeForInput()
	msg := fmt.Sprintf("%d tabs", len(u.tabs))
	if len(u.tabs) == 1 {
		msg = "1 tab"
	}
	u.toast(msg)
	win.InvalidateRect(u.hwnd, nil, false)
}

func (u *winUI) switchTab(delta int) {
	if len(u.tabs) == 0 {
		return
	}
	u.active = (u.active + delta + len(u.tabs)) % len(u.tabs)
	u.selecting = false
	if t := u.activeTab(); t != nil {
		t.sel.clear()
		setWindowTitle(u.hwnd, "suzuri — "+t.title)
	}
	u.syncChrome()
	u.maybeResizeForInput()
	win.InvalidateRect(u.hwnd, nil, false)
}

// hitTab maps an x pixel to a tab index using chrome.TabBounds (same layout as View).
func (u *winUI) hitTab(px int32) int {
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

func (u *winUI) hitPlus(px int32) bool {
	u.syncChrome()
	cellX := u.pixelToChromeCol(px)
	if cellX < 0 {
		return false
	}
	b := u.chrome.PlusBounds()
	return cellX >= b[0] && cellX < b[1]
}

func (u *winUI) pixelToChromeCol(px int32) int {
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


func (u *winUI) handle(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriBytes:
		u.drainAndParse(int(wParam))
		return 0

	case wmSuzuriBlink:
		// Skip blink repaints during frame drag/resize — they fight WM_PAINT
		// and amplify flicker (and GDI thrash with the neko underlay).
		if u.alive.Load() && !u.inSizeMove {
			u.clearToastIfDue()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case wmSuzuriClosed:
		id := int(wParam)
		if t := u.tabByID(id); t != nil {
			_, _ = t.term.Write([]byte("\r\n[suzuri] session ended\r\n"))
			u.toast("session ended")
			if u.activeTab() == t {
				win.InvalidateRect(hwnd, nil, false)
			}
		}
		return 0

	case wmSuzuriMCP:
		u.drainMCPJobs()
		return 0

	case wmSuzuriLayoutSettle:
		// Posted from EXITSIZEMOVE — safe stack for ConPTY/VT/GDI work.
		u.layoutSettlePosted = false
		if !u.alive.Load() || u.inSizeMove {
			return 0
		}
		u.applyLayoutAfterSizeMove(hwnd)
		return 0

	case wmSuzuriSaveFinish:
		u.finishConfigSave()
		return 0

	case wmSuzuriOpenPalette:
		u.openPaletteSafe()
		return 0

	case win.WM_ERASEBKGND:
		return 1

	case win.WM_ACTIVATE:
		// Log re-focus after idle — helps correlate "clicked back → gone".
		active := win.LOWORD(uint32(wParam))
		log.Info("WM_ACTIVATE", "active", active, "alive", u.alive.Load(),
			"w", u.width, "h", u.height, "cols", u.cols, "rows", u.rows)
		applog.Sync()
		if active != win.WA_INACTIVE && u.alive.Load() {
			// Refresh client size (may have changed while unfocused) via the
			// deferred settle path — never ConPTY-resize on the activate stack.
			var rc win.RECT
			if win.GetClientRect(hwnd, &rc) {
				w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
				if w >= 2 && h >= 2 {
					u.width, u.height = w, h
					u.postLayoutSettle()
				}
			}
		}
		// Always repaint on focus change: blinkLoop only ticks while
		// foreground, so without this the caret freezes mid-frame instead
		// of hiding when we lose focus (caretAlpha → 0).
		if u.alive.Load() {
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_ENTERSIZEMOVE:
		// Begin move or resize: defer ConPTY/tab resize until the gesture ends.
		u.inSizeMove = true
		log.Debug("WM_ENTERSIZEMOVE")
		return 0

	case win.WM_EXITSIZEMOVE:
		// Do almost nothing here. applyClientSize / ConPTY / backbuffer work on
		// the EXITSIZEMOVE stack hard-crashed (native AV, no Go panic). Post a
		// settle message and return immediately so Windows finishes the gesture.
		u.inSizeMove = false
		var rc win.RECT
		if win.GetClientRect(hwnd, &rc) {
			w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
			if w >= 2 && h >= 2 {
				u.width, u.height = w, h
			}
			log.Info("WM_EXITSIZEMOVE", "w", w, "h", h)
			applog.Sync()
		}
		// Remember frame pos/size (and monitor) after the user finishes dragging.
		u.persistWindowPlacement(false)
		u.postLayoutSettle()
		return 0

	case win.WM_SIZE:
		// wParam: SIZE_RESTORED=0, SIZE_MINIMIZED=1, SIZE_MAXIMIZED=2, SIZE_MAXSHOW=3, SIZE_MAXHIDE=4
		if wParam == 1 { // SIZE_MINIMIZED — zero/garbage client size; ignore.
			return 0
		}
		w := int32(win.LOWORD(uint32(lParam)))
		h := int32(win.HIWORD(uint32(lParam)))
		if w < 2 || h < 2 {
			return 0
		}
		if u.inSizeMove {
			// Lightweight: remember pixels only. ConPTY + chrome layout wait for settle.
			u.width, u.height = w, h
			return 0
		}
		// Maximize / Aero snap / programmatic size often skip ENTERSIZEMOVE.
		// Still defer heavy work off the WM_SIZE stack.
		u.width, u.height = w, h
		u.postLayoutSettle()
		// Persist maximize / snap frame (no ENTERSIZEMOVE for the chrome button).
		if wParam == 2 || wParam == 0 { // SIZE_MAXIMIZED / SIZE_RESTORED
			u.persistWindowPlacement(false)
		}
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
		// Charm overlay (palette filter) owns printable text while open.
		if u.chrome.OverlayOpen() {
			if u.chrome.PaletteOpen && ch >= 32 && ch != 0x7f {
				km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
				r := u.chrome.UpdateChrome(km)
				u.chrome = r.Model
				u.markChromeDirty()
				u.applyChromeAction(r)
				u.syncChrome()
				u.chromePx = u.chromePixelHeight()
				win.InvalidateRect(hwnd, nil, false)
			}
			// Settings ignores plain text; arrows via KEYDOWN.
			return 0
		}
		// Full-screen app (alt-screen): printable keys go to ConPTY.
		if u.appOwnsKeyboard() {
			switch ch {
			case 0x08, 0x09, 0x0a, 0x0d, 0x7f:
				return 0 // KEYDOWN
			case 0x03, 0x16:
				return 0 // Ctrl+C / Ctrl+V — KEYDOWN
			}
			if b := ptyRuneUTF8(ch); len(b) > 0 {
				u.sendKey(b)
			}
			return 0
		}
		// Warp input bar owns printable text (Enter handled in KEYDOWN).
		switch ch {
		case 0x08, 0x09, 0x0a, 0x0d, 0x7f:
			return 0
		}
		if ch == 0x03 || ch == 0x16 {
			// Ctrl+C / Ctrl+V — handled in KEYDOWN.
			return 0
		}
		if ch >= 32 && ch != 0x7f {
			if in := u.activeInput(); in != nil {
				prevRows := in.visualRows(u.inputContentCols())
				in.insertRune(ch)
				if in.visualRows(u.inputContentCols()) != prevRows {
					u.maybeResizeForInput()
				}
			}
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_KEYDOWN:
		ctrl := win.GetKeyState(win.VK_CONTROL) < 0
		shift := win.GetKeyState(win.VK_SHIFT) < 0
		alt := win.GetKeyState(win.VK_MENU) < 0
		tab := u.activeTab()

		// Charm palette / settings own keys while open (text via WM_CHAR).
		if u.chrome.OverlayOpen() {
			if km := teaKeyFromWin(wParam, ctrl, shift); km != nil {
				r := u.chrome.UpdateChrome(*km)
				u.chrome = r.Model
				u.markChromeDirty()
				u.applyChromeAction(r)
				u.syncChrome()
				u.chromePx = u.chromePixelHeight()
				win.InvalidateRect(hwnd, nil, false)
			}
			return 0
		}
		// Host chrome shortcuts always win (even over full-screen apps).
		// Ctrl+, — settings (VK_OEM_COMMA = 0xBC)
		if ctrl && !shift && wParam == 0xBC {
			r := u.chrome.UpdateChrome(chrome.OpenSettingsMsg{Config: u.cfg})
			u.chrome = r.Model
			u.markChromeDirty()
			u.applyChromeAction(r)
			u.chromePx = u.chromePixelHeight()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		if ctrl && !shift && (wParam == 'K' || wParam == 'k' || wParam == 'P' || wParam == 'p') {
			// Defer list rebuild + first paint off this keydown (native AV trail).
			log.Info("Ctrl+K/P — post open palette")
			applog.Sync()
			if u.hwnd != 0 {
				win.PostMessage(u.hwnd, wmSuzuriOpenPalette, 0, 0)
			}
			return 0
		}

		// Ctrl+/ — help (VK_OEM_2 = 0xBF on US keyboards)
		if ctrl && !shift && wParam == 0xBF {
			r := u.chrome.UpdateChrome(chrome.OpenHelpMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		if ctrl && shift && (wParam == 'T' || wParam == 't') {
			u.newTabUI("")
			return 0
		}
		if ctrl && !shift && (wParam == 'W' || wParam == 'w') {
			if tab != nil {
				u.closeTabUI(tab.id)
			}
			return 0
		}
		if ctrl && wParam == win.VK_TAB {
			if shift {
				u.switchTab(-1)
			} else {
				u.switchTab(1)
			}
			return 0
		}
		if ctrl && wParam >= '1' && wParam <= '9' {
			i := int(wParam - '1')
			if i >= 0 && i < len(u.tabs) {
				u.active = i
				u.selecting = false
				if t := u.activeTab(); t != nil {
					t.sel.clear()
					setWindowTitle(u.hwnd, "suzuri — "+t.title)
				}
				u.syncChrome()
				u.maybeResizeForInput()
				win.InvalidateRect(hwnd, nil, false)
			}
			return 0
		}
		if ctrl && shift && (wParam == 'C' || wParam == 'c') {
			u.copySelection()
			return 0
		}
		if ctrl && shift && (wParam == 'V' || wParam == 'v') {
			u.pasteClipboard()
			return 0
		}
		if ctrl && !shift && wParam == win.VK_INSERT {
			u.copySelection()
			return 0
		}
		if shift && !ctrl && wParam == win.VK_INSERT {
			u.pasteClipboard()
			return 0
		}

		// Full-screen app owns the keyboard (after host shortcuts).
		if u.appOwnsKeyboard() && tab != nil {
			// Ctrl+C with selection still copies; otherwise interrupt app.
			if ctrl && !shift && (wParam == 'C' || wParam == 'c') {
				if !tab.sel.empty() {
					u.copySelection()
				} else {
					u.sendKey([]byte{0x03})
				}
				return 0
			}
			// Ctrl+V / Shift+Insert paste into the PTY, not the bar.
			if (ctrl && !shift && (wParam == 'V' || wParam == 'v')) ||
				(shift && !ctrl && wParam == win.VK_INSERT) {
				if text, err := getClipboardText(hwnd); err == nil && text != "" {
					// Normalize newlines for PTY paste.
					payload := strings.ReplaceAll(text, "\r\n", "\n")
					payload = strings.ReplaceAll(payload, "\n", "\r")
					u.sendKey([]byte(payload))
				}
				return 0
			}
			if b := ptyKeyFromWin(tab.term, wParam, ctrl, shift, alt); len(b) > 0 {
				u.sendKey(b)
			}
			return 0
		}

		// Ctrl+C: copy selection, else clear bar, else interrupt PTY.
		if ctrl && !shift && (wParam == 'C' || wParam == 'c') {
			in := u.activeInput()
			if tab != nil && !tab.sel.empty() {
				u.copySelection()
			} else if in != nil && len(in.runes) > 0 {
				in.clear()
				u.maybeResizeForInput()
				win.InvalidateRect(hwnd, nil, false)
			} else {
				u.sendKey([]byte{0x03})
			}
			return 0
		}
		// Ctrl+V: paste into input bar.
		if ctrl && !shift && (wParam == 'V' || wParam == 'v') {
			u.pasteClipboard()
			return 0
		}
		if tab == nil {
			return 0
		}
		in := u.activeInput()
		if in == nil {
			return 0
		}
		cols := u.inputContentCols()
		// Warp input bar editing + shell scroll.
		switch wParam {
		case win.VK_RETURN:
			if shift {
				// Shift+Enter — new line in the bar (multiline script).
				prevRows := in.visualRows(cols)
				in.insertNewline()
				if in.visualRows(cols) != prevRows {
					u.maybeResizeForInput()
				}
				win.InvalidateRect(hwnd, nil, false)
				return 0
			}
			line := in.submit()
			u.maybeResizeForInput()
			// Warp-style command block in scrollback, then send to PTY.
			// Arm echo suppress so PS/cmd local-echo doesn't duplicate the block.
			if stringsTrimSpace(line) != "" {
				// Previous command's output → history under its block first.
				tab.sb.commitLive(tab.term)
				tab.sb.pushBlock(line, u.cols)
				tab.echo.arm(line)
				if isClearCommand(line) {
					tab.sb.pinHere()
				}
				log.Debug("submit arm echo", "tab", tab.id, "line", line)
			}
			// Multi-line: send with real newlines; final CR executes.
			payload := line
			if strings.Contains(payload, "\n") {
				// PowerShell accepts multi-line paste ending in CR.
				payload = strings.ReplaceAll(payload, "\n", "\r\n")
			}
			u.sendKey([]byte(payload + "\r"))
			tab.sb.stickBottom()
			u.publishBridgeSnapshot()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_UP:
			if !in.moveVisualUp(cols) {
				in.historyUp()
			}
			u.maybeResizeForInput()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_DOWN:
			if !in.moveVisualDown(cols) {
				in.historyDown()
			}
			u.maybeResizeForInput()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_RIGHT:
			in.moveRight()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_LEFT:
			in.moveLeft()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_DELETE:
			in.deleteForward()
			u.maybeResizeForInput()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_HOME:
			in.moveHome()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_END:
			in.moveEnd()
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_PRIOR:
			tab.sb.scrollBy(u.rows/2, u.rows)
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_NEXT:
			tab.sb.scrollBy(-(u.rows / 2), u.rows)
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_ESCAPE:
			if len(in.runes) > 0 || in.histIdx >= 0 {
				in.clear()
				u.maybeResizeForInput()
				win.InvalidateRect(hwnd, nil, false)
			}
		case win.VK_BACK:
			u.handleInputBackspace(hwnd, lParam)
		case win.VK_TAB:
			// No completion yet — insert two spaces for indent-ish typing.
			in.insertRunes([]rune{' ', ' '})
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_MOUSEWHEEL:
		tab := u.activeTab()
		if tab == nil {
			return 0
		}
		// Full-screen apps own the surface — don't scroll host history under them.
		if tab.altScreen() {
			return 0
		}
		delta := int16(wParam >> 16)
		steps := int(delta) / 120
		if steps == 0 {
			if delta > 0 {
				steps = 1
			} else {
				steps = -1
			}
		}
		tab.sb.scrollBy(steps*3, u.rows)
		win.InvalidateRect(hwnd, nil, false)
		return 0

	case win.WM_LBUTTONDOWN:
		u.focus()
		px := int32(win.LOWORD(uint32(lParam)))
		py := int32(win.HIWORD(uint32(lParam)))
		chH := u.metricH
		if chH < 1 {
			chH = cellH
		}
		tabStripH := int32(chrome.TabStripRows()) * chH
		chromeH := u.chromePixelHeight()

		// Click outside floating overlay (on shell) dismisses it.
		if u.chrome.OverlayOpen() && py >= chromeH {
			log.Info("dismiss overlay (click outside)")
			applog.Sync()
			// Clear overlay cells first so the next paint cannot draw a stale
			// card while cancel restores theme/font.
			u.overlayCells = nil
			u.overlayDirty = true
			r := u.chrome.UpdateChrome(chrome.DismissOverlayMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			// applyChromeAction may restore settings snap; keep it light (no
			// ConPTY on this stack — applyConfigLive posts layout settle).
			func() {
				defer applog.Recover("dismissOverlay", false)
				u.applyChromeAction(r)
				u.syncChrome()
			}()
			log.Info("dismiss overlay done")
			applog.Sync()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}

		// Top tab strip / + chip.
		if py < chromeH {
			if py < tabStripH {
				if u.hitPlus(px) {
					u.newTabUI("")
					return 0
				}
				if i := u.hitTab(px); i >= 0 {
					u.active = i
					u.selecting = false
					if t := u.activeTab(); t != nil {
						t.sel.clear()
						setWindowTitle(u.hwnd, "suzuri — "+t.title)
					}
					u.syncChrome()
					u.maybeResizeForInput()
					win.InvalidateRect(hwnd, nil, false)
				}
			}
			return 0
		}
		// Bottom Warp input bar — focus only (no text selection drag).
		var client win.RECT
		if win.GetClientRect(hwnd, &client) {
			if py >= u.shellBottomY(client.Bottom-client.Top) {
				u.focus()
				return 0
			}
		}
		tab := u.activeTab()
		if tab == nil {
			return 0
		}
		x, y := u.pixelToCell(px, py)
		absY := tab.sb.absLine(y, u.rows, liveExtent(tab.term))
		tab.sel.active = true
		tab.sel.x0, tab.sel.y0 = x, absY
		tab.sel.x1, tab.sel.y1 = x, absY
		u.selecting = true
		win.SetCapture(hwnd)
		win.InvalidateRect(hwnd, nil, false)
		return 0

	case win.WM_MOUSEMOVE:
		tab := u.activeTab()
		if tab != nil && u.selecting && (wParam&win.MK_LBUTTON) != 0 {
			x, y := u.pixelToCell(int32(win.LOWORD(uint32(lParam))), int32(win.HIWORD(uint32(lParam))))
			absY := tab.sb.absLine(y, u.rows, liveExtent(tab.term))
			tab.sel.x1, tab.sel.y1 = x, absY
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_LBUTTONUP:
		tab := u.activeTab()
		if tab != nil && u.selecting {
			x, y := u.pixelToCell(int32(win.LOWORD(uint32(lParam))), int32(win.HIWORD(uint32(lParam))))
			absY := tab.sb.absLine(y, u.rows, liveExtent(tab.term))
			tab.sel.x1, tab.sel.y1 = x, absY
			u.selecting = false
			win.ReleaseCapture()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_RBUTTONUP:
		u.pasteClipboard()
		return 0

	case win.WM_PAINT:
		if u.alive.Load() {
			u.paint(hwnd)
		} else {
			// Still must validate the update region or Windows repaints forever.
			var ps win.PAINTSTRUCT
			hdc := win.BeginPaint(hwnd, &ps)
			_ = hdc
			win.EndPaint(hwnd, &ps)
		}
		return 0

	case win.WM_CLOSE:
		log.Info("WM_CLOSE")
		// Capture placement while the HWND is still valid.
		u.persistWindowPlacement(true)
		win.DestroyWindow(hwnd)
		return 0

	case win.WM_DESTROY:
		// Best-effort if WM_CLOSE was skipped (e.g. DestroyWindow from quit).
		u.persistWindowPlacement(true)
		log.Info("WM_DESTROY — tearing down", "tabs", len(u.tabs))
		u.alive.Store(false)
		if u.bridge != nil {
			u.bridge.Stop()
		}
		unregisterUI(hwnd)
		for _, t := range u.tabs {
			t.close()
		}
		u.tabs = nil
		u.releaseBackbuffer()
		if u.font != 0 {
			win.DeleteObject(win.HGDIOBJ(u.font))
			u.font = 0
		}
		if u.fontBold != 0 {
			win.DeleteObject(win.HGDIOBJ(u.fontBold))
			u.fontBold = 0
		}
		if u.cjkFont != 0 {
			win.DeleteObject(win.HGDIOBJ(u.cjkFont))
			u.cjkFont = 0
		}
		u.hwnd = 0
		win.PostQuitMessage(0)
		return 0

	case win.WM_QUIT:
		// DefWindowProc path — message loop also sees GetMessage==0.
		log.Info("WM_QUIT")
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (u *winUI) paint(hwnd win.HWND) {
	// Native AVs still won't land here, but Go panics in chrome/VT must not
	// kill the process with no trail (focus-after-idle was a common path).
	defer func() {
		if r := recover(); r != nil {
			log.Error("paint panic",
				"err", fmt.Sprint(r),
				"stack", string(debug.Stack()),
			)
			applog.Sync()
		}
	}()

	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)
	if hdc == 0 {
		log.Warn("BeginPaint returned null HDC")
		return
	}

	var rect win.RECT
	win.GetClientRect(hwnd, &rect)
	w := rect.Right - rect.Left
	h := rect.Bottom - rect.Top
	// Minimized / zero-size clients still get WM_PAINT — do nothing useful.
	if w < 2 || h < 2 {
		return
	}

	// During frame drag/resize: solid fill only. Full paint (grid + neko field +
	// chrome) every WM_PAINT while the mouse tracks caused severe flicker and
	// GDI AVs on mouse-up. Real content returns on EXITSIZEMOVE.
	if u.inSizeMove {
		u.width, u.height = w, h
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.VoidR, chrome.VoidG, chrome.VoidB)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, rect, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
		return
	}

	tab := u.activeTab()
	if tab == nil {
		fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))
		return
	}
	if u.font == 0 {
		log.Error("paint with null font — skipping draw")
		return
	}

	// Measure real cell metrics with the active font *before* layout so shell
	// row count matches what we paint (avoids a dead band under the grid).
	oldF := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	cw, ch := measureCellSize(hdc)
	win.SelectObject(hdc, oldF)
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	metricsChanged := u.metricW != cw || u.metricH != ch
	u.metricW, u.metricH = cw, ch

	// Keep size fields honest; re-layout when bar hides (alt-screen) or metrics
	// change. Never applyClientSize (ConPTY) on the WM_PAINT stack — post settle.
	wantIn := u.inputBarPixelHeight()
	if u.width != w || u.height != h || wantIn != u.inputPx || metricsChanged {
		u.width, u.height = w, h
		u.postLayoutSettle()
	}

	// Viewport = history + live screen (live cells carry FG/BG/bold).
	// Shell PTY cursor is hidden in Warp-bar mode; shown for alt-screen apps.
	grid := tab.sb.viewCells(tab.term, u.rows)
	cur := tab.term.Cursor()
	curVis := tab.altScreen() && tab.term.CursorVisible()
	curY := cur.Y

	draw := func(dest win.HDC) {
		defer applog.Recover("paint.draw", false)
		u.blitGrid(dest, rect, grid, cur.X, curY, curVis)
		// Dim shell under floating overlay (palette / settings).
		if u.chrome.OverlayOpen() {
			u.paintDimShell(dest, rect)
		} else if u.matrixIntroActive() {
			// Startup curtain only — recover so a bad intro cannot kill the host.
			func() {
				defer applog.Recover("paint.intro", false)
				switch strings.ToLower(strings.TrimSpace(u.cfg.Intro)) {
				case config.IntroRipple:
					u.paintRippleIntro(dest, rect)
				case config.IntroNone:
					if time.Now().After(u.matrixIntroSpawnEnd) {
						u.finishMatrixIntro()
					}
				default:
					u.paintMatrixIntro(dest, rect)
				}
			}()
		}
		// Chrome strip + Warp input + floating card into the same buffer.
		if u.font == 0 {
			return
		}
		oldF := win.SelectObject(dest, win.HGDIOBJ(u.font))
		u.paintChrome(dest, rect)
		u.paintInputBar(dest, rect)
		if u.chrome.OverlayOpen() {
			u.paintOverlay(dest, rect)
		}
		win.SelectObject(dest, oldF)
	}

	if !u.ensureBackbuffer(hdc, w, h) {
		draw(hdc)
		return
	}
	draw(u.memDC)
	if !win.BitBlt(hdc, 0, 0, w, h, u.memDC, 0, 0, win.SRCCOPY) {
		// Fallback if BitBlt fails (stale DC after long suspend).
		log.Warn("BitBlt failed — direct paint fallback")
		u.releaseBackbuffer()
		draw(hdc)
	}
}

// configVisualEqual is true when live-previewable fields match (cancel no-op).
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

// SM_* virtual desktop metrics (multi-monitor).
const (
	smXVIRTUALSCREEN  = 76
	smYVIRTUALSCREEN  = 77
	smCXVIRTUALSCREEN = 78
	smCYVIRTUALSCREEN = 79
)

// placementOnScreen is true when the outer rect intersects the virtual screen
// (so we do not restore off-monitor after a display was unplugged).
func placementOnScreen(p config.WindowPlacement) bool {
	if !p.Valid() {
		return false
	}
	vx := win.GetSystemMetrics(smXVIRTUALSCREEN)
	vy := win.GetSystemMetrics(smYVIRTUALSCREEN)
	vw := win.GetSystemMetrics(smCXVIRTUALSCREEN)
	vh := win.GetSystemMetrics(smCYVIRTUALSCREEN)
	if vw < 1 || vh < 1 {
		return true // metrics unavailable — trust saved coords
	}
	left, top := int32(p.X), int32(p.Y)
	right, bottom := left+int32(p.Width), top+int32(p.Height)
	// Require at least a 80×40px sliver visible.
	const pad int32 = 80
	sl := left + pad
	if sl > right {
		sl = right
	}
	st := top + 40
	if st > bottom {
		st = bottom
	}
	return right > vx && bottom > vy && left < vx+vw && top < vy+vh &&
		sl > vx && st > vy
}

// captureWindowPlacement reads the restored outer frame via GetWindowPlacement
// (correct while maximized — GetWindowRect would return work-area bounds only).
func (u *winUI) captureWindowPlacement() (config.WindowPlacement, bool) {
	if u == nil || u.hwnd == 0 {
		return config.WindowPlacement{}, false
	}
	var wp win.WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	if !win.GetWindowPlacement(u.hwnd, &wp) {
		return config.WindowPlacement{}, false
	}
	rc := wp.RcNormalPosition
	w := int(rc.Right - rc.Left)
	h := int(rc.Bottom - rc.Top)
	if w < 320 || h < 200 {
		return config.WindowPlacement{}, false
	}
	maxed := wp.ShowCmd == win.SW_SHOWMAXIMIZED ||
		wp.ShowCmd == win.SW_MAXIMIZE ||
		(wp.Flags&win.WPF_RESTORETOMAXIMIZED) != 0
	// Minimized: still persist normal rect; do not treat as maximized unless flag set.
	if wp.ShowCmd == win.SW_SHOWMINIMIZED || wp.ShowCmd == win.SW_MINIMIZE {
		maxed = (wp.Flags & win.WPF_RESTORETOMAXIMIZED) != 0
	}
	return config.WindowPlacement{
		X:         int(rc.Left),
		Y:         int(rc.Top),
		Width:     w,
		Height:    h,
		Maximized: maxed,
	}, true
}

// persistWindowPlacement updates u.cfg.Window and optionally writes config.json.
// sync=false for mid-session moves (still writes — cheap and survives crash).
func (u *winUI) persistWindowPlacement(forceLog bool) {
	defer applog.Recover("persistWindowPlacement", false)
	if u == nil || u.hwnd == 0 {
		return
	}
	p, ok := u.captureWindowPlacement()
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
	if forceLog {
		log.Info("window placement saved", "x", p.X, "y", p.Y, "w", p.Width, "h", p.Height, "max", p.Maximized)
	} else {
		log.Debug("window placement saved", "x", p.X, "y", p.Y, "w", p.Width, "h", p.Height, "max", p.Maximized)
	}
}

// postLayoutSettle queues one UI-thread layout pass (coalesced).
func (u *winUI) postLayoutSettle() {
	if u == nil || u.hwnd == 0 || !u.alive.Load() {
		return
	}
	if u.layoutSettlePosted {
		return
	}
	u.layoutSettlePosted = true
	if win.PostMessage(u.hwnd, wmSuzuriLayoutSettle, 0, 0) == 0 {
		u.layoutSettlePosted = false
		log.Warn("postLayoutSettle PostMessage failed")
	}
}

// applyLayoutAfterSizeMove runs off the size-move stack: measure, reflow chrome,
// resize VT/ConPTY once, rebuild backbuffer on next paint.
func (u *winUI) applyLayoutAfterSizeMove(hwnd win.HWND) {
	defer applog.Recover("applyLayoutAfterSizeMove", false)
	if u == nil || !u.alive.Load() {
		return
	}
	var rc win.RECT
	if !win.GetClientRect(hwnd, &rc) {
		return
	}
	w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
	if w < 2 || h < 2 {
		return
	}
	log.Info("layout settle begin", "w", w, "h", h, "cols", u.cols, "rows", u.rows)
	applog.Sync()

	// Prefer last-known cell metrics; remeasure if we have a window DC.
	if hdc := win.GetDC(hwnd); hdc != 0 {
		if u.font != 0 {
			old := win.SelectObject(hdc, win.HGDIOBJ(u.font))
			cw, ch := measureCellSize(hdc)
			win.SelectObject(hdc, old)
			if cw > 0 {
				u.metricW = cw
			}
			if ch > 0 {
				u.metricH = ch
			}
		}
		win.ReleaseDC(hwnd, hdc)
	}

	// Drop stale backbuffer so next paint allocates at the new size.
	u.releaseBackbuffer()
	u.overlayDirty = true
	u.chromeDirty = true

	u.applyClientSize(w, h)
	log.Info("layout settle done", "w", w, "h", h, "cols", u.cols, "rows", u.rows)
	applog.Sync()

	if u.alive.Load() {
		win.InvalidateRect(hwnd, nil, false)
	}
}

// applyClientSize updates cols/rows/chrome from a client pixel size.
// Safe to call from layout settle, first paint, and WM_ACTIVATE — not from the
// raw WM_EXITSIZEMOVE stack (that hard-crashed).
// Layout: [tab strip] [shell VT] [Warp input bar].
// When the bar is hidden (alt-screen), shell uses all space under the tabs —
// no reserved empty strip at the bottom.
func (u *winUI) applyClientSize(w, h int32) {
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
	// Full remaining height under chrome (and under bar when shown).
	shellH := h - u.chromePx - u.inputPx
	if u.inputPx > 0 && shellH < ch*5 {
		// Bar mode only: keep a usable shell by compressing the bar floor.
		shellH = h - u.chromePx - ch*2
	}
	if shellH < ch {
		shellH = ch
	}
	rows := int(shellH / ch)
	if rows < 1 {
		rows = 1
	}
	// In bar mode keep a modest minimum; on alt-screen use every full cell.
	if u.inputPx > 0 && rows < 5 {
		rows = 5
	}
	if rows > maxTermRows {
		rows = maxTermRows
	}
	if rows != u.rows || cols != u.cols {
		u.cols = cols
		u.rows = rows
		// Copy tab pointers so a concurrent close cannot mutate the slice mid-loop.
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

// blitGrid paints colored cells at fixed pitch.
// Backgrounds/selection use FillRect (no pen hairlines) coalesced into runs so
// selection never shows a per-cell grid. Glyphs are placed at x*cellW so the
// font’s natural advance cannot drift off the grid.
func (u *winUI) blitGrid(hdc win.HDC, rect win.RECT, grid [][]cellPix, curX, curY int, curVis bool) {
	tab := u.activeTab()
	if tab == nil {
		return
	}

	fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)

	cw, ch := measureCellSize(hdc)
	const padX int32 = 4
	// Shell sits below the top tab strip and above the Warp input bar.
	padY := u.shellPadY()
	u.metricW, u.metricH = cw, ch
	u.chromePx = padY
	u.inputPx = u.inputBarPixelHeight()
	// Same effective live height as viewCells (trailing blank PTY rows clipped).
	liveRows := liveExtent(tab.term)
	// Fill any sub-cell remainder under the grid down to the shell bottom so
	// alt-screen (no bar) doesn't leave a thin empty band. Sample the last
	// cell's BG so fullscreen apps blend instead of showing a black gutter.
	shellBot := u.shellBottomY(rect.Bottom - rect.Top)
	gridBot := padY + int32(len(grid))*ch
	if gridBot < shellBot {
		br, bg, bb := byte(12), byte(12), byte(14)
		if n := len(grid); n > 0 {
			last := grid[n-1]
			if len(last) > 0 {
				c := last[0]
				// Prefer a non-default background if the app painted one.
				if c.BR != 0 || c.BG != 0 || c.BB != 0 {
					br, bg, bb = c.BR, c.BG, c.BB
				} else {
					// Scan for any BG on the last row.
					for _, cell := range last {
						if cell.BR != 0 || cell.BG != 0 || cell.BB != 0 {
							br, bg, bb = cell.BR, cell.BG, cell.BB
							break
						}
					}
				}
			}
		}
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(br, bg, bb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: 0, Top: gridBot, Right: rect.Right, Bottom: shellBot}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}

	// Dim brand mark under shell cells (not the tab-strip 硯).
	u.paintShellWatermark(hdc, rect, padY, shellBot)

	selBrush := win.HBRUSH(0)
	if tab.sel.active && !tab.sel.empty() {
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(40, 80, 160)}
		selBrush = win.CreateBrushIndirect(&lb)
	}
	if selBrush != 0 {
		defer win.DeleteObject(win.HGDIOBJ(selBrush))
	}

	for y, row := range grid {
		// Non-default backgrounds: one FillRect per run of the same BG.
		type bgRun struct {
			x0, x1     int
			r, g, b    byte
		}
		var bgs []bgRun
		for x, c := range row {
			if c.BR == 0 && c.BG == 0 && c.BB == 0 {
				continue
			}
			if n := len(bgs); n > 0 && bgs[n-1].x1 == x-1 &&
				bgs[n-1].r == c.BR && bgs[n-1].g == c.BG && bgs[n-1].b == c.BB {
				bgs[n-1].x1 = x
				continue
			}
			bgs = append(bgs, bgRun{x0: x, x1: x, r: c.BR, g: c.BG, b: c.BB})
		}
		for _, br := range bgs {
			lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(br.r, br.g, br.b)}
			if brush := win.CreateBrushIndirect(&lb); brush != 0 {
				r := win.RECT{
					Left:   padX + int32(br.x0)*cw,
					Top:    padY + int32(y)*ch,
					Right:  padX + int32(br.x1+1)*cw,
					Bottom: padY + int32(y+1)*ch,
				}
				fillRect(hdc, r, brush)
				win.DeleteObject(win.HGDIOBJ(brush))
			}
		}

		// Selection: coalesce contiguous cells into one rect (no grid seams).
		if selBrush != 0 {
			absY := tab.sb.absLine(y, u.rows, liveRows)
			run0 := -1
			flushSel := func(x1 int) {
				if run0 < 0 {
					return
				}
				r := win.RECT{
					Left:   padX + int32(run0)*cw,
					Top:    padY + int32(y)*ch,
					Right:  padX + int32(x1+1)*cw,
					Bottom: padY + int32(y+1)*ch,
				}
				fillRect(hdc, r, selBrush)
				run0 = -1
			}
			for x := range row {
				if tab.sel.containsAbs(x, absY) {
					if run0 < 0 {
						run0 = x
					}
				} else {
					flushSel(x - 1)
				}
			}
			flushSel(len(row) - 1)
		}

		// Glyphs at fixed cell origins. Box-drawing / blocks are drawn as
		// geometry that fills the cell (Windows Terminal-style seamless TUI
		// chrome); normal text uses TextOut.
		win.SetBkMode(hdc, win.TRANSPARENT)
		for x, c := range row {
			r := c.Ch
			if r == 0 || r == ' ' {
				continue
			}
			absY := tab.sb.absLine(y, u.rows, liveRows)
			fr, fg, fb := c.FR, c.FG, c.FB
			if tab.sel.containsAbs(x, absY) {
				fr, fg, fb = 255, 255, 255
			}
			px := padX + int32(x)*cw
			py := padY + int32(y)*ch
			cell := win.RECT{Left: px, Top: py, Right: px + cw, Bottom: py + ch}
			if drawCellGlyph(hdc, r, cell, fr, fg, fb) {
				continue
			}
			s, err := syscall.UTF16FromString(string(r))
			if err != nil || len(s) < 2 {
				continue
			}
			u.selectFontForRune(hdc, r, c.Bold)
			win.SetTextColor(hdc, win.RGB(fr, fg, fb))
			win.TextOut(hdc, px, py, &s[0], int32(len(s)-1))
		}
	}
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}

	if curVis {
		if curX < 0 {
			curX = 0
		}
		if curY < 0 {
			curY = 0
		}
		// Blend caret into cell background (smooth alpha, not hard blink).
		bgR, bgG, bgB := byte(12), byte(12), byte(14)
		if curY < len(grid) && curX < len(grid[curY]) {
			c := grid[curY][curX]
			if c.BR != 0 || c.BG != 0 || c.BB != 0 {
				bgR, bgG, bgB = c.BR, c.BG, c.BB
			}
		}
		a := u.caretAlpha()
		if a > 0 {
			cr, cg, cb := blendRGB(bgR, bgG, bgB, 220, 220, 220, a)
			lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
			if brush := win.CreateBrushIndirect(&lb); brush != 0 {
				cellL := padX + int32(curX)*cw
				cellT := padY + int32(curY)*ch
				var r win.RECT
				switch u.cfg.Cursor {
				case config.CursorUnderline:
					th := ch / 8
					if th < 2 {
						th = 2
					}
					r = win.RECT{Left: cellL, Top: cellT + ch - th, Right: cellL + cw, Bottom: cellT + ch}
				case config.CursorBar:
					th := cw / 5
					if th < 2 {
						th = 2
					}
					r = win.RECT{Left: cellL, Top: cellT, Right: cellL + th, Bottom: cellT + ch}
				default: // block
					r = win.RECT{Left: cellL, Top: cellT, Right: cellL + cw, Bottom: cellT + ch}
				}
				fillRect(hdc, r, brush)
				win.DeleteObject(win.HGDIOBJ(brush))
			}
		}
	}
}

// measureCellSize returns the monospaced cell size for the font selected in hdc.
// GetTextExtent of "M" matches glyph advance better than TmAveCharWidth, which
// is often 1px short and produces a visible grid of seams under selection.
func measureCellSize(hdc win.HDC) (cw, ch int32) {
	cw, ch = cellW, cellH
	var tm win.TEXTMETRIC
	if win.GetTextMetrics(hdc, &tm) {
		if tm.TmHeight > 0 {
			ch = tm.TmHeight
		}
		if tm.TmAveCharWidth > 0 {
			cw = tm.TmAveCharWidth
		}
	}
	m, err := syscall.UTF16FromString("M")
	if err == nil {
		var sz win.SIZE
		if win.GetTextExtentPoint32(hdc, &m[0], 1, &sz) && sz.CX > 0 {
			cw = sz.CX
		}
	}
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	return cw, ch
}

// fillRect is user32 FillRect — solid fill with no pen border (avoids GDI
// Rectangle hairlines between adjacent cells).
func fillRect(hdc win.HDC, r win.RECT, brush win.HBRUSH) {
	_, _, _ = procFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&r)), uintptr(brush))
}

// selectFontForRune picks the primary mono face, or CJK fallback for Han/kana.
func (u *winUI) selectFontForRune(hdc win.HDC, r rune, bold bool) {
	if isEastAsianRune(r) && u.cjkFont != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.cjkFont))
		return
	}
	if bold && u.fontBold != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.fontBold))
		return
	}
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
}

// createCJKFont picks a system monospaced CJK face at the UI point size.
func createCJKFont(sizePx int) win.HFONT {
	if sizePx < 10 {
		sizePx = 14
	}
	// Prefer classic JP mono, then UI faces that still map 猫/硯.
	for _, name := range []string{
		"MS Gothic",
		"ＭＳ ゴシック",
		"Yu Gothic",
		"Yu Gothic UI",
		"Meiryo",
		"Meiryo UI",
		"Noto Sans Mono CJK JP",
		"Noto Sans CJK JP",
	} {
		h := createNamedFont(name, -int32(sizePx), win.FW_NORMAL)
		if h == 0 {
			continue
		}
		got := fontFaceName(h)
		if faceMatches(got, name) || (got != "" && strings.Contains(strings.ToLower(got), "gothic")) ||
			strings.Contains(strings.ToLower(got), "meiryo") ||
			strings.Contains(strings.ToLower(got), "noto") {
			return h
		}
		// Accept any face that actually has 猫 (GDI sometimes renames).
		if cjkFontHasNeko(h) {
			return h
		}
		win.DeleteObject(win.HGDIOBJ(h))
	}
	return 0
}

func cjkFontHasNeko(h win.HFONT) bool {
	if h == 0 {
		return false
	}
	hdc := win.CreateCompatibleDC(0)
	if hdc == 0 {
		return false
	}
	defer win.DeleteDC(hdc)
	old := win.SelectObject(hdc, win.HGDIOBJ(h))
	ok := fontHasRunes(hdc, '猫')
	win.SelectObject(hdc, old)
	return ok
}

func utf8Encode(p []byte, r rune) int {
	switch {
	case r <= 0x7f:
		p[0] = byte(r)
		return 1
	case r <= 0x7ff:
		p[0] = 0xc0 | byte(r>>6)
		p[1] = 0x80 | byte(r&0x3f)
		return 2
	case r <= 0xffff:
		p[0] = 0xe0 | byte(r>>12)
		p[1] = 0x80 | byte((r>>6)&0x3f)
		p[2] = 0x80 | byte(r&0x3f)
		return 3
	default:
		p[0] = 0xf0 | byte(r>>18)
		p[1] = 0x80 | byte((r>>12)&0x3f)
		p[2] = 0x80 | byte((r>>6)&0x3f)
		p[3] = 0x80 | byte(r&0x3f)
		return 4
	}
}

// Preferred monospaced faces. Bundled FiraCode Nerd Font Mono is registered
// process-privately at startup; Cascadia ships with modern Windows as fallback.
// CreateFont always "succeeds" via substitution, so we verify the face with
// GetTextFaceW and fall through if GDI faked it.
var fontFallbacks = []string{
	BundledFace,
	"Cascadia Mono",
	"Cascadia Code",
	"Consolas",
	"Lucida Console",
	"Courier New",
}

func createFont() win.HFONT {
	return createFontFor(config.Default(), false)
}

func createFontFor(cfg config.Config, bold bool) win.HFONT {
	size := cfg.FontSizePx
	if size < 10 {
		size = 16
	}
	weight := int32(win.FW_NORMAL)
	if bold {
		weight = win.FW_BOLD
	}
	names := make([]string, 0, len(fontFallbacks)+1)
	if cfg.FontFace != "" {
		names = append(names, cfg.FontFace)
	}
	for _, n := range fontFallbacks {
		if n == cfg.FontFace {
			continue
		}
		names = append(names, n)
	}
	for _, name := range names {
		h := createNamedFont(name, -int32(size), weight)
		if h == 0 {
			continue
		}
		got := fontFaceName(h)
		if faceMatches(got, name) {
			return h
		}
		// GDI substituted a different face (font not installed).
		win.DeleteObject(win.HGDIOBJ(h))
	}
	// Last resort: any FIXED_PITCH without a face claim.
	var lf win.LOGFONT
	lf.LfHeight = -int32(size)
	lf.LfWeight = weight
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfQuality = win.CLEARTYPE_QUALITY
	lf.LfPitchAndFamily = win.FIXED_PITCH | win.FF_MODERN
	return win.CreateFontIndirect(&lf)
}

func createNamedFont(faceName string, height, weight int32) win.HFONT {
	var lf win.LOGFONT
	lf.LfHeight = height
	lf.LfWeight = weight
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfOutPrecision = win.OUT_TT_ONLY_PRECIS
	lf.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
	lf.LfQuality = win.CLEARTYPE_QUALITY
	lf.LfPitchAndFamily = win.FIXED_PITCH | win.FF_MODERN
	face, err := syscall.UTF16FromString(faceName)
	if err != nil {
		return 0
	}
	copy(lf.LfFaceName[:], face)
	return win.CreateFontIndirect(&lf)
}

func faceMatches(got, want string) bool {
	g := strings.ToLower(strings.TrimSpace(got))
	w := strings.ToLower(strings.TrimSpace(want))
	if g == "" || w == "" {
		return false
	}
	return g == w || strings.HasPrefix(g, w)
}

// fontFaceName returns the face actually selected into a temp DC (after GDI
// substitution), not merely the LOGFONT request string.
func fontFaceName(h win.HFONT) string {
	if h == 0 {
		return ""
	}
	hdc := win.CreateCompatibleDC(0)
	if hdc == 0 {
		return ""
	}
	defer win.DeleteDC(hdc)
	old := win.SelectObject(hdc, win.HGDIOBJ(h))
	if old == 0 {
		return ""
	}
	defer win.SelectObject(hdc, old)
	var buf [64]uint16
	n, _, _ := procGetTextFace.Call(uintptr(hdc), uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

func lastErr(op string) error {
	return windows.Errno(win.GetLastError())
}

var (
	modUser32         = windows.NewLazySystemDLL("user32.dll")
	modGdi32          = windows.NewLazySystemDLL("gdi32.dll")
	procSetWindowText = modUser32.NewProc("SetWindowTextW")
	procFillRect      = modUser32.NewProc("FillRect")
	procGetTextFace   = modGdi32.NewProc("GetTextFaceW")
)

func setWindowTitle(hwnd win.HWND, title string) {
	p, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	_, _, _ = procSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(p)))
}

func (u *winUI) focus() {
	if u.hwnd != 0 {
		win.SetFocus(u.hwnd)
	}
}

func (u *winUI) pixelToCell(px, py int32) (x, y int) {
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
	return
}

func (u *winUI) copySelection() {
	tab := u.activeTab()
	if tab == nil || tab.sel.empty() {
		return
	}
	text := tab.sel.text(tab.sb, tab.term)
	if text == "" {
		return
	}
	_ = setClipboardText(u.hwnd, text)
}

func (u *winUI) pasteClipboard() {
	text, err := getClipboardText(u.hwnd)
	if err != nil || text == "" {
		return
	}
	// Full-screen app: paste straight into ConPTY.
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
	// Paste into the Warp input bar (newlines kept for multiline).
	prevRows := in.visualRows(u.inputContentCols())
	in.insertRunes([]rune(text))
	if in.visualRows(u.inputContentCols()) != prevRows {
		u.maybeResizeForInput()
	}
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

// handleInputBackspace edits the Warp bar (rate-limited like the old PTY BS).
func (u *winUI) handleInputBackspace(hwnd win.HWND, lParam uintptr) {
	wasDown := (uint32(lParam) & (1 << 30)) != 0
	now := time.Now()
	if wasDown && now.Sub(u.lastBackspace) < 30*time.Millisecond {
		u.drainQueuedBackspaces(hwnd)
		return
	}
	u.lastBackspace = now
	if in := u.activeInput(); in != nil {
		prevRows := in.visualRows(u.inputContentCols())
		in.backspace()
		if in.visualRows(u.inputContentCols()) != prevRows {
			u.maybeResizeForInput()
		}
	}
	win.InvalidateRect(hwnd, nil, false)
	u.drainQueuedBackspaces(hwnd)
}

func (u *winUI) drainQueuedBackspaces(hwnd win.HWND) {
	var msg win.MSG
	for {
		if !win.PeekMessage(&msg, hwnd, 0, 0, win.PM_NOREMOVE) {
			return
		}
		switch {
		case msg.Message == win.WM_KEYDOWN && msg.WParam == uintptr(win.VK_BACK):
			win.PeekMessage(&msg, hwnd, 0, 0, win.PM_REMOVE)
		case msg.Message == win.WM_KEYUP && msg.WParam == uintptr(win.VK_BACK):
			win.PeekMessage(&msg, hwnd, 0, 0, win.PM_REMOVE)
		case msg.Message == win.WM_CHAR && (msg.WParam == 0x08 || msg.WParam == 0x7f):
			win.PeekMessage(&msg, hwnd, 0, 0, win.PM_REMOVE)
		case msg.Message == win.WM_SYSKEYDOWN && msg.WParam == uintptr(win.VK_BACK):
			win.PeekMessage(&msg, hwnd, 0, 0, win.PM_REMOVE)
		default:
			return
		}
	}
}

func (u *winUI) releaseBackbuffer() {
	if u.memDC == 0 {
		return
	}
	// Deselect our bitmap before deleting it — deleting a selected HBITMAP
	// is undefined and is a common source of delayed GDI AVs.
	deselected := false
	if u.memOldBmp != 0 {
		win.SelectObject(u.memDC, u.memOldBmp)
		u.memOldBmp = 0
		deselected = true
	}
	if u.memBmp != 0 {
		if deselected {
			win.DeleteObject(win.HGDIOBJ(u.memBmp))
		} else {
			// Still selected and we have no prior object — leak the bitmap
			// rather than SelectObject(0) / DeleteObject-while-selected (AV).
			log.Warn("backbuffer bitmap still selected; leaking to avoid GDI AV")
		}
		u.memBmp = 0
	}
	win.DeleteDC(u.memDC)
	u.memDC = 0
	u.memW, u.memH = 0, 0
}

func (u *winUI) ensureBackbuffer(hdc win.HDC, w, h int32) bool {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	// Absurd sizes (minimized/corrupt lParam) — refuse rather than ask GDI for a
	// multi-gigabit bitmap that fails or hard-faults.
	const maxBackbufferPx int32 = 16384
	if w > maxBackbufferPx || h > maxBackbufferPx {
		log.Warn("ensureBackbuffer refused huge size", "w", w, "h", h)
		return false
	}
	if u.memDC != 0 && u.memBmp != 0 && u.memW == w && u.memH == h {
		return true
	}
	u.releaseBackbuffer()
	u.memDC = win.CreateCompatibleDC(hdc)
	if u.memDC == 0 {
		log.Warn("CreateCompatibleDC failed")
		return false
	}
	u.memBmp = win.CreateCompatibleBitmap(hdc, w, h)
	if u.memBmp == 0 {
		log.Warn("CreateCompatibleBitmap failed", "w", w, "h", h)
		win.DeleteDC(u.memDC)
		u.memDC = 0
		return false
	}
	// Keep the DC's previous object so we can deselect cleanly later.
	u.memOldBmp = win.SelectObject(u.memDC, win.HGDIOBJ(u.memBmp))
	if u.memOldBmp == 0 {
		// Select failed — don't leave a half-set buffer.
		log.Warn("SelectObject backbuffer failed")
		win.DeleteObject(win.HGDIOBJ(u.memBmp))
		u.memBmp = 0
		win.DeleteDC(u.memDC)
		u.memDC = 0
		return false
	}
	u.memW, u.memH = w, h
	return true
}

// matrixIntroActive is true while startup rain is spawning or winding down.
func (u *winUI) matrixIntroActive() bool {
	if u == nil || u.matrixIntroStart.IsZero() || u.matrixIntroDone {
		return false
	}
	// Hard stop so we never paint forever if wind-down math misbehaves.
	if time.Since(u.matrixIntroStart) > matrixIntroMaxTotal {
		u.finishMatrixIntro()
		return false
	}
	return true
}

// finishMatrixIntro ends the rain curtain and starts the watermark fade-in clock.
func (u *winUI) finishMatrixIntro() {
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
// Stays hidden through most of the spawn window, then eases in while streams
// are still winding down (not waiting until the last drop leaves).
func (u *winUI) watermarkFade() float64 {
	if u == nil {
		return 1
	}
	// No intro scheduled: full mark.
	if u.matrixIntroStart.IsZero() || u.matrixIntroSpawnEnd.IsZero() {
		return 1
	}
	// Let drops start leaving first, then bring the mark up under them.
	const (
		afterSpawnDelay = 0.55 // seconds after spawn ends before fade begins
		fadeIn          = 1.25 // seconds to full whisper opacity
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
	// Smoothstep ease-in so it doesn't pop at the start.
	return t * t * (3 - 2*t)
}

// paintMatrixIntro is the startup rain over the shell viewport (not chrome/bar).
// No dim matte — shell stays black so the center 硯 has no cutout; rain draws
// over the logo. After spawn, streams wind down without wrapping.
func (u *winUI) paintMatrixIntro(hdc win.HDC, rect win.RECT) {
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY {
		return
	}
	mode := matrixSpawn
	if time.Now().After(u.matrixIntroSpawnEnd) {
		mode = matrixWindDown
	}
	// Rain only — logo is already in blitGrid underneath.
	drew := u.paintDimMatrix(hdc, rect, padY, bot, mode, u.matrixIntroStart, matrixIntroSpawn)
	if mode == matrixWindDown && !drew {
		u.finishMatrixIntro()
	}
}

// paintDimShell darkens the shell viewport under a floating overlay.
// Settings: animated Matrix rain. Other modals: Charm-style 猫咪 field.
func (u *winUI) paintDimShell(hdc win.HDC, rect win.RECT) {
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY {
		return
	}
	if u.chrome.SettingsOpen {
		// Continuous loop under settings (independent of intro).
		u.paintMatrixMatteAndRain(hdc, rect, padY, bot, matrixLoop, u.blinkStart, 0)
		return
	}
	// Non-settings overlays: theme dim + 猫咪 texture.
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.DimR, chrome.DimG, chrome.DimB)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		r := win.RECT{Left: 0, Top: padY, Right: rect.Right, Bottom: bot}
		fillRect(hdc, r, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
	u.paintDimNekoField(hdc, rect, padY, bot)
}

// paintMatrixMatteAndRain fills a dark theme-tinted matte then digital rain.
func (u *winUI) paintMatrixMatteAndRain(hdc win.HDC, rect win.RECT, padY, bot int32, mode matrixPaintMode, t0 time.Time, spawnFor time.Duration) bool {
	// Pull hard toward void/black, keep a whisper of primary (~5%) for brand.
	baseR, baseG, baseB := blendRGB(0, 0, 0, chrome.DimR, chrome.DimG, chrome.DimB, 0.35)
	matteR, matteG, matteB := blendRGB(baseR, baseG, baseB,
		chrome.PrimR, chrome.PrimG, chrome.PrimB, 0.05)
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(matteR, matteG, matteB)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		r := win.RECT{Left: 0, Top: padY, Right: rect.Right, Bottom: bot}
		fillRect(hdc, r, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
	return u.paintDimMatrix(hdc, rect, padY, bot, mode, t0, spawnFor)
}

// dimUnderlayChars is Charm's lipgloss Place whitespace fill: 猫 + 咪 ("kitty").
// Cycled left-to-right so each fullwidth column stays one glyph down the page.
var dimUnderlayChars = []string{"猫", "咪"}

// paintDimNekoField draws a Charm-style 猫咪 pattern over the dim matte.
// Fullwidth glyphs advance two mono cells. Very dim so the dialog stays dominant.
// Uses a clip rect so a partial last row still fills leftover height (no blank band).
func (u *winUI) paintDimNekoField(hdc win.HDC, rect win.RECT, top, bot int32) {
	if hdc == 0 || bot <= top {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Fullwidth glyph → two cell columns (terminal East-Asian width).
	stepX := cw * 2
	if stepX < 2 {
		stepX = 2
	}
	font := u.cjkFont
	if font == 0 {
		font = u.font
	}
	if font == 0 {
		return
	}

	// Clip so a partial bottom row still paints into leftover pixels
	// when shell height is not an exact multiple of cell height.
	saved := win.SaveDC(hdc)
	if saved == 0 {
		return
	}
	defer win.RestoreDC(hdc, saved)
	_ = win.IntersectClipRect(hdc, 0, top, rect.Right, bot)

	oldF := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldF)

	// Barely above dim matte — texture only, not readable text.
	// ~1/12 soft + 11/12 dim.
	fr := byte((int(chrome.SoftR) + int(chrome.DimR)*11) / 12)
	fg := byte((int(chrome.SoftG) + int(chrome.DimG)*11) / 12)
	fb := byte((int(chrome.SoftB) + int(chrome.DimB)*11) / 12)
	win.SetTextColor(hdc, win.RGB(fr, fg, fb))
	win.SetBkMode(hdc, win.TRANSPARENT)

	// Pre-encode 猫 / 咪 for TextOut (UTF-16, null-terminated).
	glyphs := make([][]uint16, 0, len(dimUnderlayChars))
	for _, chs := range dimUnderlayChars {
		s, err := syscall.UTF16FromString(chs)
		if err != nil || len(s) < 2 {
			continue
		}
		glyphs = append(glyphs, s)
	}
	if len(glyphs) == 0 {
		return
	}

	// Cycle like lipgloss.WithWhitespaceChars("猫咪"): column i uses glyph i%n.
	// Continue past bot; clip discards overflow (fills leftover strip).
	for y := top; y < bot; y += ch {
		col := 0
		for x := int32(0); x < rect.Right; x += stepX {
			s := glyphs[col%len(glyphs)]
			win.TextOut(hdc, x, y, &s[0], int32(len(s)-1))
			col++
		}
	}
}

// paintInputBar draws the Warp-style fixed command line at the bottom.
// Grows with soft-wrap / Shift+Enter newlines (capped at maxInputVisualRows).
// Skipped while a full-screen (alt-screen) app owns the keyboard.
func (u *winUI) paintInputBar(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	if u.appOwnsKeyboard() {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	clientH := rect.Bottom - rect.Top
	barH := u.inputBarPixelHeight()
	u.inputPx = barH
	top := clientH - barH
	if top < 0 {
		top = 0
	}

	// Panel fill.
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.PanelR, chrome.PanelG, chrome.PanelB)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			r := win.RECT{Left: 0, Top: top, Right: rect.Right, Bottom: rect.Bottom}
			fillRect(hdc, r, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}
	hair, topPad, _ := inputBarVPads(ch)
	// Top accent hairline (primary).
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.PrimR, chrome.PrimG, chrome.PrimB)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			r := win.RECT{Left: 0, Top: top, Right: rect.Right, Bottom: top + hair}
			fillRect(hdc, r, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}

	const padX int32 = 8
	// Content starts after hairline + top pad (matches inputBarPixelHeight).
	padTop := top + hair + topPad

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)
	win.SetBkMode(hdc, win.TRANSPARENT)

	prompt := inputBarPrompt
	promptRunes := len([]rune(prompt))
	promptW := int32(promptRunes) * cw
	contentCols := u.inputContentCols()

	in := u.activeInput()
	empty := in == nil || len(in.runes) == 0

	// Placeholder when empty.
	if empty {
		pr, err := syscall.UTF16FromString(prompt)
		if err == nil && len(pr) >= 2 {
			win.SetTextColor(hdc, win.RGB(chrome.PrimR, chrome.PrimG, chrome.PrimB))
			win.TextOut(hdc, padX, padTop, &pr[0], int32(len(pr)-1))
		}
		// Fancy ⇧ when the active font has it; else ASCII (see refreshKeyGlyphs).
		// Nudge 2 cells right of the prompt so it doesn't crowd the ❯.
		hint := chrome.InputBarPlaceholder()
		if s, err := syscall.UTF16FromString(hint); err == nil && len(s) >= 2 {
			win.SetTextColor(hdc, win.RGB(chrome.SoftR, chrome.SoftG, chrome.SoftB))
			win.TextOut(hdc, padX+promptW+2*cw, padTop, &s[0], int32(len(s)-1))
		}
		// Caret at start.
		u.paintInputCaret(hdc, padX+promptW, padTop, cw, ch)
		return
	}

	view, caretRow, caretCol := in.visibleWindow(contentCols, maxInputVisualRows)
	indentW := promptW // continuation lines align under content start

	for i, line := range view {
		y := padTop + int32(i)*ch
		xText := padX + promptW
		if i == 0 {
			// Prompt only on first visible row when that row is the logical start.
			// When scrolled, still show a dim continuation marker.
			if caretRow == i && /* best effort: */ true {
				pr, err := syscall.UTF16FromString(prompt)
				if err == nil && len(pr) >= 2 {
					win.SetTextColor(hdc, win.RGB(chrome.PrimR, chrome.PrimG, chrome.PrimB))
					win.TextOut(hdc, padX, y, &pr[0], int32(len(pr)-1))
				}
			}
		} else {
			xText = padX + indentW
		}
		// For row 0 we already reserved promptW; for others indent matches.
		if i == 0 {
			xText = padX + promptW
		}
		if line != "" {
			if s, err := syscall.UTF16FromString(line); err == nil && len(s) >= 2 {
				win.SetTextColor(hdc, win.RGB(chrome.TextR, chrome.TextG, chrome.TextB))
				win.TextOut(hdc, xText, y, &s[0], int32(len(s)-1))
			}
		}
	}

	// Caret on the caret row within the window.
	caretY := padTop + int32(caretRow)*ch
	caretX := padX + promptW + int32(caretCol)*cw
	u.paintInputCaret(hdc, caretX, caretY, cw, ch)
}

func (u *winUI) paintInputCaret(hdc win.HDC, x, y, cw, ch int32) {
	// Smooth opacity while focused; hidden when another window has focus.
	// (GDI FillRect has no true alpha; lerping colors reads as transparency.)
	a := u.caretAlpha()
	if a <= 0 {
		return
	}
	cr, cg, cb := blendRGB(
		chrome.PanelR, chrome.PanelG, chrome.PanelB,
		chrome.PrimR, chrome.PrimG, chrome.PrimB,
		a,
	)
	// Same shape as the shell cursor (settings: block / underline / bar).
	var r win.RECT
	switch u.cfg.Cursor {
	case config.CursorUnderline:
		th := ch / 8
		if th < 2 {
			th = 2
		}
		r = win.RECT{Left: x, Top: y + ch - th, Right: x + cw, Bottom: y + ch}
	case config.CursorBar:
		th := cw / 5
		if th < 2 {
			th = 2
		}
		r = win.RECT{Left: x, Top: y, Right: x + th, Bottom: y + ch}
	default: // block — full cell (default and config "block")
		r = win.RECT{Left: x, Top: y, Right: x + cw, Bottom: y + ch}
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		fillRect(hdc, r, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
}

// caretAlpha is 0..1 for a smooth pulse while focused; 0 when unfocused
// (hide caret — same convention as most terminals).
func (u *winUI) caretAlpha() float64 {
	if u.hwnd == 0 || win.GetForegroundWindow() != u.hwnd {
		return 0
	}
	elapsed := time.Since(u.blinkStart).Seconds()
	period := cursorBlinkPeriod.Seconds()
	if period < 0.1 {
		period = 0.1
	}
	// Sine in [0,1]; floor slightly above 0 so the caret never fully vanishes
	// while focused.
	s := 0.5 + 0.5*math.Sin(2*math.Pi*(elapsed/period))
	return 0.12 + 0.88*s
}

// isTransparentOverlayBG is true for cells that should not cover the dim+猫 underlay.
func isTransparentOverlayBG(r, g, b byte) bool {
	if r == 0 && g == 0 && b == 0 {
		return true
	}
	// Theme void / dim matte (lipgloss gutters, clear-to-void).
	if r == chrome.VoidR && g == chrome.VoidG && b == chrome.VoidB {
		return true
	}
	if r == chrome.DimR && g == chrome.DimG && b == chrome.DimB {
		return true
	}
	return false
}

// paintChromeCells paints a cached cell grid at pixel origin (ox, oy).
// defaultBar=true: tab strip — empty cells use bar fill, edge runs span the window.
// defaultBar=false: floating overlay — empty default-bg cells are transparent so the
// dimmed shell shows through around the dialog (no full-width panel/void stripes).
func (u *winUI) paintChromeCells(hdc win.HDC, rect win.RECT, cells [][]cellPix, ox, oy int32, defaultBar bool) {
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	type bgRun struct {
		x0, x1  int
		r, g, b byte
	}
	for y := 0; y < len(cells); y++ {
		row := cells[y]
		var runs []bgRun
		for x := 0; x < len(row); x++ {
			cell := row[x]
			br, bg, bb := cell.BR, cell.BG, cell.BB
			empty := cell.Ch == 0 || cell.Ch == ' '
			// Overlay: transparent gutters so dim+猫 shows left/right of the card.
			// Default black OR theme void (PlaceHorizontal used to fill sides with void).
			if !defaultBar && empty && (isTransparentOverlayBG(br, bg, bb)) {
				continue
			}
			if br == 0 && bg == 0 && bb == 0 {
				if defaultBar {
					br, bg, bb = chrome.BarR, chrome.BarG, chrome.BarB
				} else {
					// Non-empty glyph with default bg (rare) — use panel so it stays readable.
					br, bg, bb = chrome.PanelR, chrome.PanelG, chrome.PanelB
				}
			}
			if n := len(runs); n > 0 && runs[n-1].x1 == x-1 &&
				runs[n-1].r == br && runs[n-1].g == bg && runs[n-1].b == bb {
				runs[n-1].x1 = x
				continue
			}
			// Starting a run after a transparent gap: do not merge across skip.
			runs = append(runs, bgRun{x0: x, x1: x, r: br, g: bg, b: bb})
		}
		for _, rn := range runs {
			lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(rn.r, rn.g, rn.b)}
			if brush := win.CreateBrushIndirect(&lb); brush != 0 {
				r := win.RECT{
					Left:   ox + 4 + int32(rn.x0)*cw,
					Top:    oy + int32(y)*ch,
					Right:  ox + 4 + int32(rn.x1+1)*cw,
					Bottom: oy + int32(y+1)*ch,
				}
				if rn.x0 == 0 && defaultBar {
					r.Left = 0
				}
				if rn.x1 >= len(row)-1 && defaultBar {
					r.Right = rect.Right
				}
				fillRect(hdc, r, brush)
				win.DeleteObject(win.HGDIOBJ(brush))
			}
		}
		win.SetBkMode(hdc, win.TRANSPARENT)
		for x := 0; x < len(row); x++ {
			cell := row[x]
			r := cell.Ch
			if r == 0 || r == ' ' {
				continue
			}
			cellRect := win.RECT{
				Left:   ox + 4 + int32(x)*cw,
				Top:    oy + int32(y)*ch,
				Right:  ox + 4 + int32(x+1)*cw,
				Bottom: oy + int32(y+1)*ch,
			}
			if drawCellGlyph(hdc, r, cellRect, cell.FR, cell.FG, cell.FB) {
				continue
			}
			s, err := syscall.UTF16FromString(string(r))
			if err != nil || len(s) < 2 {
				continue
			}
			u.selectFontForRune(hdc, r, cell.Bold)
			win.SetTextColor(hdc, win.RGB(cell.FR, cell.FG, cell.FB))
			win.TextOut(hdc, cellRect.Left, cellRect.Top, &s[0], int32(len(s)-1))
		}
	}
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
}

// paintChrome renders the tab strip (not the floating overlay).
func (u *winUI) paintChrome(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	u.syncChrome()
	cols := u.cols
	if cols < 20 {
		cols = 20
	}
	crowsWant := u.chrome.RowCount()
	if crowsWant > 20 {
		crowsWant = 20
	}

	cells := u.chromeCells
	if u.chromeDirty || u.chromeCols != cols || len(cells) != crowsWant {
		ct := chrome.RenderToTerm(u.chrome, cols)
		ccols, crows := ct.Size()
		if crows > 20 {
			crows = 20
		}
		cells = make([][]cellPix, crows)
		for y := 0; y < crows; y++ {
			row := make([]cellPix, cols)
			for x := 0; x < cols && x < ccols; x++ {
				row[x] = glyphToCell(ct.Cell(x, y))
			}
			cells[y] = row
		}
		u.chromeCells = cells
		u.chromeCols = cols
		u.chromeDirty = false
	}

	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	chromeH := int32(len(cells)) * ch
	if chromeH > rect.Bottom-rect.Top {
		chromeH = rect.Bottom - rect.Top
	}

	// Tab strip at the top of the client area.
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.BarR, chrome.BarG, chrome.BarB)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			r := win.RECT{Left: 0, Top: 0, Right: rect.Right, Bottom: chromeH}
			fillRect(hdc, r, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}
	u.paintChromeCells(hdc, rect, cells, 0, 0, true)
	u.chromePx = chromeH
}

// paintOverlay draws the floating settings/palette card over the shell.
func (u *winUI) paintOverlay(hdc win.HDC, rect win.RECT) {
	if hdc == 0 || !u.chrome.OverlayOpen() {
		return
	}
	cols := u.cols
	if cols < 20 {
		cols = 20
	}
	// Rebuild when dirty or width changes. Do not key on a fixed row estimate —
	// settings + help caption is taller than the old 20-row paint cap.
	if u.overlayDirty || u.chromeCols != cols || len(u.overlayCells) == 0 {
		func() {
			defer applog.Recover("paintOverlay.render", false)
			ct := chrome.RenderOverlayToTerm(u.chrome, cols)
			ccols, crows := ct.Size()
			const maxOverlayRows = 48
			if crows > maxOverlayRows {
				crows = maxOverlayRows
			}
			if crows < 1 {
				crows = 1
			}
			cells := make([][]cellPix, crows)
			for y := 0; y < crows; y++ {
				row := make([]cellPix, cols)
				for x := 0; x < cols && x < ccols; x++ {
					row[x] = glyphToCell(ct.Cell(x, y))
				}
				cells[y] = row
			}
			u.overlayCells = cells
			u.chromeCols = cols
			u.overlayDirty = false
		}()
		if len(u.overlayCells) == 0 {
			return
		}
	}
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	// Place in the shell region. Prefer slight top bias; if the stack is tall
	// (settings + help), shift up so the bottom caption is not clipped.
	padY := u.chromePx
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	shellH := bot - padY
	oh := int32(len(u.overlayCells)) * ch
	oy := padY + (shellH-oh)/4
	if oy+oh > bot {
		oy = bot - oh
	}
	if oy < padY {
		oy = padY
	}
	u.paintChromeCells(hdc, rect, u.overlayCells, 0, oy, false)
}

// teaKeyFromWin maps Win32 navigation keys into Bubble Tea messages for the
// palette. Printable text arrives via WM_CHAR so filter typing works.
func teaKeyFromWin(wParam uintptr, ctrl, shift bool) *tea.KeyMsg {
	_ = ctrl
	switch wParam {
	case win.VK_ESCAPE:
		km := tea.KeyMsg{Type: tea.KeyEsc}
		return &km
	case win.VK_RETURN:
		km := tea.KeyMsg{Type: tea.KeyEnter}
		return &km
	case win.VK_UP:
		km := tea.KeyMsg{Type: tea.KeyUp}
		return &km
	case win.VK_DOWN:
		km := tea.KeyMsg{Type: tea.KeyDown}
		return &km
	case win.VK_TAB:
		if shift {
			km := tea.KeyMsg{Type: tea.KeyShiftTab}
			return &km
		}
		km := tea.KeyMsg{Type: tea.KeyTab}
		return &km
	case win.VK_BACK:
		km := tea.KeyMsg{Type: tea.KeyBackspace}
		return &km
	case win.VK_LEFT:
		km := tea.KeyMsg{Type: tea.KeyLeft}
		return &km
	case win.VK_RIGHT:
		km := tea.KeyMsg{Type: tea.KeyRight}
		return &km
	}
	return nil
}
