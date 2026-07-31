//go:build windows

package ui

import (
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

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

	tabBarH     = 28 // pixels reserved for the tab strip
	maxTabs     = 16
)

// Run opens a native Win32 window with one shell tab (more via Ctrl+Shift+T).
func Run() error {
	cols, rows := 100, 28 // rows exclude tab bar visually
	ui := &winUI{
		cols:       cols,
		rows:       rows,
		cfg:        config.Default(),
		blinkStart: time.Now(),
		nextTabID:  0,
	}
	ui.alive.Store(true)
	t, err := newTab(ui.nextTabID, cols, rows)
	if err != nil {
		return err
	}
	ui.nextTabID++
	ui.tabs = []*tab{t}
	ui.active = 0
	return ui.loop()
}

type winUI struct {
	tabs      []*tab
	active    int
	nextTabID int

	hwnd   win.HWND
	font   win.HFONT
	width  int32
	height int32
	cols   int
	rows   int
	cfg    config.Config
	// last measured cell size (for hit-testing)
	metricW int32
	metricH int32

	blinkStart    time.Time
	alive         atomic.Bool
	lastBackspace time.Time // rate-limit BS so a queued KEYDOWN burst cannot wipe the line
	selecting     bool

	// Reused double-buffer (recreated on resize) to avoid GDI thrash.
	memDC  win.HDC
	memBmp win.HBITMAP
	memW   int32
	memH   int32
}

func (u *winUI) activeTab() *tab {
	if u.active < 0 || u.active >= len(u.tabs) {
		return nil
	}
	return u.tabs[u.active]
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
	u.font = createFont()
	registerUI(hwnd, u)

	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)

	// Start I/O for the first tab; more tabs start in newTabUI.
	if t := u.activeTab(); t != nil {
		t.startWorkers(u)
	}
	go u.blinkLoop()

	var msg win.MSG
	for {
		ret := win.GetMessage(&msg, 0, 0, 0)
		if ret == 0 {
			break
		}
		if ret == -1 {
			return lastErr("GetMessage")
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
		return
	}
	t, err := newTab(u.nextTabID, u.cols, u.rows)
	if err != nil {
		return
	}
	u.nextTabID++
	u.tabs = append(u.tabs, t)
	u.active = len(u.tabs) - 1
	t.startWorkers(u)
	u.selecting = false
	setWindowTitle(u.hwnd, "suzuri — "+t.title)
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
	win.InvalidateRect(u.hwnd, nil, false)
}

func (u *winUI) hitTab(px int32) int {
	// Equal-width tabs across the bar.
	if len(u.tabs) == 0 || u.width <= 0 {
		return -1
	}
	tw := u.width / int32(len(u.tabs))
	if tw < 1 {
		tw = 1
	}
	i := int(px / tw)
	if i < 0 || i >= len(u.tabs) {
		return -1
	}
	return i
}


