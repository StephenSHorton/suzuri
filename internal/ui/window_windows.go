//go:build windows

package ui

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

const (
	className = "SuzuriTerminalClass"
	appTitle  = "suzuri（硯）"

	// UI-thread work items (never do ConPTY/VT work from foreign threads).
	wmSuzuriBytes  = win.WM_APP + 1 // drain incoming PTY byte queue into vt10x
	wmSuzuriBlink  = win.WM_APP + 2
	wmSuzuriClosed = win.WM_APP + 3 // session read ended

	cursorBlinkPeriod = 1200 * time.Millisecond
	cursorBlinkTick   = 500 * time.Millisecond

	cellW = 9
	cellH = 18

	maxTabs         = 16
	tabBarFallback  = 36 // used before first paint measures font
)

// Run opens a native Win32 window with one shell tab (more via Ctrl+Shift+T).
// Chrome (tabs, status, palette) is a Charm Bubble Tea model; the shell is VT.
func Run() error {
	cols, rows := 100, 28
	cfg := config.Default()
	log.Info("ui.Run", "cols", cols, "rows", rows, "font", cfg.FontFace, "fontPx", cfg.FontSizePx)
	ui := &winUI{
		cols:       cols,
		rows:       rows,
		cfg:        cfg,
		blinkStart: time.Now(),
		nextTabID:  0,
		chrome:     chrome.New(cols),
	}
	ui.alive.Store(true)
	t, err := newTab(ui.nextTabID, cols, rows)
	if err != nil {
		log.Error("first tab failed", "err", err)
		return err
	}
	ui.nextTabID++
	ui.tabs = []*tab{t}
	ui.active = 0
	ui.syncChrome()
	return ui.loop()
}

type winUI struct {
	tabs      []*tab
	active    int
	nextTabID int
	chrome    chrome.Model // Charm UI: tabs, status, palette

	hwnd     win.HWND
	font     win.HFONT
	fontBold win.HFONT
	width    int32
	height   int32
	cols   int
	rows   int
	cfg    config.Config
	// last measured cell size (for hit-testing)
	metricW int32
	metricH int32
	chromePx int32 // pixel height of Charm chrome

	blinkStart    time.Time
	alive         atomic.Bool
	lastBackspace time.Time // rate-limit BS so a queued KEYDOWN burst cannot wipe the line
	selecting     bool

	// Reused double-buffer (recreated on resize) to avoid GDI thrash.
	// memOldBmp is the object that was in memDC before memBmp — must be
	// re-selected before DeleteObject(memBmp) or GDI can AV later.
	memDC     win.HDC
	memBmp    win.HBITMAP
	memOldBmp win.HGDIOBJ
	memW      int32
	memH      int32

	// Chrome paint cache: RenderToTerm+Lip Gloss every WM_PAINT is expensive
	// and stress-tests GDI when the window is reactivated after idle.
	chromeDirty bool
	chromeCols  int
	chromeCells [][]cellPix // [row][col]
}

func (u *winUI) activeTab() *tab {
	if u.active < 0 || u.active >= len(u.tabs) {
		return nil
	}
	return u.tabs[u.active]
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
}

func (u *winUI) chromePixelHeight() int32 {
	rows := u.chrome.RowCount()
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	return int32(rows) * ch
}

