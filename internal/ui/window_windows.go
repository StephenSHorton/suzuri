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
	"github.com/StephenSHorton/suzuri/internal/caffeine"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/vt"
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
	// wmSuzuriToast delivers a status toast from a background goroutine
	// (manual update check) onto the UI thread.
	wmSuzuriToast = win.WM_APP + 8
	// wmSuzuriUpdateOffer opens the update confirm modal (version in toastPending).
	wmSuzuriUpdateOffer = win.WM_APP + 9
	// wmSuzuriTransfer drains transfer progress/status from engine goroutines.
	wmSuzuriTransfer = win.WM_APP + 10
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
	ui.mcpJobs = make(chan mcpJob, 8)
	ui.bridge.BindSubmit(ui.enqueueMCPSubmit)
	ui.bridge.BindNotes(ui.enqueueMCPNotes)
	ui.bridge.BindWorkspace(ui.enqueueMCPWorkspace)
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

// mcpJob is work from the loopback MCP bridge (HTTP goroutine → UI thread).
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


type winUI struct {
	// pages are chrome-strip tabs; each may hold a split tree of panes.
	// tabs is the flat list of all pane sessions (I/O by id, bridge, teardown).
	pages  []*page
	tabs   []*tab
	active int // active page index (chrome strip)
	// lastPaneLayout / lastSashes: active page geometry (paint/hit/focus/drag).
	lastPaneLayout []paneGeom
	lastSashes     []sashGeom
	lastShell      struct{ x, y, w, h int32 }
	// sashDrag is non-nil while the user is dragging a shared pane divider.
	sashDrag *sashGeom
	nextTabID int
	chrome    chrome.Model // Charm UI: tabs, status, palette

	hwnd     win.HWND
	font     win.HFONT
	fontBold win.HFONT
	// CJK-capable mono fallback (MS Gothic, etc.) for 硯 / 猫 / shell JP text.
	// Primary UI fonts (Gohu, Cascadia) typically lack Han glyphs.
	cjkFont win.HFONT
	// Symbol fallback (Cascadia / Segoe UI Symbol) for status shapes when the
	// primary mono face lacks them. Prefer primary when it has the glyphs so
	// advance width matches the cell grid (avoids tab overflow).
	symFont           win.HFONT
	primaryHasGeo     bool // ●○◉◎ present in primary face
	primaryHasBraille bool
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
	// shellMulti / notesMulti: double-click word, triple-click line selection.
	shellMulti multiClick
	notesMulti multiClick
	statusUntil   time.Time // clear toast Status after this (zero = none)
	showSplash    bool      // open first-run card after window is ready
	spinTick      uint64    // blink-loop counter for tab braille spinner
	// modalImage: full-window image viewer (click path / Open Image / image block).
	modalImage *tabImage
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

	// notesDragging: LBUTTON held after a notes body click (drag-select text).
	notesDragging bool
	// Link hover: http(s)/www under the cursor.
	hoverLink    linkSpan
	hoverLinkOK  bool
	linkCursorOn bool
	// altMouseDown: left button held while reporting clicks to an alt-screen app.
	altMouseDown bool
	// Last SGR motion cell (1-based) sent to alt-screen; avoid flooding the PTY.
	altMouseCol, altMouseRow int

	// Stay-awake (☕ top-right). Process-local SetThreadExecutionState.
	caffeine *caffeine.Manager
	// lastCaffeineHint drives chrome dirty when the timed label ticks down.
	lastCaffeineHint string

	// Async alt-screen paste (clipboard image dump must not block the UI thread).
	pasteBusy      atomic.Bool
	pendingPasteMu sync.Mutex
	pendingPaste   []pendingPaste
	lastPasteAt    time.Time

	// User is dragging or resizing the frame (WM_ENTERSIZEMOVE … EXITSIZEMOVE).
	// During this window we must not thrash ConPTY / GDI: every WM_SIZE used to
	// resize all tabs + recreate the backbuffer, which hard-crashed mid-drag.
	inSizeMove bool
	// layoutSettlePosted coalesces deferred post-resize layout messages.
	layoutSettlePosted bool
	// layoutDeferred: ConPTY resize needed but panes are mid-stream (dual Grok).
	// Paint-only relayout runs now; full settle only when I/O is quiet.
	// Paint must not re-post settle every frame while set.
	layoutDeferred bool
	// layoutDeferredAt is when layoutDeferred first became true (for max-wait
	// paint-only refresh; never forces ConPTY under load).
	layoutDeferredAt time.Time
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

	// overlaySceneReady: memDC already holds a static dim underlay (splash/
	// confirm) so later paints only re-draw the floating card. Palette and
	// help float over a live shell and full-repaint. Cleared on resize / open /
	// close. (A dual CompatibleBitmap underlay hard-crashed.)
	overlaySceneReady bool

	// paintPending coalesces InvalidateRect. Dual busy alt-screen panes (two
	// Grok sessions) used to PostMessage+invalidate on every PTY chunk and
	// hard-crash GDI with no Go panic trail.
	paintPending bool

	// inputOnlyDirty: Warp bar text/caret changed but shell cells did not.
	// WM_PAINT can re-draw only the bar(s) into memDC (notes-style scoping).
	// Cleared on PTY output, resize, scroll, overlay, chrome strip changes.
	inputOnlyDirty bool

	// toastPending is set from background goroutines; drained on wmSuzuriToast.
	toastMu      sync.Mutex
	toastPending string
	// updateOfferVer is the version string for wmSuzuriUpdateOffer confirm.
	updateOfferVer string

	// Transfer status queue (engine goroutine → UI thread).
	xferMu      sync.Mutex
	xferPending []chrome.TransferStatusMsg
	// fileDropAccept mirrors DragAcceptFiles — only true on Send-file prompt.
	fileDropAccept bool

	// MCP bridge: loopback HTTP for spawn-on-demand stdio MCP (see internal/bridge).
	bridge  *bridge.Host
	mcpJobs chan mcpJob
}

// requestPaint marks the client dirty at most once until the next WM_PAINT.
func (u *winUI) requestPaint() {
	if u == nil || u.hwnd == 0 || u.paintPending {
		return
	}
	u.paintPending = true
	win.InvalidateRect(u.hwnd, nil, false)
}

// monitorWorkArea returns the nearest monitor's work rect (excludes taskbar).
func (u *winUI) monitorWorkArea() (left, top, right, bottom int, ok bool) {
	if u == nil || u.hwnd == 0 {
		return 0, 0, 0, 0, false
	}
	hmon := win.MonitorFromWindow(u.hwnd, win.MONITOR_DEFAULTTONEAREST)
	if hmon == 0 {
		return 0, 0, 0, 0, false
	}
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if !win.GetMonitorInfo(hmon, &mi) {
		return 0, 0, 0, 0, false
	}
	return int(mi.RcWork.Left), int(mi.RcWork.Top), int(mi.RcWork.Right), int(mi.RcWork.Bottom), true
}

// requestInputPaint marks only the Warp bar dirty (shell grid unchanged).
// Falls back to a full paint when a floating overlay is open (palette/notes/…).
// chromeDirty alone is fine: the input-only paint path re-draws the strip
// without re-blitting the shell grid (same idea as macOS tryPaintInputOnly).
// While ambient is on, blink periodically clears sticky input-only so rain/CRT
// keep moving (see wmSuzuriBlink).
func (u *winUI) requestInputPaint() {
	if u == nil || u.hwnd == 0 {
		return
	}
	if u.chrome.OverlayOpen() {
		u.inputOnlyDirty = false
		u.requestPaint()
		return
	}
	u.inputOnlyDirty = true
	u.requestPaint()
}

// warpBarInsertNewline adds a soft line break in the Warp bar (Shift/Alt+Enter).
func (u *winUI) warpBarInsertNewline(in *inputBar) {
	if u == nil || in == nil {
		return
	}
	cols := u.inputContentCols()
	prevRows := in.visualRows(cols)
	in.insertNewline()
	if in.visualRows(cols) != prevRows {
		u.maybeResizeForInput()
		u.markShellDirty()
		u.requestPaint()
		return
	}
	u.requestInputPaint()
}

// markShellDirty forces a full shell repaint on the next paint cycle.
func (u *winUI) markShellDirty() {
	if u == nil {
		return
	}
	u.inputOnlyDirty = false
}

func (u *winUI) queueBytes(tabID int)  { postBytes(u, tabID) }
func (u *winUI) queueClosed(tabID int) { postClosed(u, tabID) }
func (u *winUI) isAlive() bool         { return u != nil && u.alive.Load() }
func (u *winUI) windowReady() bool     { return u != nil && u.hwnd != 0 }