func (u *winUI) handle(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriBytes:
		u.drainAndParse(int(wParam))
		return 0

	case wmSuzuriBlink:
		win.InvalidateRect(hwnd, nil, false)
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

	case win.WM_SIZE:
		u.width = int32(win.LOWORD(uint32(lParam)))
		u.height = int32(win.HIWORD(uint32(lParam)))
		if u.width > 0 && u.height > 0 {
			cols := int(u.width / cellW)
			rows := int((u.height - int32(tabBarH)) / cellH)
			if cols < 20 {
				cols = 20
			}
			if rows < 5 {
				rows = 5
			}
			if cols != u.cols || rows != u.rows {
				u.cols, u.rows = cols, rows
				for _, t := range u.tabs {
					t.resize(cols, rows)
				}
			}
		}
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
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
		if py < int32(tabBarH) {
			if i := u.hitTab(px); i >= 0 {
				u.active = i
				u.selecting = false
				if t := u.activeTab(); t != nil {
					t.sel.clear()
					setWindowTitle(u.hwnd, "suzuri — "+t.title)
				}
				win.InvalidateRect(hwnd, nil, false)
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
		u.paint(hwnd)
		return 0

	case win.WM_CLOSE:
		win.DestroyWindow(hwnd)
		return 0

	case win.WM_DESTROY:
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
		u.hwnd = 0
		win.PostQuitMessage(0)
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (u *winUI) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	var rect win.RECT
	win.GetClientRect(hwnd, &rect)

	tab := u.activeTab()
	if tab == nil {
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

	w := rect.Right - rect.Left
	h := rect.Bottom - rect.Top
	if !u.ensureBackbuffer(hdc, w, h) {
		u.blitGrid(hdc, rect, grid, cur.X, curY, curVis)
		return
	}
	u.blitGrid(u.memDC, rect, grid, cur.X, curY, curVis)
	win.BitBlt(hdc, 0, 0, w, h, u.memDC, 0, 0, win.SRCCOPY)
	// Tab strip on top of the terminal bitmap.
	oldF := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	u.paintTabBar(hdc, rect)
	win.SelectObject(hdc, oldF)
}

// blitGrid paints colored cells at fixed pitch. Selection uses one brush;
// text runs flush when FG/BG changes so we keep TextOut count low.
func (u *winUI) blitGrid(hdc win.HDC, rect win.RECT, grid [][]cellPix, curX, curY int, curVis bool) {
	tab := u.activeTab()
	if tab == nil {
		return
	}

	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.BLACK_BRUSH)))
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)

	cw, ch := int32(cellW), int32(cellH)
	var tm win.TEXTMETRIC
	if win.GetTextMetrics(hdc, &tm) {
		if tm.TmAveCharWidth > 0 {
			cw = tm.TmAveCharWidth
		}
		if tm.TmHeight > 0 {
			ch = tm.TmHeight
		}
	}
	const padX int32 = 4
	padY := int32(tabBarH)+2
	u.metricW, u.metricH = cw, ch

	selBrush := win.HBRUSH(0)
	if tab.sel.active && !tab.sel.empty() {
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(40, 80, 160)}
		selBrush = win.CreateBrushIndirect(&lb)
	}
	if selBrush != 0 {
		defer win.DeleteObject(win.HGDIOBJ(selBrush))
	}

	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))

	for y, row := range grid {
		// Non-default backgrounds first (one rect per run of same BG).
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
				old := win.SelectObject(hdc, win.HGDIOBJ(brush))
				px0 := padX + int32(br.x0)*cw
				px1 := padX + int32(br.x1+1)*cw
				py := padY + int32(y)*ch
				win.Rectangle_(hdc, px0, py, px1, py+ch)
				win.SelectObject(hdc, old)
				win.DeleteObject(win.HGDIOBJ(brush))
			}
		}

		// Selection overlay
		if selBrush != 0 {
			for x := range row {
				absY := tab.sb.absLine(y, u.rows, u.rows)
				if tab.sel.containsAbs(x, absY) {
					old := win.SelectObject(hdc, win.HGDIOBJ(selBrush))
					px := padX + int32(x)*cw
					py := padY + int32(y)*ch
					win.Rectangle_(hdc, px, py, px+cw, py+ch)
					win.SelectObject(hdc, old)
				}
			}
		}

		// Text runs: same FG (+ selection) and no spaces.
		type tRun struct {
			x0         int
			text       []rune
			fr, fg, fb byte
			sel        bool
		}
		var runs []tRun
		for x, c := range row {
			r := c.Ch
			if r == 0 {
				r = ' '
			}
			absY := tab.sb.absLine(y, u.rows, u.rows)
			sel := tab.sel.containsAbs(x, absY)
			if r == ' ' {
				continue
			}
			fr, fg, fb := c.FR, c.FG, c.FB
			if sel {
				fr, fg, fb = 255, 255, 255
			}
			n := len(runs)
			if n > 0 {
				last := &runs[n-1]
				if last.sel == sel && last.fr == fr && last.fg == fg && last.fb == fb && last.x0+len(last.text) == x {
					last.text = append(last.text, r)
					continue
				}
			}
			runs = append(runs, tRun{x0: x, text: []rune{r}, fr: fr, fg: fg, fb: fb, sel: sel})
		}
		win.SetBkMode(hdc, win.TRANSPARENT)
		for i := range runs {
			rn := &runs[i]
			s, err := syscall.UTF16FromString(string(rn.text))
			if err != nil || len(s) < 2 {
				continue
			}
			win.SetTextColor(hdc, win.RGB(rn.fr, rn.fg, rn.fb))
			px := padX + int32(rn.x0)*cw
			py := padY + int32(y)*ch
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
		px := padX + int32(curX)*cw
		py := padY + int32(curY)*ch
		elapsed := time.Since(u.blinkStart).Seconds()
		period := cursorBlinkPeriod.Seconds()
		alpha := 0.5 + 0.5*math.Sin(2*math.Pi*(elapsed/period))
		level := byte(50 + alpha*205)
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(level, level, level)}
		if brush := win.CreateBrushIndirect(&lb); brush != 0 {
			oldBr := win.SelectObject(hdc, win.HGDIOBJ(brush))
			win.Rectangle_(hdc, px, py, px+cw, py+ch-1)
			win.SelectObject(hdc, oldBr)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}
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
	// v0: only draw scripts Consolas/Cascadia cover well. Everything else
	// becomes a space so we never show the font's "missing glyph" tofu.
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