func (u *winUI) applyChromeAction(act chrome.HostAction, index int) {
	switch act {
	case chrome.ActionNewTab:
		u.newTabUI()
	case chrome.ActionCloseTab:
		if t := u.activeTab(); t != nil {
			u.closeTabUI(t.id)
		}
	case chrome.ActionNextTab:
		u.switchTab(1)
	case chrome.ActionPrevTab:
		u.switchTab(-1)
	case chrome.ActionSelectTab:
		if index >= 0 && index < len(u.tabs) {
			u.active = index
			u.selecting = false
			if t := u.activeTab(); t != nil {
				t.sel.clear()
			}
		}
	case chrome.ActionQuit:
		if u.hwnd != 0 {
			win.DestroyWindow(u.hwnd)
		}
	}
	u.syncChrome()
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

	cw, ch := int32(u.cols*cellW+24), int32(u.rows*cellH+48)
	hwnd := win.CreateWindowEx(
		0,
		cname,
		title,
		win.WS_OVERLAPPEDWINDOW|win.WS_VISIBLE,
		win.CW_USEDEFAULT,
		win.CW_USEDEFAULT,
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
	face := fontFaceName(u.font)
	log.Info("window created", "hwnd", uintptr(hwnd), "font", face, "want", u.cfg.FontFace)
	registerUI(hwnd, u)

	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)

	// Start I/O for the first tab; more tabs start in newTabUI.
	if t := u.activeTab(); t != nil {
		t.startWorkers(u)
		log.Info("tab started", "id", t.id, "pid", t.sess.Pid())
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

func (u *winUI) newTabUI() {
	if len(u.tabs) >= maxTabs {
		log.Warn("max tabs reached", "max", maxTabs)
		return
	}
	t, err := newTab(u.nextTabID, u.cols, u.rows)
	if err != nil {
		log.Error("new tab failed", "err", err)
		return
	}
	u.nextTabID++
	u.tabs = append(u.tabs, t)
	u.active = len(u.tabs) - 1
	t.startWorkers(u)
	u.selecting = false
	setWindowTitle(u.hwnd, "suzuri — "+t.title)
	u.syncChrome()
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
	u.tabs[idx].close()
	u.tabs = append(u.tabs[:idx], u.tabs[idx+1:]...)
	if len(u.tabs) == 0 {
		win.DestroyWindow(u.hwnd)
		return
	}
	if u.active >= len(u.tabs) {
		u.active = len(u.tabs) - 1
	} else if u.active > idx {
		u.active--
	}
	if t := u.activeTab(); t != nil {
		setWindowTitle(u.hwnd, "suzuri — "+t.title)
	}
	u.syncChrome()
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
	win.InvalidateRect(u.hwnd, nil, false)
}

// hitTab maps an x pixel to a tab index using chrome.TabBounds (same layout as View).
func (u *winUI) hitTab(px int32) int {
	if len(u.tabs) == 0 {
		return -1
	}
	u.syncChrome()
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	const padX int32 = 4
	cellX := int((px - padX) / cw)
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


func (u *winUI) handle(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriBytes:
		u.drainAndParse(int(wParam))
		return 0

	case wmSuzuriBlink:
		if u.alive.Load() {
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case wmSuzuriClosed:
		id := int(wParam)
		if t := u.tabByID(id); t != nil {
			_, _ = t.term.Write([]byte("\r\n[suzuri] session ended\r\n"))
			if u.activeTab() == t {
				win.InvalidateRect(hwnd, nil, false)
			}
		}
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
			// After long suspend the backbuffer DC can be stale — rebuild on focus.
			u.releaseBackbuffer()
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_SIZE:
		u.width = int32(win.LOWORD(uint32(lParam)))
		u.height = int32(win.HIWORD(uint32(lParam)))
		if u.width > 0 && u.height > 0 {
			cw, ch := u.metricW, u.metricH
			if cw < 1 {
				cw = cellW
			}
			if ch < 1 {
				ch = cellH
			}
			const padX int32 = 4
			cols := int((u.width - padX) / cw)
			if cols < 20 {
				cols = 20
			}
			u.chrome.Width = cols
			u.chrome = u.chrome.UpdateChrome(tea.WindowSizeMsg{Width: cols, Height: 24}).Model
			u.markChromeDirty()
			u.chromePx = u.chromePixelHeight()
			rows := int((u.height - u.chromePx) / ch)
			if rows < 5 {
				rows = 5
			}
			if rows != u.rows || cols != u.cols {
				u.cols = cols
				u.rows = rows
				for _, t := range u.tabs {
					t.resize(cols, rows)
				}
			} else {
				u.cols = cols
			}
		}
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
		// Charm palette filter: printable runes only (specials via KEYDOWN).
		if u.chrome.PaletteOpen {
			if ch >= 32 && ch != 0x7f {
				km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
				r := u.chrome.UpdateChrome(km)
				u.chrome = r.Model
				u.markChromeDirty()
				u.applyChromeAction(r.Action, r.Index)
				u.syncChrome()
				u.chromePx = u.chromePixelHeight()
				win.InvalidateRect(hwnd, nil, false)
			}
			return 0
		}
		switch ch {
		case 0x08, 0x09, 0x0a, 0x7f:
			return 0
		case 0x0d:
			u.sendKey([]byte("\r"))
			return 0
		}
		if ch == 0x03 {
			tab := u.activeTab()
			if tab == nil || tab.sel.empty() {
				u.sendKey([]byte{0x03})
			}
			return 0
		}
		if ch == 0x16 {
			return 0
		}
		if ch >= 32 && ch != 0x7f {
			var buf [4]byte
			n := utf8Encode(buf[:], ch)
			u.sendKey(buf[:n])
		}
		return 0

	case win.WM_KEYDOWN:
		ctrl := win.GetKeyState(win.VK_CONTROL) < 0
		shift := win.GetKeyState(win.VK_SHIFT) < 0
		tab := u.activeTab()

		// Charm palette owns navigation keys while open (text via WM_CHAR).
		if u.chrome.PaletteOpen {
			if km := teaKeyFromWin(wParam, ctrl, shift); km != nil {
				r := u.chrome.UpdateChrome(*km)
				u.chrome = r.Model
				u.markChromeDirty()
				u.applyChromeAction(r.Action, r.Index)
				u.syncChrome()
				u.chromePx = u.chromePixelHeight()
				win.InvalidateRect(hwnd, nil, false)
			}
			return 0
		}
		if ctrl && !shift && (wParam == 'K' || wParam == 'k' || wParam == 'P' || wParam == 'p') {
			r := u.chrome.UpdateChrome(chrome.OpenPaletteMsg{})
			u.chrome = r.Model
			u.markChromeDirty()
			u.chromePx = u.chromePixelHeight()
			win.InvalidateRect(hwnd, nil, false)
			return 0
		}

		if ctrl && shift && (wParam == 'T' || wParam == 't') {
			u.newTabUI()
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
		if tab == nil {
			return 0
		}
		switch wParam {
		case win.VK_UP:
			u.sendKey([]byte("\x1b[A"))
		case win.VK_DOWN:
			u.sendKey([]byte("\x1b[B"))
		case win.VK_RIGHT:
			u.sendKey([]byte("\x1b[C"))
		case win.VK_LEFT:
			u.sendKey([]byte("\x1b[D"))
		case win.VK_DELETE:
			u.sendKey([]byte("\x1b[3~"))
		case win.VK_HOME:
			u.sendKey([]byte("\x1b[H"))
		case win.VK_END:
			u.sendKey([]byte("\x1b[F"))
		case win.VK_PRIOR:
			tab.sb.scrollBy(u.rows/2, u.rows)
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_NEXT:
			tab.sb.scrollBy(-(u.rows / 2), u.rows)
			win.InvalidateRect(hwnd, nil, false)
		case win.VK_ESCAPE:
			u.sendKey([]byte{0x1b})
		case win.VK_BACK:
			u.handleBackspace(hwnd, lParam)
		case win.VK_TAB:
			u.sendKey([]byte{'\t'})
		case 'C', 'c':
			if ctrl && !shift {
				if !tab.sel.empty() {
					u.copySelection()
				} else {
					u.sendKey([]byte{0x03})
				}
				return 0
			}
		case 'V', 'v':
			if ctrl && !shift {
				u.pasteClipboard()
				return 0
			}
		}
		return 0

	case win.WM_MOUSEWHEEL:
		tab := u.activeTab()
		if tab == nil {
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
		// Only the first chrome row is the tab strip (status/palette below).
		chH := u.metricH
		if chH < 1 {
			chH = cellH
		}
		if py < u.chromePixelHeight() {
			if py < chH {
				if i := u.hitTab(px); i >= 0 {
					u.active = i
					u.selecting = false
					if t := u.activeTab(); t != nil {
						t.sel.clear()
						setWindowTitle(u.hwnd, "suzuri — "+t.title)
					}
					u.syncChrome()
					win.InvalidateRect(hwnd, nil, false)
				}
			}
			return 0
		}
		tab := u.activeTab()
		if tab == nil {
			return 0
		}
		x, y := u.pixelToCell(px, py)
		absY := tab.sb.absLine(y, u.rows, u.rows)
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
			absY := tab.sb.absLine(y, u.rows, u.rows)
			tab.sel.x1, tab.sel.y1 = x, absY
			win.InvalidateRect(hwnd, nil, false)
		}
		return 0

	case win.WM_LBUTTONUP:
		tab := u.activeTab()
		if tab != nil && u.selecting {
			x, y := u.pixelToCell(int32(win.LOWORD(uint32(lParam))), int32(win.HIWORD(uint32(lParam))))
			absY := tab.sb.absLine(y, u.rows, u.rows)
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
		win.DestroyWindow(hwnd)
		return 0

	case win.WM_DESTROY:
		log.Info("WM_DESTROY — tearing down", "tabs", len(u.tabs))
		u.alive.Store(false)
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

	tab := u.activeTab()
	if tab == nil {
		fillRect(hdc, rect, win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))
		return
	}
	if u.font == 0 {
		log.Error("paint with null font — skipping draw")
		return
	}

	// Viewport = history + live screen (live cells carry FG/BG/bold).
	grid := tab.sb.viewCells(tab.term, u.rows)
	cur := tab.term.Cursor()
	curVis := tab.term.CursorVisible() && tab.sb.atBottom()
	curY := cur.Y
	if !tab.sb.atBottom() {
		curVis = false
	}

	draw := func(dest win.HDC) {
		u.blitGrid(dest, rect, grid, cur.X, curY, curVis)
		// Chrome into the same buffer so one BitBlt presents a full frame.
		oldF := win.SelectObject(dest, win.HGDIOBJ(u.font))
		u.paintChrome(dest, rect)
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
	padY := u.chromePixelHeight()
	if padY < 1 {
		padY = int32(tabBarFallback)
	}
	u.metricW, u.metricH = cw, ch
	u.chromePx = padY

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
			absY := tab.sb.absLine(y, u.rows, u.rows)
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
			absY := tab.sb.absLine(y, u.rows, u.rows)
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
			win.SetTextColor(hdc, win.RGB(fr, fg, fb))
			win.TextOut(hdc, px, py, &s[0], int32(len(s)-1))
		}
	}

	if u.cfg.Cursor == config.CursorBlock && curVis {
		if curX < 0 {
			curX = 0
		}
		if curY < 0 {
			curY = 0
		}
		elapsed := time.Since(u.blinkStart).Seconds()
		period := cursorBlinkPeriod.Seconds()
		alpha := 0.5 + 0.5*math.Sin(2*math.Pi*(elapsed/period))
		level := byte(50 + alpha*205)
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(level, level, level)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			r := win.RECT{
				Left:   padX + int32(curX)*cw,
				Top:    padY + int32(curY)*ch,
				Right:  padX + int32(curX+1)*cw,
				Bottom: padY + int32(curY+1)*ch,
			}
			fillRect(hdc, r, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
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

func displayRune(r rune) rune {
	if r == 0 || r == 0xFFFD {
		return ' '
	}
	// C0 / DEL / C1 controls
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return ' '
	}
	// Private Use (Nerd Fonts etc.) → empty, not a □ mystery box
	if r >= 0xE000 && r <= 0xF8FF {
		return ' '
	}
	if r >= 0xF0000 {
		return ' '
	}
	// v0: only draw scripts Cascadia Mono (and fallbacks) cover well.
	// Everything else becomes a space so we never show missing-glyph tofu.
	if r > 0x024F && // beyond Latin Extended-B
		!(r >= 0x2500 && r <= 0x259F) && // box / block drawing
		!(r >= 0x2000 && r <= 0x206F) { // general punctuation (keep sparse)
		// Allow a few common prompt marks; drop the rest.
		switch r {
		case '✓', '✗', '→', '←', '▶', '❯', 'λ', '•':
			// still may miss in Consolas — prefer ASCII fallback
			return ' '
		default:
			if !unicode.In(r, unicode.Latin, unicode.Common) {
				return ' '
			}
		}
	}
	if unicode.Is(unicode.Cf, r) {
		return ' '
	}
	if !unicode.IsPrint(r) && r != ' ' {
		return ' '
	}
	return r
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

// Preferred monospaced faces. Cascadia Mono is default (ships with Windows
// Terminal / modern Windows); CreateFont always "succeeds" via substitution,
// so we verify the face with GetTextFaceW and fall through if GDI faked it.
var fontFallbacks = []string{
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
	padY := u.chromePixelHeight()
	if padY < 1 {
		padY = int32(tabBarFallback)
	}
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
	tab := u.activeTab()
	if tab == nil {
		return
	}
	text, err := getClipboardText(u.hwnd)
	if err != nil || text == "" {
		return
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	tab.sendKey([]byte(text))
	tab.sb.stickBottom()
}

func (u *winUI) handleBackspace(hwnd win.HWND, lParam uintptr) {
	wasDown := (uint32(lParam) & (1 << 30)) != 0
	now := time.Now()
	if wasDown && now.Sub(u.lastBackspace) < 30*time.Millisecond {
		u.drainQueuedBackspaces(hwnd)
		return
	}
	u.lastBackspace = now
	u.sendKey([]byte{0x7f})
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
	if u.memDC != 0 {
		// Deselect our bitmap before deleting it — deleting a selected HBITMAP
		// is undefined and is a common source of delayed GDI AVs.
		if u.memOldBmp != 0 {
			win.SelectObject(u.memDC, u.memOldBmp)
			u.memOldBmp = 0
		} else if u.memBmp != 0 {
			// Fall back to stock monochrome bitmap if we lost the original.
			stock := win.GetStockObject(win.BLACK_BRUSH) // not a bitmap; use NULL via CreateCompatibleDC default
			_ = stock
			win.SelectObject(u.memDC, win.HGDIOBJ(0))
		}
		if u.memBmp != 0 {
			win.DeleteObject(win.HGDIOBJ(u.memBmp))
			u.memBmp = 0
		}
		win.DeleteDC(u.memDC)
		u.memDC = 0
	}
	u.memW, u.memH = 0, 0
}

func (u *winUI) ensureBackbuffer(hdc win.HDC, w, h int32) bool {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
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
	u.memW, u.memH = w, h
	return true
}

// paintChrome renders the Charm View() through a mini VT grid (Lip Gloss ANSI
// → cells) so tabs/status/palette use the same paint path as the shell.
func (u *winUI) paintChrome(hdc win.HDC, rect win.RECT) {
	if hdc == 0 {
		return
	}
	u.syncChrome()
	cols := u.cols
	if cols < 20 {
		cols = 20
	}
	// Cap chrome rows so a stuck palette can't allocate a huge grid.
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
	crows := len(cells)
	chromeH := int32(crows) * ch
	if chromeH > rect.Bottom-rect.Top {
		// Don't paint past the client area (minimized / tiny windows).
		chromeH = rect.Bottom - rect.Top
		if ch > 0 {
			crows = int(chromeH / ch)
		}
	}

	// Full-bleed bar under the whole chrome (including side padding).
	{
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(0x1f, 0x1f, 0x1f)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			r := win.RECT{Left: 0, Top: 0, Right: rect.Right, Bottom: chromeH}
			fillRect(hdc, r, brush)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}

	type bgRun struct {
		x0, x1  int
		r, g, b byte
	}
	for y := 0; y < crows; y++ {
		row := cells[y]
		var runs []bgRun
		for x := 0; x < len(row); x++ {
			cell := row[x]
			br, bg, bb := cell.BR, cell.BG, cell.BB
			if br == 0 && bg == 0 && bb == 0 {
				br, bg, bb = 0x1f, 0x1f, 0x1f
			}
			if n := len(runs); n > 0 && runs[n-1].x1 == x-1 &&
				runs[n-1].r == br && runs[n-1].g == bg && runs[n-1].b == bb {
				runs[n-1].x1 = x
				continue
			}
			runs = append(runs, bgRun{x0: x, x1: x, r: br, g: bg, b: bb})
		}
		for _, rn := range runs {
			lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(rn.r, rn.g, rn.b)}
			if brush := win.CreateBrushIndirect(&lb); brush != 0 {
				r := win.RECT{
					Left:   4 + int32(rn.x0)*cw,
					Top:    int32(y) * ch,
					Right:  4 + int32(rn.x1+1)*cw,
					Bottom: int32(y+1) * ch,
				}
				if rn.x0 == 0 {
					r.Left = 0
				}
				if rn.x1 >= len(row)-1 {
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
				Left:   4 + int32(x)*cw,
				Top:    int32(y) * ch,
				Right:  4 + int32(x+1)*cw,
				Bottom: int32(y+1) * ch,
			}
			if drawCellGlyph(hdc, r, cellRect, cell.FR, cell.FG, cell.FB) {
				continue
			}
			s, err := syscall.UTF16FromString(string(r))
			if err != nil || len(s) < 2 {
				continue
			}
			if cell.Bold && u.fontBold != 0 {
				win.SelectObject(hdc, win.HGDIOBJ(u.fontBold))
			} else if u.font != 0 {
				win.SelectObject(hdc, win.HGDIOBJ(u.font))
			}
			win.SetTextColor(hdc, win.RGB(cell.FR, cell.FG, cell.FB))
			win.TextOut(hdc, cellRect.Left, cellRect.Top, &s[0], int32(len(s)-1))
		}
	}
	if u.font != 0 {
		win.SelectObject(hdc, win.HGDIOBJ(u.font))
	}
	u.chromePx = chromeH
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