func (u *winUI) activeTab() *tab {
	if p := u.activePage(); p != nil {
		return p.focused()
	}
	// Legacy fallback during init before pages are set.
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

// anyTabBusy is true when at least one pane should show a spinning activity mark.
func (u *winUI) anyTabBusy() bool {
	for _, t := range u.allPanes() {
		if t != nil && t.busy() {
			return true
		}
	}
	return false
}

func (u *winUI) syncChrome() {
	// One chrome strip entry per page (split panes share a strip tab).
	src := u.pages
	if len(src) == 0 {
		// Fallback: flat tabs as pages of one.
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
			ID:        p.id,
			Title:     title,
			Alive:     alive,
			AltScreen: alt,
			Busy:      busy,
		}
	}
	// Only invalidate the chrome cell cache when something visible changed.
	// (Calling this every paint used to force a full Lip Gloss re-render.)
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

func (u *winUI) markChromeDirty() {
	u.chromeDirty = true
	// Only dirty the floating card when it is actually open. Setting overlayDirty
	// while closed used to stick forever (nothing clears it without paintOverlay),
	// which forced every Warp-bar keystroke into a full shell repaint.
	if u.chrome.OverlayOpen() {
		u.overlayDirty = true
	}
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
			oldFace, oldBold, oldCJK, oldSym := u.font, u.fontBold, u.cjkFont, u.symFont
			u.font, u.fontBold = newFace, newBold
			u.cjkFont = createCJKFont(cfg.FontSizePx)
			u.symFont = createSymbolFont(cfg.FontSizePx)
			if oldFace != 0 {
				win.DeleteObject(win.HGDIOBJ(oldFace))
			}
			if oldBold != 0 {
				win.DeleteObject(win.HGDIOBJ(oldBold))
			}
			if oldCJK != 0 {
				win.DeleteObject(win.HGDIOBJ(oldCJK))
			}
			if oldSym != 0 {
				win.DeleteObject(win.HGDIOBJ(oldSym))
			}
			u.probeKeyGlyphs()
			got := fontFaceName(u.font)
			// Remeasure + ConPTY resize off this stack (click/key/preview).
			u.metricW, u.metricH = 0, 0
			u.postLayoutSettle()
			log.Info("font applied", "face", cfg.FontFace, "px", cfg.FontSizePx, "got", got,
				"cjk", fontFaceName(u.cjkFont), "sym", fontFaceName(u.symFont))
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

// zoomFont steps UI font size by delta and persists (same path as settings apply).
func (u *winUI) zoomFont(delta int) {
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
	u.chrome.ApplyFontSize(cfg.FontSizePx)
	// Full live apply + save (font rebuild needs GDI path).
	u.applyConfigLive(cfg)
	cfg.Window = u.cfg.Window
	u.cfg = cfg
	if err := config.Save(cfg); err != nil {
		log.Error("zoom save failed", "err", err)
		u.toast("save failed")
		return
	}
	u.toast(fmt.Sprintf("font %dpx", cfg.FontSizePx))
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

func (u *winUI) zoomFontReset() {
	if u == nil {
		return
	}
	if u.cfg.FontSizePx == config.DefaultFontSizePx {
		u.toast(fmt.Sprintf("font %dpx (default)", config.DefaultFontSizePx))
		return
	}
	cfg := u.cfg
	cfg.FontSizePx = config.DefaultFontSizePx
	u.chrome.ApplyFontSize(cfg.FontSizePx)
	u.applyConfigLive(cfg)
	cfg.Window = u.cfg.Window
	u.cfg = cfg
	if err := config.Save(cfg); err != nil {
		log.Error("zoom save failed", "err", err)
		u.toast("save failed")
		return
	}
	u.toast(fmt.Sprintf("font %dpx (reset)", cfg.FontSizePx))
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
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
	// Drop any previous modal cell cache before rebuild (help ↔ palette thrash).
	u.overlayCells = nil
	u.overlayDirty = true
	u.overlaySceneReady = false // first paint rebuilds dim+neko into memDC
	u.sashDrag = nil
	r := u.chrome.UpdateChrome(chrome.OpenPaletteMsg{})
	u.chrome = r.Model
	// Warm OverlayView once here under recover — first paint then only composites.
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

// openHelpSafe opens the shortcuts card (same cache-clear discipline as palette).
func (u *winUI) openHelpSafe() {
	defer applog.Recover("openHelpSafe", false)
	if u == nil || !u.alive.Load() {
		return
	}
	u.overlayCells = nil
	u.overlayDirty = true
	u.overlaySceneReady = false
	u.sashDrag = nil
	r := u.chrome.UpdateChrome(chrome.OpenHelpMsg{})
	u.chrome = r.Model
	func() {
		defer applog.Recover("openHelpSafe.view", false)
		_ = u.chrome.OverlayView()
	}()
	u.markChromeDirty()
	u.chromePx = u.chromePixelHeight()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
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

// toast sets a short-lived status line under the tab strip (UI thread only).
func (u *winUI) toast(msg string) {
	if u == nil {
		return
	}
	msg = strings.TrimSpace(msg)
	prevRows := u.chrome.RowCount()
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg(msg)).Model
	// Update results need a bit longer to read than split toasts.
	dur := 2500 * time.Millisecond
	if strings.Contains(msg, "update") || strings.Contains(msg, "up to date") ||
		strings.Contains(msg, "installing") || strings.Contains(msg, "opened") {
		dur = 4 * time.Second
	}
	u.statusUntil = time.Now().Add(dur)
	u.markChromeDirty()
	// Extra strip row for status — settle layout so toast has its own band.
	if u.chrome.RowCount() != prevRows && u.hwnd != 0 {
		u.postLayoutSettle()
	}
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
	log.Debug("toast", "msg", msg, "rows", u.chrome.RowCount())
}

// postToast queues a toast for the UI thread (safe from background goroutines).
func (u *winUI) postToast(msg string) {
	if u == nil {
		return
	}
	u.toastMu.Lock()
	u.toastPending = msg
	u.toastMu.Unlock()
	if u.hwnd != 0 {
		if win.PostMessage(u.hwnd, wmSuzuriToast, 0, 0) == 0 {
			// Best-effort: if post fails, try inline (may race if off UI thread).
			u.toast(msg)
		}
	}
}

// postUpdateOffer opens the install-confirm modal on the UI thread (once).
func (u *winUI) postUpdateOffer(version string) {
	if u == nil || version == "" {
		return
	}
	// Already showing update confirm — do not stack another.
	if u.chrome.ConfirmOpen {
		return
	}
	u.toastMu.Lock()
	// Drop duplicate offers still in the message queue.
	if u.updateOfferVer == version {
		u.toastMu.Unlock()
		return
	}
	u.updateOfferVer = version
	u.toastMu.Unlock()
	if u.hwnd != 0 {
		if win.PostMessage(u.hwnd, wmSuzuriUpdateOffer, 0, 0) == 0 {
			u.openUpdateConfirm(version)
		}
	}
}

func (u *winUI) openUpdateConfirm(version string) {
	if u == nil || version == "" {
		return
	}
	if u.chrome.ConfirmOpen {
		return
	}
	r := u.chrome.UpdateChrome(chrome.OpenConfirmUpdateMsg{Version: version})
	u.chrome = r.Model
	u.overlayCells = nil
	u.overlayDirty = true
	u.overlaySceneReady = false
	u.markChromeDirty()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

func (u *winUI) startUpdateCheck() {
	runUpdateCheck(updateCheckHooks{
		toast:       u.postToast,
		offerUpdate: u.postUpdateOffer,
	})
}

func (u *winUI) clearToastIfDue() {
	if u.statusUntil.IsZero() {
		return
	}
	if time.Now().Before(u.statusUntil) {
		return
	}
	u.statusUntil = time.Time{}
	prevRows := u.chrome.RowCount()
	u.chrome = u.chrome.UpdateChrome(chrome.StatusMsg("")).Model
	u.markChromeDirty()
	// Toast band changes chrome height → shell rows; settle via the coalesced
	// path (defers under dual Grok I/O instead of ResizePseudoConsole mid-stream).
	if u.chrome.RowCount() != prevRows && u.hwnd != 0 {
		u.postLayoutSettle()
	}
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

// inputBarPixelHeight is kept as a layout signature (sum of active-page pane
// bars). Bars themselves live inside each leaf — shell uses the full height
// under the chrome strip.
func (u *winUI) inputBarPixelHeight() int32 {
	return u.sumActivePaneBarHeights()
}

// sumActivePaneBarHeights totals per-pane bar heights for settle detection.
func (u *winUI) sumActivePaneBarHeights() int32 {
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		// Before first layout: estimate focused pane only (solo).
		t := u.activeTab()
		if t == nil {
			return 0
		}
		cw, ch := u.metricW, u.metricH
		if cw < 1 {
			cw = cellW
		}
		if ch < 1 {
			ch = cellH
		}
		w := u.width
		if w < 1 {
			w = int32(u.cols) * cw
		}
		return paneInputBarPixelHeight(t, w, cw, ch)
	}
	var sum int32
	for _, g := range layouts {
		sum += g.barH
	}
	return sum
}

// inputBarCwd is the shortened path shown above the command line (empty if unknown).
func (u *winUI) inputBarCwd() string {
	t := u.activeTab()
	if t == nil {
		return ""
	}
	return displayPath(t.cwd)
}

// appOwnsKeyboard is true when the active tab's full-screen app should receive
// raw keys (alt-screen). Host chrome shortcuts still win first.
func (u *winUI) appOwnsKeyboard() bool {
	t := u.activeTab()
	return t != nil && t.altScreen()
}

// maybeResizeForInput recomputes shell rows when a pane bar height changes.
// No-ops when geometry is unchanged (avoids ConPTY resize thrash on every Enter).
// When panes stream, paint-only + defer ConPTY settle (never ResizePseudoConsole hot).
func (u *winUI) maybeResizeForInput() {
	if u == nil || u.width < 1 || u.height < 1 {
		return
	}
	// Probe layout with current input state; only settle if VT sizes move.
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	sx, sy, sw, sh := u.shellRect(u.width, u.height)
	need := false
	if p := u.activePage(); p != nil && p.root != nil {
		geoms := layoutPage(p.root, sx, sy, sw, sh, cw, ch, p.focusID).leaves
		for _, g := range geoms {
			if g.pane != nil && (g.pane.lastCols != g.cols || g.pane.lastRows != g.rows) {
				need = true
				break
			}
		}
	} else {
		need = true
	}
	if !need {
		return
	}
	if u.anyPaneConPtyBusy() {
		u.markLayoutDeferred()
		u.relayoutActivePaintOnly()
		return
	}
	u.applyClientSize(u.width, u.height)
}

// shellBottomY is the exclusive bottom of the shell region (client bottom).
// Input bars are painted inside each pane, not as a global strip.
func (u *winUI) shellBottomY(clientH int32) int32 {
	if clientH < u.shellPadY()+int32(cellH) {
		return clientH
	}
	return clientH
}

func (u *winUI) applyChromeAction(r chrome.Result) {
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
				setWindowTitle(u.hwnd, "suzuri — "+t.title)
			}
			u.syncChrome()
			u.postLayoutSettle()
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
	case chrome.ActionZoomIn:
		u.zoomFont(+1)
	case chrome.ActionZoomOut:
		u.zoomFont(-1)
	case chrome.ActionZoomReset:
		u.zoomFontReset()
	case chrome.ActionReplayIntro:
		u.replayIntro()
	case chrome.ActionCheckUpdates:
		u.startUpdateCheck()
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
		u.overlaySceneReady = false
		u.syncFileDropAccept()
		if u.hwnd != 0 {
			win.InvalidateRect(u.hwnd, nil, false)
		}
	case chrome.ActionOpenTransferReceive:
		r2 := u.chrome.UpdateChrome(chrome.OpenTransferPromptMsg{Mode: chrome.TransferModeReceive})
		u.chrome = r2.Model
		u.overlayCells = nil
		u.overlayDirty = true
		u.overlaySceneReady = false
		u.syncFileDropAccept()
		if u.hwnd != 0 {
			win.InvalidateRect(u.hwnd, nil, false)
		}
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
	u.syncFileDropAccept()
}

// syncFileDropAccept enables WM_DROPFILES only while Send-file prompt is open.
func (u *winUI) syncFileDropAccept() {
	if u == nil || u.hwnd == 0 {
		return
	}
	want := u.chrome.AcceptsFileDrop()
	if want == u.fileDropAccept {
		return
	}
	u.fileDropAccept = want
	setWindowFileDropAccept(u.hwnd, want)
	if want {
		// Prompt UI already shows drop zone; OS will allow the drop cursor.
		log.Debug("file drop accept on (send prompt)")
	} else {
		log.Debug("file drop accept off")
	}
}

// transferHost implementation for winUI.
func (u *winUI) postTransferStatus(msg chrome.TransferStatusMsg) {
	if u == nil {
		return
	}
	// Reuse toast queue machinery: post a custom work item via toast channel pattern.
	// Progress can be frequent — always hop to UI thread with PostMessage when possible.
	u.xferMu.Lock()
	u.xferPending = append(u.xferPending, msg)
	u.xferMu.Unlock()
	if u.hwnd != 0 {
		_ = win.PostMessage(u.hwnd, wmSuzuriTransfer, 0, 0)
		return
	}
	u.drainTransferStatus()
}

func (u *winUI) drainTransferStatus() {
	u.xferMu.Lock()
	pending := u.xferPending
	u.xferPending = nil
	u.xferMu.Unlock()
	for _, msg := range pending {
		r := u.chrome.UpdateChrome(msg)
		u.chrome = r.Model
	}
	if len(pending) > 0 {
		u.overlayCells = nil
		u.overlayDirty = true
		u.overlaySceneReady = false
		u.markChromeDirty()
		if u.hwnd != 0 {
			win.InvalidateRect(u.hwnd, nil, false)
		}
	}
}

func (u *winUI) copyText(s string) {
	if u == nil {
		return
	}
	_ = setClipboardText(u.hwnd, s)
}

func (u *winUI) defaultReceiveDir() string {
	return defaultDownloadDir()
}

// openRenameUI seeds and opens the rename dialog for a pane or strip tab.
func (u *winUI) openRenameUI(target chrome.RenameTarget) {
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
	u.overlaySceneReady = false
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

// applyRename sets a custom pane or page title (empty clears the lock).
// Pane renames never touch page.userTitle when multi-pane (Grok/OSC only
// rename panes). Solo pages keep strip in sync with the only pane name.
func (u *winUI) applyRename(target chrome.RenameTarget, name string) {
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
	if t := u.activeTab(); t != nil {
		setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
	}
	u.markChromeDirty()
	u.toast("renamed")
}

// replayIntro restarts the configured startup curtain (matrix / ripple / none).
func (u *winUI) replayIntro() {
	if u == nil {
		return
	}
	u.beginIntro(true)
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, false)
	}
}

// beginIntro arms the startup curtain. When shell background rain is already
// on, matrix intro is skipped (redundant with always-on rain).
func (u *winUI) beginIntro(replay bool) {
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

func (u *winUI) loop() error {
	// Must stay on this OS thread for the life of the HWND (see main).
	runtime.LockOSThread()

	hInst := win.GetModuleHandle(nil)
	if hInst == 0 {
		return lastErr("GetModuleHandle")
	}

	cname, _ := syscall.UTF16PtrFromString(className)
	title, _ := syscall.UTF16PtrFromString(appTitle)

	// Glowy 硯 app icon (PE resource via rsrc_windows_*.syso, or embedded .ico).
	iconBig, iconSm := loadAppIcons(hInst)

	// Stable callback: keep ui pinned via global map so GC never collects it
	// while Win32 still has the WndProc pointer.
	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		// CS_DBLCLKS so double-click on tabs / pane titles can open rename.
		Style:         win.CS_DBLCLKS,
		LpfnWndProc:   wndProcCallback,
		HInstance:     hInst,
		LpszClassName: cname,
		HIcon:         iconBig,
		HIconSm:       iconSm,
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
	// Ensure title bar / taskbar pick up the icon even if class was re-registered.
	applyWindowIcons(hwnd, iconBig, iconSm)
	u.font = createFontFor(u.cfg, false)
	u.fontBold = createFontFor(u.cfg, true)
	u.cjkFont = createCJKFont(u.cfg.FontSizePx)
	u.symFont = createSymbolFont(u.cfg.FontSizePx)
	u.probeKeyGlyphs()
	face := fontFaceName(u.font)
	log.Info("window created", "hwnd", uintptr(hwnd), "font", face, "want", u.cfg.FontFace,
		"cjk", fontFaceName(u.cjkFont), "sym", fontFaceName(u.symFont))
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

	// Startup curtain (matrix / ripple / none). Matrix skipped if shell rain is on.
	u.beginIntro(false)

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
	// Startup update check: toast "checking…" then confirm before any install.
	scheduleStartupUpdateCheck(u.postToast, u.postUpdateOffer)

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
	if u == nil || u.hwnd == 0 {
		log.Warn("postClosed skipped", "tab", tabID, "reason", "no hwnd")
		return
	}
	// Flush before PostMessage so a native crash on the close path still leaves a trail.
	log.Info("postClosed", "tab", tabID)
	applog.Sync()
	if win.PostMessage(u.hwnd, wmSuzuriClosed, uintptr(tabID), 0) == 0 {
		log.Warn("postClosed PostMessage failed", "tab", tabID)
	}
}

func (u *winUI) sendKey(b []byte) {
	if t := u.activeTab(); t != nil {
		t.sendKey(b)
	}
}

func (u *winUI) barCols(tab *tab) int {
	cols := u.cols
	if tab != nil {
		if g := u.paneGeomFor(tab.id); g != nil && g.cols > 0 {
			return g.cols
		}
	}
	return cols
}

func (u *winUI) submitBarLine(tab *tab, line string) {
	if u == nil || tab == nil {
		return
	}
	submitBarLine(tab, line, u.barCols(tab), u.toast)
	u.publishBridgeSnapshot()
}

func (u *winUI) tryFlushCmdQueue(tab *tab) {
	if u == nil || tab == nil {
		return
	}
	if tryFlushCmdQueue(tab, u.barCols(tab), u.toast) {
		u.publishBridgeSnapshot()
		u.markShellDirty()
		u.requestPaint()
	}
}

func (u *winUI) blinkLoop() {
	t := time.NewTicker(cursorBlinkTick)
	defer t.Stop()
	// Full-rate ticks when focused (or when AnimateUnfocused is on). Skipping
	// entirely while backgrounded freezes rain/spinners — optional for low CPU.
	for range t.C {
		if !u.alive.Load() || u.hwnd == 0 {
			return
		}
		if !u.cfg.AnimateUnfocused && win.GetForegroundWindow() != u.hwnd {
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
	// Host cwd OSC from quiet prompt (strip before VT so it never paints).
	if clean, path, ok := stripAndTakeCwd(data); ok {
		prevPath := t.cwd
		t.setCwd(path)
		data = clean
		// Path above the input bar — reflow only if cwd row presence changes
		// (empty ↔ non-empty). Spammed prompts used to post settle every OSC.
		if u.activeTab() == t && !t.altScreen() {
			was := displayPath(prevPath) != ""
			now := displayPath(t.cwd) != ""
			if was != now {
				u.maybeResizeForInput()
			}
		}
	} else {
		data = clean
	}
	// Inline images: iTerm OSC 1337, suzuri OSC 7879, and path heuristics.
	// Attached into scrollback under the current stream (not a sticky overlay).
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
	// Visible if this pane is on the active page (any leaf, not only focused).
	visible := u.paneVisible(t.id)
	if len(data) == 0 {
		// More may have been queued; re-arm if needed.
		t.inMu.Lock()
		more := len(t.inBuf) > 0
		t.inMu.Unlock()
		if more {
			t.postBytes(u)
		}
		if visible {
			u.markShellDirty()
			u.requestPaint()
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
	u.markShellDirty()
	// No host image injection on alt-screen (Grok) — use click → modal instead.
	if t.sb.atBottom() {
		t.sb.stickBottom()
	}
	// Grok (and others) set OSC 0/2 window title with a braille spinner while
	// working; strip for display, keep titleBusy for the tab strip spinner.
	if title := t.term.Title(); title != "" {
		prevBusy := t.busy()
		titleChanged := t.applyTitle(title)
		if t.busy() != prevBusy {
			u.chromeDirty = true
			u.syncChrome()
		} else if titleChanged {
			// Real title text change (not a spinner frame) — refresh strip labels.
			u.chromeDirty = true
		}
		// Never SetWindowText on spinner frames — that thrash was part of the
		// dual-Grok crash path. Use the stripped display title only.
		if titleChanged && u.activeTab() == t {
			setWindowTitle(u.hwnd, "suzuri — "+t.title)
		}
	}
	// Alt-screen enter/leave: hide/show Warp bar. Never ConPTY-resize here —
	// dual Grok + ResizePseudoConsole mid-stream hard-crashes (log dies with
	// no panic after "layout settle"). Paint-only now; settle when idle.
	nowAlt := t.altScreen()
	if nowAlt != t.wasAlt {
		if t.wasAlt {
			resetHostAfterAltApp(t.term)
			if t.kittyGfx != nil {
				t.kittyGfx.clear()
			}
			clearKittyHBMCache(t.id)
			t.markShellIdle()
		}
		t.wasAlt = nowAlt
		log.Info("alt screen", "tab", t.id, "on", nowAlt)
		u.onAltScreenToggled(t)
	}
	t.maybeReleaseBarAwaiting()
	u.tryFlushCmdQueue(t)
	// Bridge snapshot is relatively expensive — skip on pure spam frames.
	// (MCP clients still get updates on submit / tab change.)
	if u.bridge != nil && len(data) > 0 {
		// Coalesce: at most ~10/s via existing invalidate path is enough;
		// full snapshot every PTY chunk under spam flooded layout work.
		if u.spinTick%8 == 0 {
			u.publishBridgeSnapshot()
		}
	}
	// Only repaint if this pane is on the visible page. Coalesce — dual busy
	// alt-screen panes used to invalidate hundreds of times per second.
	if visible {
		u.requestPaint()
	}
	t.inMu.Lock()
	more := len(t.inBuf) > 0
	t.inMu.Unlock()
	if more {
		t.postBytes(u)
	}
}

func (u *winUI) tabByID(id int) *tab {
	for _, t := range u.allPanes() {
		if t != nil && t.id == id {
			return t
		}
	}
	return nil
}

// paneVisible is true when pane id is a leaf on the active chrome page.
func (u *winUI) paneVisible(id int) bool {
	p := u.activePage()
	if p == nil {
		return u.activeTab() != nil && u.activeTab().id == id
	}
	return findPane(p.root, id) != nil
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

func (u *winUI) enqueueMCPNotes(req bridge.NotesRequest) bridge.NotesResult {
	if u == nil || !u.alive.Load() || u.hwnd == 0 {
		return bridge.NotesResult{OK: false, Error: "suzuri UI not ready"}
	}
	job := mcpJob{
		notes:    true,
		notesReq: req,
		notesOut: make(chan bridge.NotesResult, 1),
	}
	select {
	case u.mcpJobs <- job:
	default:
		return bridge.NotesResult{OK: false, Error: "mcp notes queue full"}
	}
	if win.PostMessage(u.hwnd, wmSuzuriMCP, 0, 0) == 0 {
		return bridge.NotesResult{OK: false, Error: "post mcp notes job failed"}
	}
	select {
	case res := <-job.notesOut:
		return res
	case <-time.After(5 * time.Second):
		return bridge.NotesResult{OK: false, Error: "mcp notes timed out"}
	}
}

func (u *winUI) enqueueMCPWorkspace(req bridge.WorkspaceRequest) bridge.WorkspaceResult {
	if u == nil || !u.alive.Load() || u.hwnd == 0 {
		return bridge.WorkspaceResult{OK: false, Error: "suzuri UI not ready"}
	}
	job := mcpJob{
		workspace:    true,
		workspaceReq: req,
		workspaceOut: make(chan bridge.WorkspaceResult, 1),
	}
	select {
	case u.mcpJobs <- job:
	default:
		return bridge.WorkspaceResult{OK: false, Error: "mcp workspace queue full"}
	}
	if win.PostMessage(u.hwnd, wmSuzuriMCP, 0, 0) == 0 {
		return bridge.WorkspaceResult{OK: false, Error: "post mcp workspace job failed"}
	}
	select {
	case res := <-job.workspaceOut:
		return res
	case <-time.After(5 * time.Second):
		return bridge.WorkspaceResult{OK: false, Error: "mcp workspace timed out"}
	}
}

func (u *winUI) drainMCPJobs() {
	for {
		select {
		case job := <-u.mcpJobs:
			if job.notes {
				res := runNotesOnChrome(&u.chrome, job.notesReq)
				if u.chrome.NotesOpen {
					u.overlayDirty = true
					// windows uses different dirty flags — requestPaint covers it
				}
				u.markChromeDirty()
				u.requestPaint()
				if job.notesOut != nil {
					job.notesOut <- res
				}
			} else if job.workspace {
				res := runWorkspaceOnChrome(&u.chrome, job.workspaceReq)
				if u.chrome.WorkspaceOpen {
					u.overlayDirty = true
				}
				u.markChromeDirty()
				u.requestPaint()
				if job.workspaceOut != nil {
					job.workspaceOut <- res
				}
			} else {
				err := u.submitOnUIThread(job.tabID, job.line)
				if job.done != nil {
					job.done <- err
				}
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
	u.submitBarLine(t, line)
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
	panes := u.allPanes()
	s := bridge.Snapshot{
		Cols:      u.cols,
		Rows:      u.rows,
		ActiveTab: activeID,
		Tabs:      make([]bridge.TabSnap, 0, len(panes)),
	}
	for _, t := range panes {
		s.Tabs = append(s.Tabs, u.tabSnap(t))
	}
	return s
}

func (u *winUI) tabSnap(t *tab) bridge.TabSnap {
	armed, cmd, phase := t.echo.status()
	// Live text (effective extent).
	liveText := snapshotLiveText(t.term)
	// Viewport as the user sees it (rune grid → strings).
	viewRows := u.rows
	if g := u.paneGeomFor(t.id); g != nil && g.rows > 0 {
		viewRows = g.rows
	}
	view := t.sb.view(t.term, viewRows)
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

// newTabUI opens a chrome tab (single pane). profileName empty uses ActiveProfile.
func (u *winUI) newTabUI(profileName string) {
	if len(u.pages) >= maxTabs {
		log.Warn("max tabs reached", "max", maxTabs)
		u.toast("max tabs")
		return
	}
	if u.paneCount() >= maxPanesTotal {
		u.toast("max panes")
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
	u.addPageWithTab(t)
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

// closeTabUI closes the chrome tab that owns pane/page id.
// id may be a page id or a pane id (strip × / palette close tab).
func (u *winUI) closeTabUI(id int) {
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

// sessionEndedCloseTab closes a pane whose shell process exited (exit/EOF).
// Last pane of last page quits immediately (no confirm) — shell semantics.
func (u *winUI) sessionEndedCloseTab(id int) {
	defer applog.Recover("sessionEndedCloseTab", false)
	u.closePaneUI(id, false)
}

// removeTabAt is retained for compatibility; removes a flat-list pane by index.
// Prefer closePaneUI / removePageAt for page-aware close.
func (u *winUI) removeTabAt(idx int) {
	defer applog.Recover("removeTabAt", false)
	if idx < 0 || idx >= len(u.tabs) {
		return
	}
	t := u.tabs[idx]
	if t != nil {
		u.closePaneUI(t.id, true)
	}
}

func (u *winUI) switchTab(delta int) {
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
		setWindowTitle(u.hwnd, "suzuri — "+t.title)
	}
	u.syncChrome()
	// Defer reflow when switching bar ↔ alt-screen (same ConPTY race as removeTabAt).
	u.postLayoutSettle()
	win.InvalidateRect(u.hwnd, nil, false)
}

// hitTab maps an x pixel to a tab index using chrome.TabBounds (same layout as View).
func (u *winUI) hitTab(px int32) int {
	if len(u.pages) == 0 && len(u.tabs) == 0 {
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

func (u *winUI) hitCaffeine(px int32) bool {
	u.syncChrome()
	cellX := u.pixelToChromeCol(px)
	if cellX < 0 {
		return false
	}
	b := u.chrome.CaffeineBounds()
	return cellX >= b[0] && cellX < b[1]
}

// hitPaneTitleBar returns the multi-pane mini-title row under (px,py), if any.
func (u *winUI) hitPaneTitleBar(px, py int32, layouts []paneGeom) *paneGeom {
	for i := range layouts {
		g := &layouts[i]
		if g.titleH < 1 || g.pane == nil {
			continue
		}
		if px >= g.x && px < g.x+g.w && py >= g.titleY && py < g.titleY+g.titleH {
			return g
		}
	}
	return nil
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
			// Inject async clipboard paste results (image dump runs off-thread).
			u.drainPendingPaste()
			for _, t := range u.allPanes() {
				if t != nil && !t.altScreen() && t.queueLen() > 0 {
					u.tryFlushCmdQueue(t)
				}
			}
			if msg := caffeineTick(u.caffeine); msg != "" {
				u.toast(msg)
				u.markChromeDirty()
				u.syncChrome()
			} else if u.caffeine != nil && u.caffeine.Active() {
				hint := u.caffeine.StripLabel()
				if hint != u.lastCaffeineHint {
					u.lastCaffeineHint = hint
					u.chrome = syncCaffeineChrome(u.chrome, u.caffeine)
					u.chromeDirty = true
				}
			}
			// Ease scrollback visual → offset (macOS does this in ebiten Update;
			// without it, wheel only moves offset and the view stays stuck).
			// Dim modals hide the shell; palette keeps a live shell underneath.
			const dt = float64(cursorBlinkTick) / float64(time.Second)
			needScrollPaint := false
			if !u.dimShellModal() {
				for _, t := range u.allPanes() {
					if t == nil || t.sb == nil {
						continue
					}
					prev := t.sb.visual
					t.sb.tickSmooth(dt)
					if absFloat(t.sb.visual-prev) > 0.01 || absFloat(t.sb.visual-float64(t.sb.offset)) > 0.01 {
						needScrollPaint = true
					}
				}
			}
			// Animate braille (or geometric) busy marks on the tab strip.
			u.spinTick++
			if u.anyTabBusy() && u.spinTick%uint64(tabSpinEveryNTicks) == 0 {
				chrome.AdvanceTabSpinner()
				u.chromeDirty = true
				u.syncChrome()
			}
			// Flush deferred ConPTY settle only when I/O is quiet.
			// Max-wait under load: paint-only reflow (never ResizePseudoConsole
			// mid-stream — force=true hard-killed 0.9.82 under Grok).
			// At most every N blink ticks so we don't thrash PostMessage.
			if u.layoutDeferred && u.spinTick%uint64(tabSpinEveryNTicks*4) == 0 {
				if !u.anyPaneConPtyBusy() {
					u.postLayoutSettle()
				} else if !u.layoutDeferredAt.IsZero() &&
					time.Since(u.layoutDeferredAt) >= layoutDeferMaxWait {
					u.relayoutActivePaintOnly()
					u.requestPaint()
					// Re-arm max-wait so we keep refreshing paint, not ConPTY.
					u.layoutDeferredAt = time.Now()
					if u.spinTick%64 == 0 {
						log.Info("layout max-wait paint-only (still busy, skip ConPTY)",
							"w", u.width, "h", u.height, "panes", u.trailPaneSummary())
						applog.Trail("layout max-wait paint-only",
							"w", u.width, "h", u.height, "panes", u.trailPaneSummary())
					}
				}
			}
			// Paint policy (macOS input-only parity):
			// - Scroll / shell anim / alt-screen cursor → full paint
			// - Warp bar caret / sticky inputOnlyDirty → bar-only (no grid blit)
			// Full 25fps grid+matrix paints made bar typing feel laggy.
			if u.dimShellModal() {
				if u.chrome.SettingsOpen {
					if u.spinTick%uint64(tabSpinEveryNTicks) == 0 {
						u.requestPaint()
					}
				} else if u.chromeDirty {
					u.requestPaint()
				}
			} else if needScrollPaint {
				u.markShellDirty()
				u.requestPaint()
			} else if u.inputOnlyDirty {
				// Sticky bar-only after typing. If ambient is on, unstick every
				// few ticks so underlays keep animating on the normal shell
				// (not only under Grok) without full-painting every keystroke.
				if u.shellAmbientOn() && u.spinTick%uint64(tabSpinEveryNTicks*3) == 0 {
					u.markShellDirty()
				}
				u.requestPaint()
			} else if u.chrome.OverlayOpen() {
				// Palette/help float over a live shell — need full composite.
				u.requestPaint()
			} else if u.needsShellAnimPaint() {
				// Ambient / alt caret / intro: always full paint.
				// Never call requestInputPaint here — sticky input-only freezes ambient.
				u.requestPaint()
			} else {
				// Idle shell, no ambient: only pulse the Warp caret.
				u.requestInputPaint()
			}
		}
		return 0

	case wmSuzuriClosed:
		// Shell exited (e.g. `exit`) — close tab; last tab quits (no confirm).
		id := int(wParam)
		log.Info("wmSuzuriClosed", "tab", id, "tabs", len(u.tabs))
		applog.Sync()
		if u.tabByID(id) != nil {
			log.Info("shell session ended — closing tab", "tab", id, "tabs", len(u.tabs))
			applog.Sync()
			u.sessionEndedCloseTab(id)
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

	case wmSuzuriToast:
		u.toastMu.Lock()
		msg := u.toastPending
		u.toastPending = ""
		u.toastMu.Unlock()
		if msg != "" {
			u.toast(msg)
		}
		return 0

	case wmSuzuriUpdateOffer:
		u.toastMu.Lock()
		ver := u.updateOfferVer
		u.updateOfferVer = ""
		u.toastMu.Unlock()
		if ver != "" && !u.chrome.ConfirmOpen {
			u.openUpdateConfirm(ver)
		}
		return 0

	case win.WM_DROPFILES:
		// Only armed while Send-file prompt is open (DragAcceptFiles).
		hDrop := win.HDROP(wParam)
		if !u.chrome.AcceptsFileDrop() {
			dragFinish(hDrop)
			return 0
		}
		// Hover-style feedback then commit paths.
		rHover := u.chrome.UpdateChrome(chrome.TransferDropHoverMsg{Hover: true})
		u.chrome = rHover.Model
		paths := pathsFromHDROP(hDrop)
		r := u.chrome.UpdateChrome(chrome.TransferDropPathsMsg{Paths: paths})
		u.chrome = r.Model
		u.overlayCells = nil
		u.overlayDirty = true
		u.overlaySceneReady = false
		u.applyChromeAction(r)
		u.syncFileDropAccept()
		u.markChromeDirty()
		if u.hwnd != 0 {
			win.InvalidateRect(u.hwnd, nil, false)
		}
		return 0

	case wmSuzuriOpenPalette:
		u.openPaletteSafe()
		return 0

	case wmSuzuriTransfer:
		u.drainTransferStatus()
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

	case win.WM_SIZING:
		// Soft magnetic snap to ½ / ⅓ / ¼ / ⅔ / ¾ / full of the monitor work area.
		// Small threshold so the magnet lets go easily when dragging past.
		if lParam == 0 {
			return 1
		}
		r := (*win.RECT)(unsafe.Pointer(lParam))
		if workL, workT, workR, workB, ok := u.monitorWorkArea(); ok {
			l, top, rt, bot := int(r.Left), int(r.Top), int(r.Right), int(r.Bottom)
			if softSnapRect(&l, &top, &rt, &bot, int(wParam), workL, workT, workR, workB, softSnapThresholdPx) {
				r.Left, r.Top, r.Right, r.Bottom = int32(l), int32(top), int32(rt), int32(bot)
			}
		}
		return 1 // TRUE = we may have modified the rect

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

	case win.WM_MOVE:
		// Win+Shift+←/→ (move monitor) and some snap paths move without a useful
		// SIZE_RESTORED pair. Skip during interactive drag (EXITSIZEMOVE saves).
		if !u.inSizeMove && u.alive.Load() {
			u.persistWindowPlacement(false)
		}
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
		// Maximize / Aero snap / Win+Arrow / programmatic size often skip ENTERSIZEMOVE.
		// Still defer heavy work off the WM_SIZE stack.
		u.width, u.height = w, h
		u.postLayoutSettle()
		// Persist every non-minimized size change (snap, maximize, restore).
		u.persistWindowPlacement(false)
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
		// Charm overlay (palette, rename, notes, workspace, transfer) owns printable text.
		if u.chrome.OverlayOpen() {
			// Transfer *panel* needs runes too (c = copy ticket). Prompt typing
			// already used this path; panel was missing → c silently did nothing.
			// Workspace compose also needs runes (keys never reached handleWorkspaceKey).
			if (u.chrome.PaletteOpen || u.chrome.RenameOpen || u.chrome.NotesOpen ||
				u.chrome.WorkspaceOpen || u.chrome.TransferPromptOpen ||
				u.chrome.TransferPanelOpen) && ch >= 32 && ch != 0x7f {
				km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
				r := u.chrome.UpdateChrome(km)
				u.chrome = r.Model
				// Copy-ticket (and any other host action from a printable key).
				if r.Action != chrome.ActionNone {
					u.applyChromeAction(r)
				}
				// Filter / rename / notes / workspace / transfer typing: only dirty the overlay.
				u.overlayDirty = true
				u.overlayCells = nil
				if u.chrome.NotesOpen || u.chrome.NotesDirty() {
					u.persistNotesIfDirty()
				}
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
					u.markShellDirty() // bar height change reflows shell
					u.requestPaint()
				} else {
					u.requestInputPaint()
				}
			} else {
				u.requestPaint()
			}
		}
		return 0

	case win.WM_KEYDOWN:
		ctrl := win.GetKeyState(win.VK_CONTROL) < 0
		shift := win.GetKeyState(win.VK_SHIFT) < 0
		alt := win.GetKeyState(win.VK_MENU) < 0
		tab := u.activeTab()

		// Ctrl+Shift+M — toggle notes (works even while notes overlay is open).
		if ctrl && shift && !alt && (wParam == 'M' || wParam == 'm') {
			r := u.chrome.UpdateChrome(chrome.ToggleNotesMsg{})
			u.chrome = r.Model
			u.overlayCells = nil
			u.overlayDirty = true
			u.overlaySceneReady = false
			u.markChromeDirty()
			u.persistNotesIfDirty()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		// Charm palette / settings / transfer own keys while open (text via WM_CHAR).
		if u.chrome.OverlayOpen() {
			// Notes clipboard + bank shortcuts need host (CF_UNICODETEXT / chords).
			if u.chrome.NotesOpen && ctrl && !alt {
				// Undo / redo before the !shift gate (redo uses Shift+Z).
				if wParam == 'Z' || wParam == 'z' {
					m := u.chrome
					if shift {
						_ = m.NotesRedo()
					} else {
						_ = m.NotesUndo()
					}
					u.chrome = m
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					u.persistNotesIfDirty()
					return 0
				}
				if !shift && (wParam == 'Y' || wParam == 'y') {
					m := u.chrome
					_ = m.NotesRedo()
					u.chrome = m
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					u.persistNotesIfDirty()
					return 0
				}
				if !shift {
					switch wParam {
					case 'C', 'c':
						if s := u.chrome.NotesSelectedText(); s != "" {
							_ = setClipboardText(hwnd, s)
						}
						km := tea.KeyMsg{Type: tea.KeyCtrlC}
						r := u.chrome.UpdateChrome(km)
						u.chrome = r.Model
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						u.persistNotesIfDirty()
						return 0
					case 'X', 'x':
						if s := u.chrome.NotesSelectedText(); s != "" {
							_ = setClipboardText(hwnd, s)
						}
						km := tea.KeyMsg{Type: tea.KeyCtrlX}
						r := u.chrome.UpdateChrome(km)
						u.chrome = r.Model
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						u.persistNotesIfDirty()
						return 0
					case 'V', 'v':
						if s, err := getClipboardText(hwnd); err == nil && s != "" {
							m := u.chrome
							m.NotesPaste(s)
							u.chrome = m
							u.overlayDirty = true
							u.overlayCells = nil
							win.InvalidateRect(hwnd, nil, false)
							u.persistNotesIfDirty()
						}
						return 0
					case 'A', 'a':
						km := tea.KeyMsg{Type: tea.KeyCtrlA}
						r := u.chrome.UpdateChrome(km)
						u.chrome = r.Model
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						return 0
					case 'N', 'n':
						// New note (works from list or editor).
						km := tea.KeyMsg{Type: tea.KeyCtrlN}
						r := u.chrome.UpdateChrome(km)
						u.chrome = r.Model
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						u.persistNotesIfDirty()
						return 0
					}
					// Ctrl+Backspace / Ctrl+Delete: delete word.
					if wParam == win.VK_BACK {
						m := u.chrome
						m.NotesDeleteWord(-1)
						u.chrome = m
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						u.persistNotesIfDirty()
						return 0
					}
					if wParam == win.VK_DELETE {
						m := u.chrome
						m.NotesDeleteWord(1)
						u.chrome = m
						u.overlayDirty = true
						u.overlayCells = nil
						win.InvalidateRect(hwnd, nil, false)
						u.persistNotesIfDirty()
						return 0
					}
				}
			}
			// Transfer path/ticket prompt: Ctrl+V paste (tickets are long).
			if u.chrome.TransferPromptOpen && ctrl && !alt && !shift &&
				(wParam == 'V' || wParam == 'v') {
				u.pasteClipboard()
				return 0
			}
			// Workspace compose: Ctrl+V paste into message field.
			if u.chrome.WorkspaceOpen && ctrl && !alt && !shift &&
				(wParam == 'V' || wParam == 'v') {
				u.pasteClipboard()
				return 0
			}
			// Workspace undo/redo — teaKeyFromWin has no ctrl-letter map.
			if u.chrome.WorkspaceOpen && ctrl && !alt {
				if wParam == 'Z' || wParam == 'z' {
					var km tea.KeyMsg
					if shift {
						km = tea.KeyMsg{Type: tea.KeyCtrlY}
					} else {
						km = tea.KeyMsg{Type: tea.KeyCtrlZ}
					}
					r := u.chrome.UpdateChrome(km)
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					return 0
				}
				if !shift && (wParam == 'Y' || wParam == 'y') {
					r := u.chrome.UpdateChrome(tea.KeyMsg{Type: tea.KeyCtrlY})
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					return 0
				}
			}
			if km := teaKeyFromWin(wParam, ctrl, shift); km != nil {
				r := u.chrome.UpdateChrome(*km)
				u.chrome = r.Model
				u.applyChromeAction(r)
				u.syncFileDropAccept()
				// Palette / rename / notes / workspace: only dirty overlay.
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
				u.persistNotesIfDirty()
				win.InvalidateRect(hwnd, nil, false)
			}
			return 0
		}
		// F2 — rename focused pane (custom title; empty clears).
		if !ctrl && !shift && !alt && wParam == win.VK_F2 {
			u.openRenameUI(chrome.RenameTargetPane)
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
			u.openHelpSafe()
			return 0
		}
		// Zoom: Ctrl++ / Ctrl+- / Ctrl+0 (and numpad ±). Works over overlays.
		if ctrl && !alt {
			switch wParam {
			case win.VK_OEM_PLUS, win.VK_ADD: // =/+ key or numpad +
				// Shift optional: Ctrl+= and Ctrl+Shift+= both zoom in.
				u.zoomFont(+1)
				return 0
			case win.VK_OEM_MINUS, win.VK_SUBTRACT:
				if !shift {
					u.zoomFont(-1)
					return 0
				}
			case '0', win.VK_NUMPAD0:
				if !shift {
					u.zoomFontReset()
					return 0
				}
			}
		}
		if ctrl && shift && (wParam == 'T' || wParam == 't') {
			u.newTabUI("")
			return 0
		}
		if ctrl && shift && (wParam == 'N' || wParam == 'n') {
			openNewWindow()
			return 0
		}
		// Split panes (Windows Terminal-ish: Alt+Shift+± / Alt+arrows).
		// Also Ctrl+Shift+D (right) / Ctrl+Shift+E (down) as mnemonic backups.
		if alt && shift && !ctrl {
			switch wParam {
			case win.VK_OEM_PLUS: // = / +
				u.splitActive(splitVert)
				return 0
			case win.VK_OEM_MINUS: // -
				u.splitActive(splitHoriz)
				return 0
			}
		}
		if ctrl && shift && !alt {
			switch wParam {
			case 'D', 'd':
				u.splitActive(splitVert)
				return 0
			case 'E', 'e':
				u.splitActive(splitHoriz)
				return 0
			}
		}
		// Pane focus: Alt+arrows (Windows Terminal style). Word-jump is Ctrl+arrows
		// (handled in the input bar / notes paths below). Not Alt+Shift (splits).
		if alt && !shift && !ctrl {
			switch wParam {
			case win.VK_LEFT:
				u.focusPaneDir(0)
				return 0
			case win.VK_RIGHT:
				u.focusPaneDir(1)
				return 0
			case win.VK_UP:
				u.focusPaneDir(2)
				return 0
			case win.VK_DOWN:
				u.focusPaneDir(3)
				return 0
			}
		}
		// Ctrl+W closes the focused pane. Last pane in a multi-pane tab
		// collapses the chrome tab; last pane of the last tab arms confirm-quit
		// (see closePaneUI → closePageAt). No separate "close tab" chord.
		if ctrl && !shift && (wParam == 'W' || wParam == 'w') {
			if tab != nil {
				u.closePaneUI(tab.id, true)
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
			n := len(u.pages)
			if n == 0 {
				n = len(u.tabs)
			}
			if i >= 0 && i < n {
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

		// Image lightbox owns Esc before alt-screen apps.
		if wParam == win.VK_ESCAPE && u.modalImage != nil {
			u.modalImage = nil
			win.InvalidateRect(hwnd, nil, false)
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
			// Route through pasteClipboard so image + bracketed paste match darwin.
			if (ctrl && !shift && (wParam == 'V' || wParam == 'v')) ||
				(shift && !ctrl && wParam == win.VK_INSERT) {
				u.pasteClipboard()
				return 0
			}
			// Escape: ignore auto-repeat (lParam bit 30 = previous key state).
			// Hold-Esc after notes/palette dismiss must not flood the PTY.
			if wParam == win.VK_ESCAPE && (lParam&(1<<30)) != 0 {
				return 0
			}
			if b := ptyKeyFromWin(tab.term, &tab.kitty, wParam, ctrl, shift, alt); len(b) > 0 {
				u.sendKey(b)
			}
			return 0
		}

		// Ctrl+C: copy selection, else clear bar, else interrupt PTY + drop queue.
		if ctrl && !shift && (wParam == 'C' || wParam == 'c') {
			in := u.activeInput()
			if tab != nil && !tab.sel.empty() {
				u.copySelection()
			} else if in != nil && len(in.runes) > 0 {
				in.clear()
				u.maybeResizeForInput()
				u.markShellDirty()
				u.requestPaint()
			} else {
				if tab != nil {
					if n := tab.clearCmdQueue(); n > 0 {
						u.toast(fmt.Sprintf("cleared %d queued", n))
					}
				}
				u.sendKey([]byte{0x03})
			}
			return 0
		}
		// Ctrl+V: paste into input bar.
		if ctrl && !shift && (wParam == 'V' || wParam == 'v') {
			u.pasteClipboard()
			return 0
		}
		// Shift+Enter / Alt+Enter → multiline in Warp bar BEFORE activeInput nil
		// checks and before any path that might DefWindowProc (system beep).
		// Use L/R shift bits too — some layouts report VK_SHIFT flaky mid-chord.
		shiftDown := shift || win.GetKeyState(win.VK_LSHIFT) < 0 || win.GetKeyState(win.VK_RSHIFT) < 0
		if !u.appOwnsKeyboard() && !ctrl && wParam == win.VK_RETURN && (shiftDown || alt) {
			if in := u.activeInput(); in != nil {
				u.warpBarInsertNewline(in)
				return 0
			}
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
		// Ctrl+←/→ word jump; Ctrl+Backspace/Delete word delete (Windows native).
		if ctrl && !alt {
			switch wParam {
			case win.VK_LEFT:
				in.moveWordLeft()
				u.requestInputPaint()
				return 0
			case win.VK_RIGHT:
				in.moveWordRight()
				u.requestInputPaint()
				return 0
			case win.VK_BACK:
				prevRows := in.visualRows(cols)
				in.deleteWordLeft()
				if in.visualRows(cols) != prevRows {
					u.maybeResizeForInput()
					u.markShellDirty()
					u.requestPaint()
				} else {
					u.requestInputPaint()
				}
				return 0
			case win.VK_DELETE:
				prevRows := in.visualRows(cols)
				in.deleteWordRight()
				if in.visualRows(cols) != prevRows {
					u.maybeResizeForInput()
					u.markShellDirty()
					u.requestPaint()
				} else {
					u.requestInputPaint()
				}
				return 0
			}
		}
		switch wParam {
		case win.VK_RETURN:
			if shiftDown {
				// Shift+Enter — new line in the bar (multiline script).
				u.warpBarInsertNewline(in)
				return 0
			}
			line := in.submit()
			u.maybeResizeForInput()
			u.submitBarLine(tab, line)
			u.markShellDirty()
			u.requestPaint()
		case win.VK_UP:
			if !in.moveVisualUp(cols) {
				in.historyUp()
			}
			u.maybeResizeForInput()
			u.requestInputPaint()
		case win.VK_DOWN:
			if !in.moveVisualDown(cols) {
				in.historyDown()
			}
			u.maybeResizeForInput()
			u.requestInputPaint()
		case win.VK_RIGHT:
			// zsh-autosuggest: → at EOL accepts the ghost suggestion.
			if in.cursor >= len(in.runes) {
				if in.acceptGhost(tab.cwd) {
					u.maybeResizeForInput()
					u.requestInputPaint()
					break
				}
			}
			in.moveRight()
			u.requestInputPaint()
		case win.VK_LEFT:
			in.moveLeft()
			u.requestInputPaint()
		case win.VK_DELETE:
			in.deleteForward()
			u.maybeResizeForInput()
			u.requestInputPaint()
		case win.VK_HOME:
			in.moveHome()
			u.requestInputPaint()
		case win.VK_END:
			in.moveEnd()
			u.requestInputPaint()
		case win.VK_PRIOR:
			vr := u.rows
			if g := u.focusedGeom(); g != nil && g.rows > 0 {
				vr = g.rows
			}
			tab.sb.scrollBy(vr/2, vr)
			u.markShellDirty()
			u.requestPaint()
		case win.VK_NEXT:
			vr := u.rows
			if g := u.focusedGeom(); g != nil && g.rows > 0 {
				vr = g.rows
			}
			tab.sb.scrollBy(-(vr / 2), vr)
			u.markShellDirty()
			u.requestPaint()
		case win.VK_ESCAPE:
			if u.modalImage != nil {
				u.modalImage = nil
				win.InvalidateRect(hwnd, nil, false)
				return 0
			}
			if len(in.runes) > 0 || in.histIdx >= 0 {
				in.clear()
				u.maybeResizeForInput()
				win.InvalidateRect(hwnd, nil, false)
			}
		case win.VK_BACK:
			u.handleInputBackspace(hwnd, lParam)
		case win.VK_TAB:
			// Host path + history completion (Warp bar never forwards Tab to PTY).
			prevRows := in.visualRows(cols)
			if in.complete(tab.cwd, shift) {
				if in.visualRows(cols) != prevRows {
					u.maybeResizeForInput()
				}
				win.InvalidateRect(hwnd, nil, false)
			}
		}
		return 0

	case win.WM_SYSKEYDOWN:
		// Alt+key arrives as SYSKEYDOWN. Never DefWindowProc for keys we own —
		// that is what plays the Windows "ding" on Alt+Enter.
		ctrl := win.GetKeyState(win.VK_CONTROL) < 0
		shift := win.GetKeyState(win.VK_SHIFT) < 0 || win.GetKeyState(win.VK_LSHIFT) < 0 || win.GetKeyState(win.VK_RSHIFT) < 0
		alt := win.GetKeyState(win.VK_MENU) < 0
		// Alt+Enter in Warp bar → multiline (same as Shift+Enter).
		if alt && !ctrl && wParam == win.VK_RETURN && !u.appOwnsKeyboard() {
			if in := u.activeInput(); in != nil {
				u.warpBarInsertNewline(in)
				return 0
			}
		}
		// Alt+Enter (and other app keys) → alt-screen TUIs (Grok newline).
		if u.appOwnsKeyboard() {
			tab := u.activeTab()
			if tab != nil {
				if b := ptyKeyFromWin(tab.term, &tab.kitty, wParam, ctrl, shift, alt); len(b) > 0 {
					u.sendKey(b)
					return 0
				}
			}
		}
		// Swallow unhandled syskeys so Windows does not beep.
		return 0

	case win.WM_SYSCHAR:
		// Companion to SYSKEYDOWN — DefWindowProc would beep on Alt+letter/Enter.
		return 0

	case win.WM_MOUSEWHEEL:
		delta := int16(wParam >> 16)
		steps := int(delta) / 120
		if steps == 0 {
			if delta > 0 {
				steps = 1
			} else {
				steps = -1
			}
		}
		// Workspace owns the wheel while open — do not scroll the shell under it.
		if u.chrome.WorkspaceOpen {
			m := u.chrome
			// Win32: positive delta = wheel away = older messages = ScrollUp.
			m.WorkspaceScroll(steps * 3)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		// Prefer the pane under the cursor so split layouts scroll the hovered leaf.
		var pt win.POINT
		if win.GetCursorPos(&pt) {
			win.ScreenToClient(hwnd, &pt)
		}
		tab := u.tabUnderPoint(pt.X, pt.Y)
		if tab == nil {
			tab = u.activeTab()
		}
		if tab == nil {
			return 0
		}
		// Win32: positive delta = wheel away = scroll up.
		viewRows := u.rows
		if g := u.paneGeomFor(tab.id); g != nil && g.rows > 0 {
			viewRows = g.rows
		}
		if tab.altScreen() {
			// Full-screen apps own the surface — never host history under them.
			// Forward wheel as SGR mouse (if tracking) or arrow keys (Grok, vim, …).
			cx, cy, _ := u.pixelToCellInPane(pt.X, pt.Y, tab)
			if b := encodeMouseWheel(tab.term, cx+1, cy+1, steps*3); len(b) > 0 {
				tab.sendKey(b) // hovered pane's PTY, even if unfocused
			}
			return 0
		}
		tab.sb.scrollBy(steps*3, viewRows)
		u.inputOnlyDirty = false
		u.requestPaint()
		return 0

	case win.WM_LBUTTONDBLCLK:
		// Double-click strip tab → rename tab; pane title bar → rename pane.
		u.focus()
		px := int32(win.LOWORD(uint32(lParam)))
		py := int32(win.HIWORD(uint32(lParam)))
		if u.modalImage != nil || u.chrome.OverlayOpen() {
			return 0
		}
		chH := u.metricH
		if chH < 1 {
			chH = cellH
		}
		tabStripH := int32(chrome.TabStripRows()) * chH
		if py < tabStripH {
			if i := u.hitTab(px); i >= 0 {
				u.active = i
				u.selecting = false
				if t := u.activeTab(); t != nil {
					t.sel.clear()
					setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
				}
				u.syncChrome()
				u.openRenameUI(chrome.RenameTargetTab)
			}
			return 0
		}
		layouts := u.computeActiveLayout()
		if g := u.hitPaneTitleBar(px, py, layouts); g != nil && g.pane != nil {
			_ = u.focusPaneByID(g.pane.id)
			u.openRenameUI(chrome.RenameTargetPane)
			return 0
		}
		return 0

	case win.WM_LBUTTONDOWN:
		u.focus()
		px := int32(win.LOWORD(uint32(lParam)))
		py := int32(win.HIWORD(uint32(lParam)))
		// Image modal: any click closes (simple lightbox).
		if u.modalImage != nil {
			u.modalImage = nil
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		chH := u.metricH
		if chH < 1 {
			chH = cellH
		}
		tabStripH := int32(chrome.TabStripRows()) * chH
		chromeH := u.chromePixelHeight()

		// Click outside floating overlay (on shell) dismisses it.
		// Clicks on the card itself (including notes) must not put it away.
		if u.chrome.OverlayOpen() && py >= chromeH {
			if u.hitOverlayCard(px, py) {
				u.focus()
				// Notes: route click into list / title / editor.
				if u.chrome.NotesOpen {
					cw, ch := u.metricW, u.metricH
					if cw < 1 {
						cw = cellW
					}
					if ch < 1 {
						ch = cellH
					}
					var rect win.RECT
					win.GetClientRect(hwnd, &rect)
					oy := u.overlayOriginY(rect.Bottom-rect.Top, len(u.overlayCells))
					cx := int(px / cw)
					cy := int((py - oy) / ch)
					nClick := u.notesMulti.bump(cx, cy, time.Now())
					r := u.chrome.UpdateChrome(chrome.NotesClickMsg{
						CellX: cx, CellY: cy, Cols: u.cols, ClickCount: nClick,
					})
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					u.notesDragging = r.StartNotesDrag
					if u.notesDragging {
						win.SetCapture(hwnd)
					}
					u.persistNotesIfDirty()
					win.InvalidateRect(hwnd, nil, false)
					return 0
				}
				// Transfer panel: click card (Copy ticket button / ticket) → clipboard.
				if u.chrome.TransferPanelOpen {
					r := u.chrome.UpdateChrome(chrome.TransferClickMsg{})
					u.chrome = r.Model
					if r.Action != chrome.ActionNone {
						u.applyChromeAction(r)
					}
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					return 0
				}
				// Workspace: channel tabs / + new channel.
				if u.chrome.WorkspaceOpen {
					cw, ch := u.metricW, u.metricH
					if cw < 1 {
						cw = cellW
					}
					if ch < 1 {
						ch = cellH
					}
					var rect win.RECT
					win.GetClientRect(hwnd, &rect)
					oy := u.overlayOriginY(rect.Bottom-rect.Top, len(u.overlayCells))
					cx := int(px / cw)
					cy := int((py - oy) / ch)
					r := u.chrome.UpdateChrome(chrome.WorkspaceClickMsg{
						CellX: cx, CellY: cy, Cols: u.cols,
					})
					u.chrome = r.Model
					u.overlayDirty = true
					u.overlayCells = nil
					win.InvalidateRect(hwnd, nil, false)
					return 0
				}
				return 0
			}
			log.Info("dismiss overlay (click outside)")
			applog.Sync()
			// Clear overlay cells first so the next paint cannot draw a stale
			// card while cancel restores theme/font.
			u.overlayCells = nil
			u.overlayDirty = true
			u.overlaySceneReady = false
			r := u.chrome.UpdateChrome(chrome.DismissOverlayMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			u.persistNotesIfDirty()
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

		// Top tab strip / + chip / caffeine cup.
		if py < chromeH {
			if py < tabStripH {
				if u.hitCaffeine(px) {
					if msg, ok := applyCaffeineAction(u.caffeine, chrome.ActionCaffeineToggle, 0); ok {
						if msg != "" {
							u.toast(msg)
						}
						u.markChromeDirty()
						u.syncChrome()
						win.InvalidateRect(hwnd, nil, false)
					}
					return 0
				}
				if u.hitPlus(px) {
					u.newTabUI("")
					return 0
				}
				if i := u.hitTab(px); i >= 0 {
					u.active = i
					u.selecting = false
					if t := u.activeTab(); t != nil {
						t.sel.clear()
						setWindowTitle(u.hwnd, "suzuri — "+t.displayTitle())
					}
					u.syncChrome()
					u.maybeResizeForInput()
					win.InvalidateRect(hwnd, nil, false)
				}
			}
			return 0
		}
		// Sash drag starts before pane hit-test (shared divider between panes).
		layouts := u.computeActiveLayout()
		if si := hitSash(u.lastSashes, px, py); si >= 0 {
			s := u.lastSashes[si]
			u.sashDrag = &s
			u.selecting = false
			win.SetCapture(hwnd)
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		// Click-to-focus a pane; click on a pane's input bar focuses without selection.
		if hi := hitPane(layouts, px, py); hi >= 0 && layouts[hi].pane != nil {
			g := layouts[hi]
			if g.barH > 0 && py >= g.barY && py < g.barY+g.barH {
				_ = u.focusPaneByID(g.pane.id)
				u.focus()
				win.InvalidateRect(hwnd, nil, false)
				return 0
			}
			if u.focusPaneByID(g.pane.id) {
				win.InvalidateRect(hwnd, nil, false)
				// Fall through so a drag can still start selection on the new focus.
			}
		}
		tab := u.activeTab()
		if tab == nil {
			return 0
		}
		// Don't start shell selection on the focused pane's bar region.
		if g := u.focusedGeom(); g != nil && g.barH > 0 && py >= g.barY {
			u.focus()
			return 0
		}
		// Ctrl+click a URL → open in the system browser (works on alt-screen too).
		ctrlClick := win.GetKeyState(win.VK_CONTROL) < 0
		altClick := win.GetKeyState(win.VK_MENU) < 0
		shiftClick := win.GetKeyState(win.VK_SHIFT) < 0
		if ctrlClick && !altClick && !shiftClick {
			if url := u.linkURLAt(px, py); url != "" {
				openURLInBrowser(url)
				u.toast("opened link")
				return 0
			}
		}
		// Grok / alt-screen: click near "[Open Image]" or a path opens a modal.
		// Primary shell: click an image block opens the same modal.
		if u.tryOpenImageModalAt(px, py) {
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		x, y, viewRows := u.pixelToCellInPane(px, py, tab)
		absY := tab.sb.absLine(y, viewRows, liveExtent(tab.term))
		n := u.shellMulti.bump(x, absY, time.Now())
		// Alt-screen (Grok): double/triple-click = host word/line selection.
		// Single-click still forwards to the TUI when mouse tracking is on.
		if tab.altScreen() {
			if n >= 2 {
				applyShellMultiClick(&tab.sel, tab.sb, tab.term, x, absY, n)
				u.selecting = true
				win.SetCapture(hwnd)
				win.InvalidateRect(hwnd, nil, false)
				return 0
			}
			tab.sel.clear()
			if mouseTracking(tab.term) {
				cx, cy, _ := u.pixelToCellInPane(px, py, tab)
				col, row := cx+1, cy+1
				if b := encodeMouseButton(tab.term, col, row, 0, true); len(b) > 0 {
					tab.sendKey(b)
					u.altMouseDown = true
					u.altMouseCol, u.altMouseRow = col, row
					win.SetCapture(hwnd)
				}
			}
			return 0
		}
		applyShellMultiClick(&tab.sel, tab.sb, tab.term, x, absY, n)
		u.selecting = true
		win.SetCapture(hwnd)
		win.InvalidateRect(hwnd, nil, false)
		return 0

	case win.WM_MOUSEMOVE:
		px := int32(win.LOWORD(uint32(lParam)))
		py := int32(win.HIWORD(uint32(lParam)))
		// Link hover (hand cursor + primary tint) when not dragging.
		if u.sashDrag == nil && !u.selecting && !u.notesDragging {
			u.updateLinkHover(px, py)
		}
		// Alt-screen: SGR motion for Grok button hover (1003) / drag (1002).
		if tab := u.activeTab(); tab != nil && tab.altScreen() && !u.chrome.OverlayOpen() {
			left := u.altMouseDown || (wParam&win.MK_LBUTTON) != 0
			u.maybeSendAltMouseMotion(tab, px, py, left)
		} else {
			u.altMouseCol, u.altMouseRow = 0, 0
		}
		// Live sash resize: paint-only mid-drag always. ConPTY only on LBUTTONUP
		// settle — quiet-path ConPTY thrash during drag still hard-crashed under
		// Grok in production (ResizePseudoConsole storms).
		if u.sashDrag != nil && (wParam&win.MK_LBUTTON) != 0 {
			applySashDrag(*u.sashDrag, px, py)
			// Keep sash geom parent bounds from last layout; node.ratio is live.
			if u.width > 0 && u.height > 0 {
				u.markLayoutDeferred()
				u.relayoutActivePaintOnly()
			}
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		// Notes drag-select.
		if u.notesDragging && u.chrome.NotesOpen && (wParam&win.MK_LBUTTON) != 0 {
			cw, ch := u.metricW, u.metricH
			if cw < 1 {
				cw = cellW
			}
			if ch < 1 {
				ch = cellH
			}
			var rect win.RECT
			win.GetClientRect(hwnd, &rect)
			oy := u.overlayOriginY(rect.Bottom-rect.Top, len(u.overlayCells))
			cx := int(px / cw)
			cy := int((py - oy) / ch)
			r := u.chrome.UpdateChrome(chrome.NotesDragMsg{
				CellX: cx, CellY: cy, Cols: u.cols,
			})
			u.chrome = r.Model
			u.overlayDirty = true
			u.overlayCells = nil
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		// Hover cursor on sash (when not dragging selection).
		if !u.selecting && u.sashDrag == nil {
			_ = u.computeActiveLayout()
			if si := hitSash(u.lastSashes, px, py); si >= 0 {
				s := u.lastSashes[si]
				if s.dir == splitVert {
					win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_SIZEWE)))
				} else {
					win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_SIZENS)))
				}
			}
		}
		tab := u.activeTab()
		if tab != nil && u.selecting && (wParam&win.MK_LBUTTON) != 0 {
			x, y, viewRows := u.pixelToCellInPane(px, py, tab)
			absY := tab.sb.absLine(y, viewRows, liveExtent(tab.term))
			tab.sel.x1, tab.sel.y1 = x, absY
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_LBUTTONUP:
		if u.sashDrag != nil {
			u.sashDrag = nil
			win.ReleaseCapture()
			// Final settle so ConPTY matches the dragged sizes cleanly.
			u.postLayoutSettle()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		if u.notesDragging {
			u.notesDragging = false
			win.ReleaseCapture()
			u.persistNotesIfDirty()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}
		if u.altMouseDown {
			u.altMouseDown = false
			px := int32(win.LOWORD(uint32(lParam)))
			py := int32(win.HIWORD(uint32(lParam)))
			if t := u.activeTab(); t != nil && t.altScreen() && mouseTracking(t.term) {
				cx, cy, _ := u.pixelToCellInPane(px, py, t)
				if b := encodeMouseButton(t.term, cx+1, cy+1, 0, false); len(b) > 0 {
					t.sendKey(b)
				}
			}
			win.ReleaseCapture()
			return 0
		}
		tab := u.activeTab()
		if tab != nil && u.selecting {
			px := int32(win.LOWORD(uint32(lParam)))
			py := int32(win.HIWORD(uint32(lParam)))
			x, y, viewRows := u.pixelToCellInPane(px, py, tab)
			absY := tab.sb.absLine(y, viewRows, liveExtent(tab.term))
			tab.sel.x1, tab.sel.y1 = x, absY
			u.selecting = false
			win.ReleaseCapture()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_RBUTTONUP:
		// Right-click paste — not under chrome overlays (workspace tabs etc.).
		if !u.chrome.OverlayOpen() {
			u.pasteClipboard()
		}
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
		u.persistNotes()
		log.Info("WM_DESTROY — tearing down", "tabs", len(u.tabs))
		u.alive.Store(false)
		if u.caffeine != nil {
			u.caffeine.Close()
		}
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
		if u.symFont != 0 {
			win.DeleteObject(win.HGDIOBJ(u.symFont))
			u.symFont = 0
		}
		releaseAppIcons()
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

	// Accept new coalesced invalidates while we paint.
	u.paintPending = false

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

	// Keep size fields honest. Never applyClientSize (ConPTY) on the WM_PAINT
	// stack — post settle only when client size/metrics change or a pane's VT
	// size would change. Do NOT settle on bar-height bookkeeping alone: that
	// used to thrash 88↔89 rows and make scrollback flicker/unusable.
	sizeChanged := u.width != w || u.height != h
	u.width, u.height = w, h

	// Layout all panes on the active page (equal H/V splits).
	layouts := u.computeActiveLayout()
	if len(layouts) == 0 {
		// Single-pane fallback geometry = full shell.
		sx, sy, sw, sh := u.shellRect(w, h)
		layouts = []paneGeom{{
			pane: tab, x: sx, y: sy, w: sw, h: sh,
			cols: u.cols, rows: u.rows, focused: true,
		}}
		u.lastPaneLayout = layouts
	}
	var barSum int32
	needResize := false
	for _, g := range layouts {
		barSum += g.barH
		if g.pane != nil && (g.pane.lastCols != g.cols || g.pane.lastRows != g.rows) {
			needResize = true
		}
	}
	u.inputPx = barSum
	// ConPTY size mismatch after alt-screen paint-only relayout is expected
	// while layoutDeferred — do NOT re-post settle every WM_PAINT (that
	// flooded "layout settle deferred" and hard-crashed under dual Grok).
	if sizeChanged || metricsChanged {
		u.postLayoutSettle()
	} else if needResize && !u.layoutDeferred {
		u.postLayoutSettle()
	}

	draw := func(dest win.HDC) {
		defer applog.Recover("paint.draw", false)
		overlay := u.chrome.OverlayOpen()
		dimModal := u.dimShellModal()
		// Fast path: static dim underlay (splash/confirm) already in memDC —
		// only re-paint the card. Palette/help never use this (live shell).
		if u.staticDimUnderlay() && u.overlaySceneReady &&
			u.memDC != 0 && dest == u.memDC && u.font != 0 {
			oldF := win.SelectObject(dest, win.HGDIOBJ(u.font))
			u.paintOverlay(dest, rect)
			u.paintImageModal(dest, rect)
			win.SelectObject(dest, oldF)
			return
		}
		// Notes-style scoping for the Warp bar: when only bar text/caret changed,
		// leave the shell grid in memDC and re-paint bars (and chrome if dirty).
		// Shell rain freezes while we stay here — acceptable, same tradeoff as
		// notes overlay scoping so typing does not re-blit the whole grid.
		if u.inputOnlyDirty && !overlay && !dimModal &&
			!u.matrixIntroActive() &&
			u.memDC != 0 && dest == u.memDC && u.font != 0 &&
			u.memW == w && u.memH == h {
			oldF := win.SelectObject(dest, win.HGDIOBJ(u.font))
			if u.chromeDirty {
				u.paintChrome(dest, rect)
			}
			u.paintInputBar(dest, rect)
			u.paintImageModal(dest, rect)
			win.SelectObject(dest, oldF)
			// Keep inputOnlyDirty sticky until markShellDirty so blink ticks
			// also take this path (caret pulse without re-blitting the grid).
			// Drop a stale overlayDirty left over from markChromeDirty while the
			// card was closed — nothing else clears it without paintOverlay.
			if !u.chrome.OverlayOpen() {
				u.overlayDirty = false
			}
			return
		}
		// Full paint path — shell is authoritative again.
		u.inputOnlyDirty = false

		// Void fill once; per-pane blit draws cells only.
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(chrome.VoidR, chrome.VoidG, chrome.VoidB)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(dest, rect, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
		padY := u.shellPadY()
		shellBot := u.shellBottomY(rect.Bottom - rect.Top)

		// Settings/splash/confirm: dim matte replaces the live terminal.
		// Palette + shortcuts: keep painting the live shell; card floats on top.
		if dimModal {
			u.paintDimShell(dest, rect)
		} else {
			u.overlaySceneReady = false
			// Shared dim rain under the whole shell region (not per-pane).
			// Also under alt-screen TUIs (Grok/vim): default-bg cells leave the
			// underlay visible (same as the 硯 watermark).
			if u.shellAmbientOn() && !u.matrixIntroActive() {
				u.paintShellAmbient(dest, rect, padY, shellBot)
			}
			u.paintShellWatermark(dest, rect, padY, shellBot)

			for _, g := range layouts {
				if g.pane == nil {
					continue
				}
				viewRows := g.rows
				if viewRows < 1 {
					viewRows = u.rows
				}
				grid := g.pane.sb.viewCells(g.pane.term, viewRows)
				if g.pane == u.activeTab() && u.hoverLinkOK {
					applyLinkHoverTint(grid, u.hoverLink)
				}
				cur := g.pane.term.Cursor()
				curVis := g.pane.altScreen() && g.pane.term.CursorVisible() && g.focused
				u.blitGridPane(dest, rect, grid, cur.X, cur.Y, curVis, g)
				if !g.pane.altScreen() {
					u.paintPaneImages(dest, rect, g)
				}
				// Grok prompt image previews (Kitty graphics APC) — over the cell grid.
				u.paintKittyPlacements(dest, rect, g.pane, g.y, g.y+g.h)
			}
			// CRT scanlines over the grid so empty cells don't hide them.
			if u.shellAmbientOn() && !u.matrixIntroActive() {
				u.paintShellAmbientOver(dest, rect, padY, shellBot)
			}
			if len(layouts) > 1 {
				u.paintPaneTitles(dest, layouts)
			}
			if u.matrixIntroActive() {
				func() {
					defer applog.Recover("paint.intro", false)
					switch strings.ToLower(strings.TrimSpace(u.cfg.Intro)) {
					case config.IntroRipple:
						u.paintRippleIntro(dest, rect)
					case config.IntroInkWash:
						u.paintInkWashIntro(dest, rect)
					case config.IntroCRT:
						u.paintCRTIntro(dest, rect)
					case config.IntroNone:
						if time.Now().After(u.matrixIntroSpawnEnd) {
							u.finishMatrixIntro()
						}
					default:
						u.paintMatrixIntro(dest, rect)
					}
				}()
			}
		}
		// Chrome strip + Warp input + floating card into the same buffer.
		if u.font == 0 {
			u.overlaySceneReady = false
			return
		}
		oldF := win.SelectObject(dest, win.HGDIOBJ(u.font))
		u.paintChrome(dest, rect)
		if !dimModal && len(layouts) > 1 {
			u.paintPaneBorders(dest, layouts)
		}
		// Freeze static dim underlay for splash/confirm card-only redraws.
		if u.staticDimUnderlay() && dest == u.memDC && u.memDC != 0 {
			u.overlaySceneReady = true
		} else if !u.staticDimUnderlay() {
			u.overlaySceneReady = false
		}
		if overlay {
			u.paintOverlay(dest, rect)
		}
		// Warp bars after the floating card so palette/help never cover inputs.
		if !dimModal {
			u.paintInputBar(dest, rect)
		}
		// Image lightbox on top of everything (Grok click / shell image block).
		u.paintImageModal(dest, rect)
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
		strings.EqualFold(a.ShellAmbient, b.ShellAmbient) &&
		a.ShellMatrixOpacity == b.ShellMatrixOpacity &&
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

// captureWindowPlacement reads the outer frame for reopen-on-same-place.
//
// Maximized: use GetWindowPlacement's restore rect + Maximized (GetWindowRect is
// only the work area while zoomed).
//
// Restored / Win+Arrow snap / half-screen: use GetWindowRect for the *actual*
// frame. GetWindowPlacement.RcNormalPosition stays at the pre-snap restore size,
// so keyboard snap never updated the saved layout when we only used placement.
func (u *winUI) captureWindowPlacement() (config.WindowPlacement, bool) {
	if u == nil || u.hwnd == 0 {
		return config.WindowPlacement{}, false
	}
	// Iconic: skip (tray coords are useless).
	if win.IsIconic(u.hwnd) {
		return config.WindowPlacement{}, false
	}
	if win.IsZoomed(u.hwnd) {
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
		return config.WindowPlacement{
			X:         int(rc.Left),
			Y:         int(rc.Top),
			Width:     w,
			Height:    h,
			Maximized: true,
		}, true
	}
	var rc win.RECT
	if !win.GetWindowRect(u.hwnd, &rc) {
		return config.WindowPlacement{}, false
	}
	w := int(rc.Right - rc.Left)
	h := int(rc.Bottom - rc.Top)
	if w < 320 || h < 200 {
		return config.WindowPlacement{}, false
	}
	return config.WindowPlacement{
		X:         int(rc.Left),
		Y:         int(rc.Top),
		Width:     w,
		Height:    h,
		Maximized: false,
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

// anyPaneConPtyBusy is true when a full ConPTY settle should wait.
// Dual alt-screen + ResizePseudoConsole mid-stream hard-crashes (no Go panic).
// Uses recent PTY I/O only — not titleBusy (Grok spinners never clear).
func (u *winUI) anyPaneConPtyBusy() bool {
	if u == nil {
		return false
	}
	for _, t := range u.allPanes() {
		if t != nil && t.alive.Load() && !t.conPtyResizeOK() {
			return true
		}
	}
	return false
}

// anyPaneSizeMismatch is true when a live leaf's ConPTY/VT last size does not
// match current layout geometry (hot skip left lastCols sticky).
func (u *winUI) anyPaneSizeMismatch() bool {
	if u == nil || u.width < 1 || u.height < 1 {
		return false
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	sx, sy, sw, sh := u.shellRect(u.width, u.height)
	for _, pg := range u.pages {
		if pg == nil || pg.root == nil {
			continue
		}
		for _, g := range layoutPage(pg.root, sx, sy, sw, sh, cw, ch, pg.focusID).leaves {
			t := g.pane
			if t == nil || !t.alive.Load() {
				continue
			}
			if t.lastCols != g.cols || t.lastRows != g.rows {
				return true
			}
		}
	}
	return false
}

// markLayoutDeferred records that ConPTY settle must wait (paint-only for now).
func (u *winUI) markLayoutDeferred() {
	if u == nil {
		return
	}
	if !u.layoutDeferred {
		u.layoutDeferredAt = time.Now()
	}
	u.layoutDeferred = true
}

// clearLayoutDeferred resets deferred-settle bookkeeping after a full apply.
func (u *winUI) clearLayoutDeferred() {
	if u == nil {
		return
	}
	u.layoutDeferred = false
	u.layoutDeferredAt = time.Time{}
}

// trailPaneSummary is a compact pane state string for durable crash trails.
func (u *winUI) trailPaneSummary() string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	for i, t := range u.allPanes() {
		if t == nil {
			continue
		}
		if i > 0 {
			b.WriteByte(';')
		}
		fmt.Fprintf(&b, "t%d:alive=%v,alt=%v,hot=%v,sz=%dx%d",
			t.id, t.alive.Load(), t.altScreen(), paneHasRecentIO(t, conPtyIOQuiet),
			t.lastCols, t.lastRows)
	}
	return b.String()
}

// onAltScreenToggled reflows when a TUI enters/leaves alt-screen (bar hide/show
// changes usable rows). Paint-only while panes stream; ConPTY settle when idle
// so dual Grok does not hard-crash.
func (u *winUI) onAltScreenToggled(t *tab) {
	if u == nil {
		return
	}
	u.relayoutActivePaintOnly()
	busy := u.anyPaneConPtyBusy()
	applog.Trail("alt-screen toggle",
		"tab", tabIDOrNeg(t),
		"busy", busy,
		"panes", u.trailPaneSummary(),
	)
	if busy {
		u.markLayoutDeferred()
		if t != nil {
			log.Debug("alt screen: paint-only, ConPTY settle deferred", "tab", t.id)
		}
	} else {
		u.postLayoutSettle()
	}
	u.requestPaint()
}

func tabIDOrNeg(t *tab) int {
	if t == nil {
		return -1
	}
	return t.id
}

// relayoutActivePaintOnly recomputes leaf geometry (bars/titles) for the active
// page without resizing ConPTY or dropping the backbuffer.
func (u *winUI) relayoutActivePaintOnly() {
	if u == nil || u.width < 2 || u.height < 2 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	pg := u.activePage()
	if pg == nil || pg.root == nil {
		return
	}
	sx, sy, sw, sh := u.shellRect(u.width, u.height)
	res := layoutPage(pg.root, sx, sy, sw, sh, cw, ch, pg.focusID)
	u.lastPaneLayout = res.leaves
	u.lastSashes = res.sashes
	u.lastShell.x, u.lastShell.y = res.shellX, res.shellY
	u.lastShell.w, u.lastShell.h = res.shellW, res.shellH
	u.inputPx = u.sumActivePaneBarHeights()
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
	// If panes are mid-stream (dual Grok), paint-only and try again later —
	// never ResizePseudoConsole under load (force-under-load hard-killed 0.9.82).
	// Do not requestPaint here: the paint path used to re-post settle every
	// frame → log flood → crash.
	if u.anyPaneConPtyBusy() {
		u.markLayoutDeferred()
		u.relayoutActivePaintOnly()
		u.layoutSettlePosted = false // allow blink to re-post when idle
		if u.spinTick%32 == 0 {
			log.Debug("layout settle deferred (pane busy)", "w", w, "h", h)
			applog.Trail("layout settle deferred", "w", w, "h", h, "panes", u.trailPaneSummary())
		}
		return
	}

	log.Info("layout settle begin", "w", w, "h", h, "cols", u.cols, "rows", u.rows,
		"force", false, "panes", u.trailPaneSummary())
	applog.Trail("layout settle begin", "w", w, "h", h, "cols", u.cols, "rows", u.rows,
		"panes", u.trailPaneSummary())
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

	// Drop backbuffer only when client size changed — dual busy settles used
	// to thrash GDI by recreating a full-window bitmap every alt-screen toggle.
	if u.memW != w || u.memH != h || u.memDC == 0 {
		u.releaseBackbuffer()
	}
	u.overlayDirty = true
	u.chromeDirty = true

	// Apply including alt-screen TUIs so split/window resize reflows Grok.
	// Coalesced settle + same-size no-op prevent ResizePseudoConsole storms.
	// Only reached when no pane has recent PTY I/O at settle *entry*; a leaf
	// may still skip ConPTY if I/O arrived mid-apply (host ErrResizeBusy).
	u.applyClientSize(w, h)
	// Keep deferred while any leaf still needs ConPTY (sticky lastCols) so
	// quiet blink re-settles. Clear only when all panes match layout.
	if u.anyPaneConPtyBusy() || u.anyPaneSizeMismatch() {
		u.markLayoutDeferred()
		u.layoutSettlePosted = false
		log.Info("layout settle partial (retry when quiet)", "w", w, "h", h,
			"cols", u.cols, "rows", u.rows, "busy", u.anyPaneConPtyBusy(),
			"panes", u.trailPaneSummary())
		applog.Trail("layout settle partial", "w", w, "h", h, "panes", u.trailPaneSummary())
	} else {
		u.clearLayoutDeferred()
		log.Info("layout settle done", "w", w, "h", h, "cols", u.cols, "rows", u.rows,
			"deferred", u.layoutDeferred)
		applog.Trail("layout settle done", "w", w, "h", h, "cols", u.cols, "rows", u.rows)
	}
	applog.Sync()

	if u.alive.Load() {
		u.requestPaint()
	}
}

// applyClientSize updates cols/rows/chrome from a client pixel size.
// Safe to call from layout settle, first paint, and WM_ACTIVATE — not from the
// raw WM_EXITSIZEMOVE stack (that hard-crashed).
// Layout: [tab strip] [shell region]. Each leaf stacks [title?][VT][input bar?].
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
	if rows != rowsApprox {
		u.chrome = u.chrome.UpdateChrome(tea.WindowSizeMsg{Width: cols, Height: rows}).Model
	}
	// u.cols / u.rows track the full shell grid (chrome width + primary geometry).
	u.cols = cols
	u.rows = rows

	// Per-pane layout: each leaf gets its own cols/rows; ConPTY resize only when
	// the assigned size changes.
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
			g.pane.resize(g.cols, g.rows)
		}
	}
	// No pages yet: resize flat tabs to full size (init path).
	if len(u.pages) == 0 {
		tabs := append([]*tab(nil), u.tabs...)
		for _, t := range tabs {
			if t == nil || !t.alive.Load() {
				continue
			}
			t.resize(cols, rows)
		}
	}
}

// blitGrid paints the active pane full-shell (legacy single-pane entry).
func (u *winUI) blitGrid(hdc win.HDC, rect win.RECT, grid [][]cellPix, curX, curY int, curVis bool) {
	tab := u.activeTab()
	if tab == nil {
		return
	}
	const padX int32 = 4
	padY := u.shellPadY()
	g := paneGeom{
		pane: tab, x: padX, y: padY,
		w: rect.Right - padX, h: u.shellBottomY(rect.Bottom-rect.Top) - padY,
		cols: u.cols, rows: u.rows, focused: true,
	}
	// Full-shell path still owns void fill + matrix (used only if something
	// calls blitGrid directly).
	fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))
	shellBot := u.shellBottomY(rect.Bottom - rect.Top)
	if u.shellAmbientOn() && !u.matrixIntroActive() && !u.dimShellModal() {
		u.paintShellAmbient(hdc, rect, padY, shellBot)
	}
	u.paintShellWatermark(hdc, rect, padY, shellBot)
	u.blitGridPane(hdc, rect, grid, curX, curY, curVis, g)
}

// blitGridPane paints colored cells for one pane at a fixed pitch origin.
// Backgrounds/selection use FillRect (no pen hairlines) coalesced into runs so
// selection never shows a per-cell grid. Glyphs are placed at x*cellW so the
// font’s natural advance cannot drift off the grid.
// Caller paints void/matrix/watermark for the shell region.
func (u *winUI) blitGridPane(hdc win.HDC, rect win.RECT, grid [][]cellPix, curX, curY int, curVis bool, g paneGeom) {
	tab := g.pane
	if tab == nil {
		return
	}

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)

	cw, ch := measureCellSize(hdc)
	padX, padY := g.x, g.y
	viewRows := g.rows
	if viewRows < 1 {
		viewRows = u.rows
	}
	// Same effective live height as viewCells (trailing blank PTY rows clipped).
	liveRows := liveExtent(tab.term)
	// Fill any sub-cell remainder under the grid within this pane.
	paneBot := g.y + g.h
	gridBot := padY + int32(len(grid))*ch
	if gridBot < paneBot {
		br, bg, bb := byte(12), byte(12), byte(14)
		if n := len(grid); n > 0 {
			last := grid[n-1]
			if len(last) > 0 {
				c := last[0]
				if c.BR != 0 || c.BG != 0 || c.BB != 0 {
					br, bg, bb = c.BR, c.BG, c.BB
				} else {
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
			fillRect(hdc, win.RECT{Left: g.x, Top: gridBot, Right: g.x + g.w, Bottom: paneBot}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}

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
			x0, x1  int
			r, g, b byte
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
			absY := tab.sb.absLine(y, viewRows, liveRows)
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

		// Glyphs at fixed cell origins.
		win.SetBkMode(hdc, win.TRANSPARENT)
		for x, c := range row {
			r := c.Ch
			if r == 0 || r == ' ' {
				continue
			}
			absY := tab.sb.absLine(y, viewRows, liveRows)
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

// paintPaneTitles draws the mini title strip on each multi-pane leaf.
func (u *winUI) paintPaneTitles(hdc win.HDC, layouts []paneGeom) {
	if len(layouts) < 2 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	for _, g := range layouts {
		if g.titleH < 1 {
			continue
		}
		br, bg, bb := chrome.BarR, chrome.BarG, chrome.BarB
		if g.focused {
			br, bg, bb = chrome.PanelR, chrome.PanelG, chrome.PanelB
		}
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(br, bg, bb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: g.x, Top: g.titleY, Right: g.x + g.w, Bottom: g.titleY + g.titleH}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
		// Active accent underline under the mini tab.
		if g.focused {
			fr, fg, fb := chrome.PrimR, chrome.PrimG, chrome.PrimB
			if fr == 0 && fg == 0 && fb == 0 {
				fr, fg, fb = 0, 230, 118
			}
			ulb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(fr, fg, fb)}
			if ub := win.CreateBrushIndirect(&ulb); ub != 0 {
				th := int32(2)
				if th > g.titleH {
					th = 1
				}
				bot := g.titleY + g.titleH
				fillRect(hdc, win.RECT{Left: g.x, Top: bot - th, Right: g.x + g.w, Bottom: bot}, ub)
				win.DeleteObject(win.HGDIOBJ(ub))
			}
		}
		title := "shell"
		if g.pane != nil {
			if d := g.pane.displayTitle(); d != "" {
				title = d
			} else {
				title = fmt.Sprintf("shell %d", g.pane.id+1)
			}
			if g.pane.busy() {
				title = "◌ " + title
			}
		}
		maxChars := int(g.w/cw) - 2
		if maxChars < 1 {
			maxChars = 1
		}
		rs := []rune(title)
		if len(rs) > maxChars {
			if maxChars > 1 {
				title = string(rs[:maxChars-1]) + "…"
			} else {
				title = string(rs[:maxChars])
			}
		}
		tr, tg, tb := chrome.SoftR, chrome.SoftG, chrome.SoftB
		if g.focused {
			tr, tg, tb = chrome.TextR, chrome.TextG, chrome.TextB
		}
		if u.font != 0 {
			oldF := win.SelectObject(hdc, win.HGDIOBJ(u.font))
			win.SetBkMode(hdc, win.TRANSPARENT)
			win.SetTextColor(hdc, win.RGB(tr, tg, tb))
			if s, err := syscall.UTF16FromString(" " + title); err == nil && len(s) > 1 {
				win.TextOut(hdc, g.x, g.titleY, &s[0], int32(len(s)-1))
			}
			win.SelectObject(hdc, oldF)
		}
	}
}

// paintPaneBorders draws a *shared* dim perimeter + internal sashes (no double
// borders between siblings), then a primary highlight on the focused leaf.
func (u *winUI) paintPaneBorders(hdc win.HDC, layouts []paneGeom) {
	if len(layouts) < 2 || hdc == 0 {
		return
	}
	dr, dg, db := chrome.MuteR, chrome.MuteG, chrome.MuteB
	if dr == 0 && dg == 0 && db == 0 {
		dr, dg, db = 70, 70, 80
	}
	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB
	if pr == 0 && pg == 0 && pb == 0 {
		pr, pg, pb = 0, 230, 118
	}
	// Dim sash color: mute with a hint of primary.
	dr = byte((int(dr)*3 + int(pr)) / 4)
	dg = byte((int(dg)*3 + int(pg)) / 4)
	db = byte((int(db)*3 + int(pb)) / 4)

	dlb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(dr, dg, db)}
	dbrush := win.CreateBrushIndirect(&dlb)
	if dbrush == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(dbrush))

	plb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(pr, pg, pb)}
	pbrush := win.CreateBrushIndirect(&plb)
	if pbrush != 0 {
		defer win.DeleteObject(win.HGDIOBJ(pbrush))
	}

	// Outer shell perimeter (once) — shared frame for the whole split group.
	sx, sy, sw, sh := u.lastShell.x, u.lastShell.y, u.lastShell.w, u.lastShell.h
	if sw < 1 || sh < 1 {
		// Fallback: union of leaf outer rects.
		sx, sy = layouts[0].x, layouts[0].outerY
		if layouts[0].outerH < 1 {
			sy = layouts[0].y
		}
		var maxR, maxB int32
		for _, g := range layouts {
			oy, oh := g.outerY, g.outerH
			if oh < 1 {
				oy, oh = g.y, g.h
			}
			if g.x < sx {
				sx = g.x
			}
			if oy < sy {
				sy = oy
			}
			if g.x+g.w > maxR {
				maxR = g.x + g.w
			}
			if oy+oh > maxB {
				maxB = oy + oh
			}
		}
		sw, sh = maxR-sx, maxB-sy
	}
	const dimT int32 = 1
	u.fillPaneBorder(hdc, sx, sy, sw, sh, dimT, dbrush)

	// Internal sashes: single shared divider (fills the gap between siblings).
	for _, s := range u.lastSashes {
		if s.w < 1 || s.h < 1 {
			continue
		}
		// Draw a 1px line centered in the sash strip for a clean hairline.
		if s.dir == splitVert {
			cx := s.x + s.w/2
			if cx < s.x {
				cx = s.x
			}
			fillRect(hdc, win.RECT{Left: cx, Top: s.y, Right: cx + 1, Bottom: s.y + s.h}, dbrush)
		} else {
			cy := s.y + s.h/2
			if cy < s.y {
				cy = s.y
			}
			fillRect(hdc, win.RECT{Left: s.x, Top: cy, Right: s.x + s.w, Bottom: cy + 1}, dbrush)
		}
	}

	// Active pane: primary highlight on its outer edges (overlays shared sashes).
	if pbrush == 0 {
		return
	}
	const hotT int32 = 2
	for _, g := range layouts {
		if !g.focused {
			continue
		}
		oy, oh := g.outerY, g.outerH
		if oh < 1 {
			oy, oh = g.y, g.h
		}
		if g.w < 2 || oh < 2 {
			continue
		}
		u.fillPaneBorder(hdc, g.x, oy, g.w, oh, hotT, pbrush)
	}
}

// fillPaneBorder paints a hollow rectangle frame of thickness t.
func (u *winUI) fillPaneBorder(hdc win.HDC, x, y, w, h, t int32, brush win.HBRUSH) {
	if brush == 0 || w < 1 || h < 1 || t < 1 {
		return
	}
	if t*2 > w {
		t = w / 2
	}
	if t*2 > h {
		t = h / 2
	}
	if t < 1 {
		return
	}
	// top, bottom, left, right
	fillRect(hdc, win.RECT{Left: x, Top: y, Right: x + w, Bottom: y + t}, brush)
	fillRect(hdc, win.RECT{Left: x, Top: y + h - t, Right: x + w, Bottom: y + h}, brush)
	fillRect(hdc, win.RECT{Left: x, Top: y, Right: x + t, Bottom: y + h}, brush)
	fillRect(hdc, win.RECT{Left: x + w - t, Top: y, Right: x + w, Bottom: y + h}, brush)
}

// measureCellSize returns the monospaced cell size for the font selected in hdc.
// GetTextExtent of "M" matches glyph advance better than TmAveCharWidth, which
// is often 1px short and produces a visible grid of seams under selection.
// Cell height always covers ascent+descent so descenders (j/g/y) are not clipped
// by the next row's background fill.
func measureCellSize(hdc win.HDC) (cw, ch int32) {
	cw, ch = cellW, cellH
	var tm win.TEXTMETRIC
	if win.GetTextMetrics(hdc, &tm) {
		if tm.TmHeight > 0 {
			ch = tm.TmHeight
		}
		// Prefer explicit ink box when Height under-reports (some bitmap faces).
		ink := tm.TmAscent + tm.TmDescent
		if ink > 0 && ch < ink+1 {
			ch = ink + 1
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
	// Status marks: keep primary metrics when possible. Only fall back to the
	// symbol face when the primary mono lacks the code point (avoids Cascadia
	// glyphs overflowing FiraCode-sized cells in the tab strip).
	if isStatusGlyphRune(r) && u.symFont != 0 {
		needSym := false
		switch {
		case r >= 0x2800 && r <= 0x28FF:
			needSym = !u.primaryHasBraille
		case r >= 0x25A0 && r <= 0x25FF:
			needSym = !u.primaryHasGeo
		}
		if needSym {
			win.SelectObject(hdc, win.HGDIOBJ(u.symFont))
			return
		}
	}
	if bold && u.fontBold != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.fontBold))
		return
	}
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
}

func isStatusGlyphRune(r rune) bool {
	// Braille Patterns (6-dot CLI spinners / tab state).
	if r >= 0x2800 && r <= 0x28FF {
		return true
	}
	// Geometric Shapes (●○◉◆◎…).
	if r >= 0x25A0 && r <= 0x25FF {
		return true
	}
	return false
}

// createSymbolFont picks a face known for braille + geometric status glyphs.
func createSymbolFont(sizePx int) win.HFONT {
	if sizePx < 10 {
		sizePx = 14
	}
	for _, name := range []string{
		"Cascadia Mono",
		"Cascadia Code",
		"Segoe UI Symbol",
	} {
		h := createNamedFont(name, -int32(sizePx), win.FW_NORMAL)
		if h == 0 {
			continue
		}
		// Must actually map the classic 6-dot cell (GDI substitutes freely).
		hdc := win.CreateCompatibleDC(0)
		if hdc == 0 {
			win.DeleteObject(win.HGDIOBJ(h))
			continue
		}
		old := win.SelectObject(hdc, win.HGDIOBJ(h))
		ok := fontHasRunes(hdc, '⠿', '⣿', '⣷', '⠁')
		win.SelectObject(hdc, old)
		win.DeleteDC(hdc)
		if ok {
			return h
		}
		win.DeleteObject(win.HGDIOBJ(h))
	}
	return 0
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
	procExtTextOut    = modGdi32.NewProc("ExtTextOutW")
)

// ETO_CLIPPED — clip text to the rect (gdi32 ExtTextOutW).
const etoClipped = 0x0004

func extTextOutClipped(hdc win.HDC, x, y int32, clip *win.RECT, s *uint16, n uint32) {
	if procExtTextOut.Find() != nil || clip == nil || s == nil || n == 0 {
		win.TextOut(hdc, x, y, s, int32(n))
		return
	}
	_, _, _ = procExtTextOut.Call(
		uintptr(hdc),
		uintptr(x),
		uintptr(y),
		uintptr(etoClipped),
		uintptr(unsafe.Pointer(clip)),
		uintptr(unsafe.Pointer(s)),
		uintptr(n),
		0,
	)
}

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
	x, y, _ = u.pixelToCellInPane(px, py, u.activeTab())
	return x, y
}

// tabUnderPoint returns the leaf pane under client pixels on the active page.
func (u *winUI) tabUnderPoint(px, py int32) *tab {
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
	if t := u.activeTab(); t != nil && len(layouts) <= 1 {
		chromeH := u.chromePixelHeight()
		if py >= chromeH {
			shellBot := u.shellBottomY(u.height)
			if py < shellBot {
				return t
			}
		}
	}
	return nil
}

// updateLinkHover finds an http(s)/www URL under the cursor for primary tint + hand cursor.
func (u *winUI) updateLinkHover(px, py int32) {
	if u == nil {
		return
	}
	clear := func() {
		if u.hoverLinkOK || u.linkCursorOn {
			u.hoverLinkOK = false
			u.hoverLink = linkSpan{}
			if u.linkCursorOn {
				win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)))
				u.linkCursorOn = false
			}
			u.markShellDirty()
			u.requestPaint()
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
	if g := u.focusedGeom(); g != nil && g.barH > 0 && py >= g.barY {
		clear()
		return
	}
	x, y, viewRows := u.pixelToCellInPane(px, py, tab)
	if viewRows < 1 {
		viewRows = u.rows
	}
	grid := tab.sb.viewCells(tab.term, viewRows)
	span, ok := linkAt(findLinksInGrid(grid), x, y)
	if !ok {
		clear()
		return
	}
	changed := !u.hoverLinkOK || u.hoverLink.url != span.url ||
		u.hoverLink.row != span.row || u.hoverLink.x0 != span.x0 || u.hoverLink.x1 != span.x1
	u.hoverLink = span
	u.hoverLinkOK = true
	if !u.linkCursorOn {
		win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_HAND)))
		u.linkCursorOn = true
	}
	if changed {
		u.markShellDirty()
		u.requestPaint()
	}
}

func (u *winUI) linkURLAt(px, py int32) string {
	if u == nil || u.chrome.OverlayOpen() {
		return ""
	}
	tab := u.activeTab()
	if tab == nil {
		return ""
	}
	if g := u.focusedGeom(); g != nil && g.barH > 0 && py >= g.barY {
		return ""
	}
	x, y, viewRows := u.pixelToCellInPane(px, py, tab)
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

// maybeSendAltMouseMotion reports pointer moves for hover (1003) or drag (1002).
func (u *winUI) maybeSendAltMouseMotion(t *tab, px, py int32, leftDown bool) {
	if u == nil || t == nil || t.term == nil || !t.altScreen() {
		return
	}
	if !mouseAnyMotion(t.term) && !(mouseDragMotion(t.term) && leftDown) {
		return
	}
	if g := u.paneGeomFor(t.id); g != nil {
		if px < g.x || px >= g.x+g.w || py < g.y || py >= g.y+g.h {
			return
		}
		if g.barH > 0 && py >= g.barY {
			return
		}
	}
	cx, cy, _ := u.pixelToCellInPane(px, py, t)
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

// pixelToCellInPane maps client pixels to cell coords within a pane's layout.
// viewRows is the pane viewport height for scrollback absLine.
func (u *winUI) pixelToCellInPane(px, py int32, tab *tab) (x, y, viewRows int) {
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
	if !u.lastPasteAt.IsZero() && time.Since(u.lastPasteAt) < 280*time.Millisecond {
		return
	}
	u.lastPasteAt = time.Now()
	// Overlays own paste even when Grok is alt-screen underneath.
	if u.chrome.TransferPromptOpen || u.chrome.WorkspaceOpen || u.chrome.NotesOpen {
		text, err := getClipboardText(u.hwnd)
		if err != nil || text == "" {
			return
		}
		if u.chrome.TransferPromptOpen {
			m := u.chrome
			m.TransferPaste(text)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			win.InvalidateRect(u.hwnd, nil, false)
			return
		}
		if u.chrome.WorkspaceOpen {
			m := u.chrome
			m.WorkspacePaste(text)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			win.InvalidateRect(u.hwnd, nil, false)
			return
		}
		if u.chrome.NotesOpen {
			m := u.chrome
			m.NotesPaste(text)
			u.chrome = m
			u.overlayDirty = true
			u.overlayCells = nil
			win.InvalidateRect(u.hwnd, nil, false)
			u.persistNotesIfDirty()
			return
		}
	}
	// Alt-screen (Grok, …): host delivers images. Clipboard PNG/DIB dump can be
	// slow — never block the UI thread; finish on a worker and inject on blink.
	if u.appOwnsKeyboard() {
		if u.pasteBusy.Swap(true) {
			return // already dumping a clipboard image
		}
		go u.pasteAltScreenAsync()
		return
	}
	text, err := getClipboardText(u.hwnd)
	if err != nil || text == "" {
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
		u.markShellDirty()
		u.requestPaint()
		return
	}
	u.requestInputPaint()
}

// pasteAltScreenAsync reads the clipboard off-thread and queues PTY inject.
func (u *winUI) pasteAltScreenAsync() {
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
	text, _ := getClipboardText(u.hwnd)
	if strings.TrimSpace(text) == "" {
		return
	}
	// Host bracketed paste only — Super+V + payload double-pasted into Grok.
	u.pendingPasteMu.Lock()
	u.pendingPaste = append(u.pendingPaste, pendingPaste{payload: bracketedPaste(text)})
	u.pendingPasteMu.Unlock()
}

// drainPendingPaste injects async paste results on the UI thread.
func (u *winUI) drainPendingPaste() {
	if u == nil {
		return
	}
	u.pendingPasteMu.Lock()
	batch := u.pendingPaste
	u.pendingPaste = nil
	u.pendingPasteMu.Unlock()
	for _, p := range batch {
		// Single inject: host payload only (no Super+V dual-path).
		if len(p.payload) > 0 {
			u.sendKey(p.payload)
		}
		if p.toast != "" {
			u.toast(p.toast)
		}
	}
}

// handleInputBackspace edits the Warp bar (rate-limited while held).
// Do not drain the queue on "too soon" — that used to swallow all auto-repeat
// KEYDOWNs and make hold-backspace feel dead.
func (u *winUI) handleInputBackspace(hwnd win.HWND, lParam uintptr) {
	wasDown := (uint32(lParam) & (1 << 30)) != 0
	now := time.Now()
	if wasDown && now.Sub(u.lastBackspace) < 30*time.Millisecond {
		return
	}
	u.lastBackspace = now
	if in := u.activeInput(); in != nil {
		prevRows := in.visualRows(u.inputContentCols())
		in.backspace()
		if in.visualRows(u.inputContentCols()) != prevRows {
			u.maybeResizeForInput()
			u.markShellDirty()
			u.requestPaint()
		} else {
			u.requestInputPaint()
		}
	} else {
		u.requestPaint()
	}
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
	// memDC contents are gone — next overlay paint must rebuild dim+neko.
	u.overlaySceneReady = false
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

// dimShellModal is true when a modal replaces the live terminal with a dim
// matte (settings / splash / confirm only). Workspace floats over the live
// shell — no full-window dim under the card.
func (u *winUI) dimShellModal() bool {
	if u == nil {
		return false
	}
	return u.chrome.SettingsOpen || u.chrome.ConfirmOpen || u.chrome.SplashOpen
}

// solidOverlayPanel fills ALL default-bg cells (splash/confirm only — no wide gutters).
func (u *winUI) solidOverlayPanel() bool {
	if u == nil {
		return false
	}
	return u.chrome.ConfirmOpen || u.chrome.SplashOpen
}

// solidOverlayInterior fills holes only inside the card bbox (workspace/settings).
func (u *winUI) solidOverlayInterior() bool {
	if u == nil {
		return false
	}
	return u.chrome.WorkspaceOpen || u.chrome.SettingsOpen
}

// staticDimUnderlay is true for dim modals that don't animate (splash/confirm).
// Settings matrix rain always full-repaints. Palette/help never use this path.
func (u *winUI) staticDimUnderlay() bool {
	if u == nil || u.chrome.SettingsOpen {
		return false
	}
	return u.chrome.ConfirmOpen || u.chrome.SplashOpen
}

// floatOverLiveShell is true when a card paints over the live terminal
// without a dim matte (palette/help/notes/rename/transfer/workspace).
func (u *winUI) floatOverLiveShell() bool {
	if u == nil || u.dimShellModal() {
		return false
	}
	return u.chrome.PaletteOpen || u.chrome.HelpOpen || u.chrome.RenameOpen ||
		u.chrome.NotesOpen || u.chrome.TransferPromptOpen || u.chrome.TransferPanelOpen ||
		u.chrome.WorkspaceOpen
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

// needsShellAnimPaint is true when the shell underlay or alt-screen caret must
// animate (full paint). Idle normal shells only need the Warp-bar caret.
func (u *winUI) needsShellAnimPaint() bool {
	if u == nil {
		return false
	}
	if u.matrixIntroActive() || u.shellAmbientOn() {
		return true
	}
	return u.anyAltScreenCursor()
}

// anyAltScreenCursor is true when a visible pane is on alt-screen with a
// blinking app caret (Grok, vim, …).
func (u *winUI) anyAltScreenCursor() bool {
	for _, t := range u.allPanes() {
		if t == nil || !t.altScreen() || t.term == nil {
			continue
		}
		if t.term.CursorVisible() {
			return true
		}
	}
	return false
}

// paintShellMatrix is a rain-only alias kept for call-site clarity in comments/tests.
func (u *winUI) paintShellMatrix(hdc win.HDC, rect win.RECT, padY, bot int32) {
	u.paintShellAmbient(hdc, rect, padY, bot)
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
// Settings: Ambient showcase by default; Intro curtain when that row is focused.
// Other modals: Charm-style 猫咪 field.
// Restored from b78e569 (pre-session dim formula + dense grid).
func (u *winUI) paintDimShell(hdc win.HDC, rect win.RECT) {
	defer applog.Recover("paintDimShell", false)
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY {
		return
	}
	if u.chrome.SettingsOpen {
		u.paintSettingsUnderlay(hdc, rect, padY, bot)
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

// paintSettingsUnderlay fills the settings matte and previews Ambient (default)
// or the focused Intro curtain behind the modal.
func (u *winUI) paintSettingsUnderlay(hdc win.HDC, rect win.RECT, padY, bot int32) {
	// Theme-tinted matte first (same base as paintMatrixMatteAndRain).
	baseR, baseG, baseB := blendRGB(0, 0, 0, chrome.DimR, chrome.DimG, chrome.DimB, 0.35)
	matteR, matteG, matteB := blendRGB(baseR, baseG, baseB,
		chrome.PrimR, chrome.PrimG, chrome.PrimB, 0.05)
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(matteR, matteG, matteB)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		r := win.RECT{Left: 0, Top: padY, Right: rect.Right, Bottom: bot}
		fillRect(hdc, r, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}

	if u.chrome.SettingsShowcaseIntro() {
		u.paintSettingsIntroPreview(hdc, rect, padY, bot)
		return
	}

	// Default: showcase Ambient + Intensity behind the card.
	if !u.shellAmbientOn() {
		return
	}
	intensity := settingsAmbientShowcaseIntensity(u.cfg)
	if intensity <= 0 {
		return
	}
	switch u.cfg.ShellAmbient {
	case config.AmbientRain:
		u.paintDimMatrixIntensity(hdc, rect, padY, bot, matrixLoop, u.blinkStart, 0, intensity)
	case config.AmbientCRT:
		u.paintCRTAmbient(hdc, rect, padY, bot, intensity*0.9)
	default:
		u.paintAmbientGlyphs(hdc, rect, padY, bot, u.cfg.ShellAmbient, intensity)
	}
}

// paintSettingsIntroPreview draws the focused Intro style under settings.
// Uses shared cell helpers where possible so we never call finishMatrixIntro
// (which would end a live startup curtain).
func (u *winUI) paintSettingsIntroPreview(hdc win.HDC, rect win.RECT, padY, bot int32) {
	style := config.Normalize(u.cfg).Intro
	now := time.Now()
	origin := u.blinkStart
	if origin.IsZero() {
		origin = now
	}
	// Loop finite curtains (ripple / ink / CRT) with a short gap; matrix loops forever.
	cycle := matrixIntroSpawn + 800*time.Millisecond
	if cycle < time.Second {
		cycle = time.Second
	}
	phase := now.Sub(origin) % cycle
	playT0 := now.Add(-phase)

	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	rows := int((bot - padY + ch - 1) / ch)
	cols := int((rect.Right + cw - 1) / cw)
	col := themeAmbientColors()

	switch style {
	case config.IntroNone:
		return
	case config.IntroInkWash:
		cells := inkWashCells(cols, rows, playT0, matrixIntroSpawn, now, col)
		u.paintRainCellList(hdc, rect, padY, bot, cells)
	case config.IntroCRT:
		t := phase.Seconds()
		sp := matrixIntroSpawn.Seconds()
		if t < sp {
			flash := 0.55
			if t < 0.3 {
				flash = 0.85
			}
			fade := 1.0
			if t > sp*0.55 {
				fade = 1 - (t-sp*0.55)/(sp*0.55)
				if fade < 0 {
					fade = 0
				}
			}
			if fade > 0 {
				u.paintCRTAmbient(hdc, rect, padY, bot, flash*fade)
			}
			cells := crtIntroCells(cols, rows, playT0, matrixIntroSpawn, now, col)
			u.paintRainCellList(hdc, rect, padY, bot, cells)
		}
	case config.IntroRipple:
		// Continuous-ish preview: matrix rain stands in for ring math here
		// (full ripple painter mutates intro state). Still reads as motion.
		// Prefer a short spawn+wind cycle via temporary intro fields without
		// finishing if already done.
		if u.matrixIntroDone {
			savedStart, savedSpawn := u.matrixIntroStart, u.matrixIntroSpawnEnd
			u.matrixIntroStart = playT0
			u.matrixIntroSpawnEnd = playT0.Add(matrixIntroSpawn)
			u.paintRippleIntro(hdc, rect)
			u.matrixIntroStart, u.matrixIntroSpawnEnd = savedStart, savedSpawn
			// paintRippleIntro may no-op finish when already done.
		} else {
			// Avoid mutating an active startup intro — show matrix instead.
			u.paintDimMatrix(hdc, rect, padY, bot, matrixLoop, origin, 0)
		}
	default:
		u.paintDimMatrix(hdc, rect, padY, bot, matrixLoop, origin, 0)
	}
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
// Restored from b78e569 (1/12 soft + 11/12 dim; every row, stepX = 2 cells).
func (u *winUI) paintDimNekoField(hdc win.HDC, rect win.RECT, top, bot int32) {
	defer applog.Recover("paintDimNekoField", false)
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
// paintInputBar draws the Warp bar for every leaf on the active page
// (each pane owns its bar at the bottom of its leaf).
func (u *winUI) paintInputBar(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	layouts := u.lastPaneLayout
	if len(layouts) == 0 {
		layouts = u.computeActiveLayout()
	}
	if len(layouts) == 0 {
		// Solo fallback before layout exists.
		if t := u.activeTab(); t != nil && !t.altScreen() {
			cw, ch := u.metricW, u.metricH
			if cw < 1 {
				cw = cellW
			}
			if ch < 1 {
				ch = cellH
			}
			w := rect.Right - rect.Left
			barH := paneInputBarPixelHeight(t, w, cw, ch)
			if barH > 0 {
				g := paneGeom{
					pane: t, x: 0, w: w, barY: rect.Bottom - barH, barH: barH,
					barCols: paneInputContentCols(w, cw), focused: true,
				}
				u.paintPaneInputBar(hdc, g)
			}
		}
		return
	}
	for _, g := range layouts {
		if g.barH < 1 || g.pane == nil {
			continue
		}
		u.paintPaneInputBar(hdc, g)
	}
}

// paintPaneInputBar draws one pane's command line into g.barY/g.barH.
func (u *winUI) paintPaneInputBar(hdc win.HDC, g paneGeom) {
	if hdc == 0 || g.pane == nil || g.barH < 1 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	top := g.barY
	left := g.x
	right := g.x + g.w
	bot := g.barY + g.barH

	// Panel fill (slightly dimmer when unfocused).
	pr, pg, pb := chrome.PanelR, chrome.PanelG, chrome.PanelB
	if !g.focused {
		// Blend toward void so unfocused bars read as secondary.
		pr = pr/2 + chrome.VoidR/2
		pg = pg/2 + chrome.VoidG/2
		pb = pb/2 + chrome.VoidB/2
	}
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(pr, pg, pb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: left, Top: top, Right: right, Bottom: bot}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}
	hair, topPad, _ := inputBarVPads(ch)
	// Top hairline: primary when focused, mute otherwise.
	hr, hg, hb := chrome.MuteR, chrome.MuteG, chrome.MuteB
	if g.focused {
		hr, hg, hb = chrome.PrimR, chrome.PrimG, chrome.PrimB
	}
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(hr, hg, hb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: left, Top: top, Right: right, Bottom: top + hair}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}

	padX := left + inputBarPadX
	padTop := top + hair + topPad

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)
	win.SetBkMode(hdc, win.TRANSPARENT)

	// Working directory row.
	if p := displayPath(g.pane.cwd); p != "" {
		maxCols := int((g.w - inputBarPadX*2) / cw)
		if maxCols < 8 {
			maxCols = 8
		}
		label := truncateRunes(p, maxCols)
		if s, err := syscall.UTF16FromString(label); err == nil && len(s) >= 2 {
			win.SetTextColor(hdc, win.RGB(chrome.SoftR, chrome.SoftG, chrome.SoftB))
			win.TextOut(hdc, padX, padTop, &s[0], int32(len(s)-1))
		}
		padTop += ch
	}

	prompt := inputBarPrompt
	promptW := int32(len([]rune(prompt))) * cw
	contentCols := g.barCols
	if contentCols < 1 {
		contentCols = paneInputContentCols(g.w, cw)
	}
	in := &g.pane.input
	empty := len(in.runes) == 0

	textR, textG, textB := chrome.TextR, chrome.TextG, chrome.TextB
	softR, softG, softB := chrome.SoftR, chrome.SoftG, chrome.SoftB
	primR, primG, primB := chrome.PrimR, chrome.PrimG, chrome.PrimB
	if !g.focused {
		textR, textG, textB = softR, softG, softB
	}

	if empty {
		if pr, err := syscall.UTF16FromString(prompt); err == nil && len(pr) >= 2 {
			win.SetTextColor(hdc, win.RGB(primR, primG, primB))
			win.TextOut(hdc, padX, padTop, &pr[0], int32(len(pr)-1))
		}
		if g.focused {
			hint := chrome.InputBarPlaceholder()
			if s, err := syscall.UTF16FromString(hint); err == nil && len(s) >= 2 {
				win.SetTextColor(hdc, win.RGB(softR, softG, softB))
				win.TextOut(hdc, padX+promptW+2*cw, padTop, &s[0], int32(len(s)-1))
			}
			u.paintInputCaret(hdc, padX+promptW, padTop, cw, ch)
		}
		return
	}

	view, caretRow, caretCol := in.visibleWindow(contentCols, maxInputVisualRows)
	for i, line := range view {
		y := padTop + int32(i)*ch
		xText := padX + promptW
		if i == 0 {
			if pr, err := syscall.UTF16FromString(prompt); err == nil && len(pr) >= 2 {
				win.SetTextColor(hdc, win.RGB(primR, primG, primB))
				win.TextOut(hdc, padX, y, &pr[0], int32(len(pr)-1))
			}
		}
		if line != "" {
			if s, err := syscall.UTF16FromString(line); err == nil && len(s) >= 2 {
				win.SetTextColor(hdc, win.RGB(textR, textG, textB))
				win.TextOut(hdc, xText, y, &s[0], int32(len(s)-1))
			}
		}
	}

	if !g.focused {
		return
	}
	caretY := padTop + int32(caretRow)*ch
	caretX := padX + promptW + int32(caretCol)*cw
	if ghost := in.ghostSuffix(g.pane.cwd); ghost != "" {
		if s, err := syscall.UTF16FromString(ghost); err == nil && len(s) >= 2 {
			win.SetTextColor(hdc, win.RGB(chrome.MuteR, chrome.MuteG, chrome.MuteB))
			win.TextOut(hdc, caretX, caretY, &s[0], int32(len(s)-1))
		}
	}
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
// defaultBar=false: floating overlay. When solidPanel (dim modals), default-bg
// holes fill with panel; otherwise gutters stay transparent (palette/help).
func (u *winUI) paintChromeCells(hdc win.HDC, rect win.RECT, cells [][]cellPix, ox, oy int32, defaultBar bool) {
	u.paintChromeCellsEx(hdc, rect, cells, ox, oy, defaultBar, false, false)
}

func (u *winUI) paintChromeCellsEx(hdc win.HDC, rect win.RECT, cells [][]cellPix, ox, oy int32, defaultBar, solidPanel, solidInterior bool) {
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Card bbox for interior-only hole fill (workspace).
	cardL, cardR, cardTop, cardBot := -1, -1, -1, -1
	if solidInterior && !defaultBar {
		for y := 0; y < len(cells); y++ {
			row := cells[y]
			for x := 0; x < len(row); x++ {
				cell := row[x]
				solid := !isTransparentOverlayBG(cell.BR, cell.BG, cell.BB) ||
					(cell.Ch != 0 && cell.Ch != ' ')
				if !solid {
					continue
				}
				if cardL < 0 || x < cardL {
					cardL = x
				}
				if x > cardR {
					cardR = x
				}
				if cardTop < 0 {
					cardTop = y
				}
				cardBot = y
			}
		}
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
			inInterior := solidInterior && cardL >= 0 &&
				y >= cardTop && y <= cardBot && x >= cardL && x <= cardR
			fillHoles := solidPanel || inInterior
			// Overlay: transparent gutters stay open; holes only inside card bbox.
			if !defaultBar && empty && isTransparentOverlayBG(br, bg, bb) {
				if fillHoles {
					br, bg, bb = chrome.PanelR, chrome.PanelG, chrome.PanelB
				} else {
					continue
				}
			}
			if br == 0 && bg == 0 && bb == 0 {
				if defaultBar {
					br, bg, bb = chrome.BarR, chrome.BarG, chrome.BarB
				} else {
					// Non-empty glyph with default bg — panel so it stays readable.
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
			// Clip status / any wide fallback glyphs to the cell so tab chips
			// never spill into the title or past the pad edge.
			if isStatusGlyphRune(r) {
				extTextOutClipped(hdc, cellRect.Left, cellRect.Top, &cellRect, &s[0], uint32(len(s)-1))
			} else {
				win.TextOut(hdc, cellRect.Left, cellRect.Top, &s[0], int32(len(s)-1))
			}
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

// overlayOriginY matches paintOverlay placement (shell region, slight top bias).
func (u *winUI) overlayOriginY(clientH int32, rows int) int32 {
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	padY := u.chromePx
	bot := u.shellBottomY(clientH)
	shellH := bot - padY
	oh := int32(rows) * ch
	oy := padY + (shellH-oh)/4
	if oy+oh > bot {
		oy = bot - oh
	}
	if oy < padY {
		oy = padY
	}
	return oy
}

// ensureOverlayCells builds the floating card grid if missing (for hit-tests).
func (u *winUI) ensureOverlayCells() {
	if !u.chrome.OverlayOpen() {
		return
	}
	cols := u.cols
	if cols < 20 {
		cols = 20
	}
	if !u.overlayDirty && u.chromeCols == cols && len(u.overlayCells) > 0 {
		return
	}
	ct := chrome.RenderOverlayToTerm(u.chrome, cols)
	if ct == nil {
		u.overlayCells = nil
		return
	}
	ccols, crows := ct.Size()
	maxOverlayRows := 48
	if u.chrome.WorkspaceOpen {
		maxOverlayRows = 96
	}
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
}

// hitOverlayCard is true when (px,py) lands on a non-transparent overlay cell.
func (u *winUI) hitOverlayCard(px, py int32) bool {
	if u == nil || !u.chrome.OverlayOpen() {
		return false
	}
	u.ensureOverlayCells()
	if len(u.overlayCells) == 0 {
		return false
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	var rect win.RECT
	if u.hwnd != 0 {
		win.GetClientRect(u.hwnd, &rect)
	}
	clientH := rect.Bottom - rect.Top
	if clientH < 1 {
		clientH = u.height
	}
	oy := u.overlayOriginY(clientH, len(u.overlayCells))
	oh := int32(len(u.overlayCells)) * ch
	if py < oy || py >= oy+oh {
		return false
	}
	if px < 0 {
		return false
	}
	cy := int((py - oy) / ch)
	cx := int(px / cw)
	if cy < 0 || cy >= len(u.overlayCells) {
		return false
	}
	row := u.overlayCells[cy]
	if cx < 0 || cx >= len(row) {
		return false
	}
	cell := row[cx]
	empty := cell.Ch == 0 || cell.Ch == ' '
	if empty && isTransparentOverlayBG(cell.BR, cell.BG, cell.BB) {
		return false
	}
	return true
}

// persistNotesIfDirty flushes the notes bank to notes.json when dirty.
func (u *winUI) persistNotesIfDirty() {
	if u == nil || !u.chrome.NotesDirty() {
		return
	}
	u.persistNotes()
}

// persistNotes always writes the bank (exit path / force).
func (u *winUI) persistNotes() {
	if u == nil {
		return
	}
	m := u.chrome
	bank := m.NotesSnapshot()
	if err := chrome.SaveNotesBank(bank); err != nil {
		log.Warn("notes save failed", "err", err, "path", chrome.NotesPath())
		u.chrome = m
		return
	}
	m.ClearNotesDirty()
	u.chrome = m
}

// paintOverlay draws the floating settings/palette card over the shell.
func (u *winUI) paintOverlay(hdc win.HDC, rect win.RECT) {
	defer applog.Recover("paintOverlay", false)
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
			if ct == nil {
				u.overlayCells = nil
				return
			}
			ccols, crows := ct.Size()
			// Workspace is near-full-height; don't clip the message viewport.
			maxOverlayRows := 48
			if u.chrome.WorkspaceOpen {
				maxOverlayRows = 96
			}
			if crows > maxOverlayRows {
				crows = maxOverlayRows
			}
			if crows < 1 {
				crows = 1
			}
			if ccols < 1 {
				ccols = 1
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
	oy := u.overlayOriginY(rect.Bottom-rect.Top, len(u.overlayCells))
	// Dim settings: full solid panel. Workspace: interior holes only (no side bars).
	u.paintChromeCellsEx(hdc, rect, u.overlayCells, 0, oy, false, u.solidOverlayPanel(), u.solidOverlayInterior())
	// Notes editor caret: same block/underline/bar as the terminal cursor.
	u.paintNotesCaret(hdc, oy)
}

// chromeCellOriginX matches paintChromeCells (ox + 4 + col*cw). The notes
// caret must use the same origin or it paints a second ghost glyph beside
// the real character.
const chromeCellOriginX int32 = 4

// paintNotesCaret draws the notes body/title caret using cfg.Cursor.
//
// Block: solid reverse-video over the insertion cell only (no second TextOut
// offset from the grid — that was doubling glyphs). The overlay already has
// the character; we cover that cell then redraw once at the exact same pixel
// origin paintChromeCells uses.
func (u *winUI) paintNotesCaret(hdc win.HDC, overlayOY int32) {
	if u == nil || hdc == 0 || !u.chrome.NotesOpen {
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
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Same pixel rect as paintChromeCells for this cell.
	x := chromeCellOriginX + int32(cx)*cw
	y := overlayOY + int32(cy)*ch
	cellRect := win.RECT{Left: x, Top: y, Right: x + cw, Bottom: y + ch}

	a := u.caretAlpha()
	if a <= 0 {
		return
	}

	switch u.cfg.Cursor {
	case config.CursorUnderline:
		th := ch / 8
		if th < 2 {
			th = 2
		}
		cr, cg, cb := blendRGB(chrome.PanelR, chrome.PanelG, chrome.PanelB, chrome.PrimR, chrome.PrimG, chrome.PrimB, a)
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: x, Top: y + ch - th, Right: x + cw, Bottom: y + ch}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
		return
	case config.CursorBar:
		th := cw / 5
		if th < 2 {
			th = 2
		}
		cr, cg, cb := blendRGB(chrome.PanelR, chrome.PanelG, chrome.PanelB, chrome.PrimR, chrome.PrimG, chrome.PrimB, a)
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			fillRect(hdc, win.RECT{Left: x, Top: y, Right: x + th, Bottom: y + ch}, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
		return
	}

	// Block: opaque cover of the cell, then one glyph at the same origin as
	// the grid paint (insert point = this cell; typed chars land here).
	var cell cellPix
	have := false
	if cy >= 0 && cy < len(u.overlayCells) && cx >= 0 && cx < len(u.overlayCells[cy]) {
		cell = u.overlayCells[cy][cx]
		have = true
	}
	bgR, bgG, bgB := chrome.PanelR, chrome.PanelG, chrome.PanelB
	if have && (cell.BR != 0 || cell.BG != 0 || cell.BB != 0) {
		bgR, bgG, bgB = cell.BR, cell.BG, cell.BB
	}
	// Near-opaque so the original glyph is fully covered (no double).
	fillA := a
	if fillA < 0.92 {
		fillA = 0.92
	}
	fillR, fillG, fillB := blendRGB(bgR, bgG, bgB, chrome.PrimR, chrome.PrimG, chrome.PrimB, fillA)
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(fillR, fillG, fillB)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		fillRect(hdc, cellRect, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
	if !have || cell.Ch == 0 || cell.Ch == ' ' {
		return
	}
	// Single redraw of the covered glyph, same path as paintChromeCells.
	glR, glG, glB := chrome.OnPrimR, chrome.OnPrimG, chrome.OnPrimB
	if glR == 0 && glG == 0 && glB == 0 {
		glR, glG, glB = 12, 12, 14
	}
	if drawCellGlyph(hdc, cell.Ch, cellRect, glR, glG, glB) {
		return
	}
	s, err := syscall.UTF16FromString(string(cell.Ch))
	if err != nil || len(s) < 2 {
		return
	}
	u.selectFontForRune(hdc, cell.Ch, cell.Bold)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(glR, glG, glB))
	win.TextOut(hdc, cellRect.Left, cellRect.Top, &s[0], int32(len(s)-1))
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
}

// teaKeyFromWin maps Win32 navigation keys into Bubble Tea messages for the
// palette/notes. Printable text arrives via WM_CHAR so filter typing works.
// Ctrl+A/C/X/V for notes are handled separately (clipboard needs the host).
func teaKeyFromWin(wParam uintptr, ctrl, shift bool) *tea.KeyMsg {
	switch wParam {
	case win.VK_ESCAPE:
		km := tea.KeyMsg{Type: tea.KeyEsc}
		return &km
	case win.VK_RETURN:
		km := tea.KeyMsg{Type: tea.KeyEnter}
		return &km
	case win.VK_UP:
		t := tea.KeyUp
		if ctrl && shift {
			t = tea.KeyCtrlShiftUp
		} else if ctrl {
			t = tea.KeyCtrlUp
		} else if shift {
			t = tea.KeyShiftUp
		}
		km := tea.KeyMsg{Type: t}
		return &km
	case win.VK_DOWN:
		t := tea.KeyDown
		if ctrl && shift {
			t = tea.KeyCtrlShiftDown
		} else if ctrl {
			t = tea.KeyCtrlDown
		} else if shift {
			t = tea.KeyShiftDown
		}
		km := tea.KeyMsg{Type: t}
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
		t := tea.KeyLeft
		if ctrl && shift {
			t = tea.KeyCtrlShiftLeft
		} else if ctrl {
			t = tea.KeyCtrlLeft
		} else if shift {
			t = tea.KeyShiftLeft
		}
		km := tea.KeyMsg{Type: t}
		return &km
	case win.VK_RIGHT:
		t := tea.KeyRight
		if ctrl && shift {
			t = tea.KeyCtrlShiftRight
		} else if ctrl {
			t = tea.KeyCtrlRight
		} else if shift {
			t = tea.KeyShiftRight
		}
		km := tea.KeyMsg{Type: t}
		return &km
	case win.VK_DELETE:
		km := tea.KeyMsg{Type: tea.KeyDelete}
		return &km
	case win.VK_HOME:
		t := tea.KeyHome
		if ctrl && shift {
			t = tea.KeyCtrlShiftHome
		} else if ctrl {
			t = tea.KeyCtrlHome
		} else if shift {
			t = tea.KeyShiftHome
		}
		km := tea.KeyMsg{Type: t}
		return &km
	case win.VK_END:
		t := tea.KeyEnd
		if ctrl && shift {
			t = tea.KeyCtrlShiftEnd
		} else if ctrl {
			t = tea.KeyCtrlEnd
		} else if shift {
			t = tea.KeyShiftEnd
		}
		km := tea.KeyMsg{Type: t}
		return &km
	case win.VK_F2:
		km := tea.KeyMsg{Type: tea.KeyF2}
		return &km
	}
	return nil
}