func createFont() win.HFONT {
	// Prefer Cascadia Mono (ships with Windows Terminal) for cleaner coverage.
	for _, name := range []string{"Cascadia Mono", "Cascadia Code", "Consolas", "Lucida Console"} {
		var lf win.LOGFONT
		lf.LfHeight = -16
		lf.LfWeight = win.FW_NORMAL
		lf.LfCharSet = win.DEFAULT_CHARSET
		lf.LfOutPrecision = win.OUT_DEFAULT_PRECIS
		lf.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
		lf.LfQuality = win.CLEARTYPE_QUALITY
		lf.LfPitchAndFamily = win.FIXED_PITCH | win.FF_MODERN
		face, err := syscall.UTF16FromString(name)
		if err != nil {
			continue
		}
		copy(lf.LfFaceName[:], face)
		if h := win.CreateFontIndirect(&lf); h != 0 {
			return h
		}
	}
	return 0
}

func lastErr(op string) error {
	return windows.Errno(win.GetLastError())
}

var (
	modUser32       = windows.NewLazySystemDLL("user32.dll")
	procSetWindowText = modUser32.NewProc("SetWindowTextW")
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
	padY := int32(tabBarH) + 2
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
		return false
	}
	u.memBmp = win.CreateCompatibleBitmap(hdc, w, h)
	if u.memBmp == 0 {
		win.DeleteDC(u.memDC)
		u.memDC = 0
		return false
	}
	win.SelectObject(u.memDC, win.HGDIOBJ(u.memBmp))
	u.memW, u.memH = w, h
	return true
}

func (u *winUI) paintTabBar(hdc win.HDC, rect win.RECT) {
	if len(u.tabs) == 0 {
		return
	}
	n := int32(len(u.tabs))
	tw := rect.Right / n
	if tw < 40 {
		tw = 40
	}
	for i, t := range u.tabs {
		x0 := int32(i) * tw
		x1 := x0 + tw
		if i == len(u.tabs)-1 {
			x1 = rect.Right
		}
		bg := win.RGB(40, 40, 40)
		fg := win.RGB(180, 180, 180)
		if i == u.active {
			bg = win.RGB(55, 55, 70)
			fg = win.RGB(240, 240, 240)
		}
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: bg}
		if br := win.CreateBrushIndirect(&lb); br != 0 {
			old := win.SelectObject(hdc, win.HGDIOBJ(br))
			win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
			win.Rectangle_(hdc, x0, 0, x1, int32(tabBarH))
			win.SelectObject(hdc, old)
			win.DeleteObject(win.HGDIOBJ(br))
		}
		label := t.title
		if label == "" {
			label = "shell"
		}
		s, err := syscall.UTF16FromString(label)
		if err == nil && len(s) > 1 {
			win.SetBkMode(hdc, win.TRANSPARENT)
			win.SetTextColor(hdc, fg)
			win.TextOut(hdc, x0+8, 6, &s[0], int32(len(s)-1))
		}
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(70, 70, 70)}
	if br := win.CreateBrushIndirect(&lb); br != 0 {
		old := win.SelectObject(hdc, win.HGDIOBJ(br))
		win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
		win.Rectangle_(hdc, 0, int32(tabBarH)-1, rect.Right, int32(tabBarH))
		win.SelectObject(hdc, old)
		win.DeleteObject(win.HGDIOBJ(br))
	}
}
