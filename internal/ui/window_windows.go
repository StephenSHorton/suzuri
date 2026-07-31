//go:build windows

package ui

import (
	"math"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"
	"unsafe"

	"github.com/hinshun/vt10x"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/host"
)

const (
	className = "SuzuriTerminalClass"
	appTitle  = "suzuri（硯）"

	// UI-thread work items (never do ConPTY/VT work from foreign threads).
	wmSuzuriBytes  = win.WM_APP + 1 // drain incoming PTY byte queue into vt10x
	wmSuzuriBlink  = win.WM_APP + 2
	wmSuzuriClosed = win.WM_APP + 3 // session read ended

	cursorBlinkPeriod = 1200 * time.Millisecond
	cursorBlinkTick   = 200 * time.Millisecond

	cellW = 9
	cellH = 18
)

// Run opens a native Win32 window and drives the ConPTY session until closed.
func Run(sess *host.Session) error {
	cols, rows := 100, 30
	ui := &winUI{
		sess:       sess,
		cols:       cols,
		rows:       rows,
		cfg:        config.Default(),
		term:       vt10x.New(vt10x.WithSize(cols, rows)),
		writeCh:    make(chan []byte, 256),
		blinkStart: time.Now(),
	}
	ui.alive.Store(true)
	return ui.loop()
}

type winUI struct {
	sess *host.Session
	term vt10x.Terminal // ONLY touched on the UI thread

	hwnd   win.HWND
	font   win.HFONT
	width  int32
	height int32
	cols   int
	rows   int
	cfg    config.Config

	// PTY → UI: raw bytes only. VT parse runs on the UI thread.
	inMu  sync.Mutex
	inBuf []byte

	// UI → PTY: never WriteFile on the message loop.
	writeCh chan []byte

	blinkStart time.Time
	alive      atomic.Bool
	bytesMsg   atomic.Bool // coalesce wmSuzuriBytes
}

func (u *winUI) loop() error {
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

	go u.writeLoop()
	go u.readLoop()
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

func (u *winUI) writeLoop() {
	for b := range u.writeCh {
		if _, err := u.sess.Write(b); err != nil {
			return
		}
	}
}

func (u *winUI) sendKey(b []byte) {
	if !u.alive.Load() || len(b) == 0 {
		return
	}
	p := append([]byte(nil), b...)
	select {
	case u.writeCh <- p:
	default:
	}
}

func (u *winUI) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := u.sess.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			u.inMu.Lock()
			u.inBuf = append(u.inBuf, chunk...)
			// Cap runaway buffer (shell spam) so we don't OOM.
			if len(u.inBuf) > 1<<20 {
				u.inBuf = u.inBuf[len(u.inBuf)-1<<19:]
			}
			u.inMu.Unlock()
			u.postBytes()
		}
		if err != nil {
			if u.hwnd != 0 {
				win.PostMessage(u.hwnd, wmSuzuriClosed, 0, 0)
			}
			return
		}
	}
}

func (u *winUI) postBytes() {
	if u.hwnd == 0 || !u.alive.Load() {
		return
	}
	if u.bytesMsg.CompareAndSwap(false, true) {
		win.PostMessage(u.hwnd, wmSuzuriBytes, 0, 0)
	}
}

func (u *winUI) blinkLoop() {
	t := time.NewTicker(cursorBlinkTick)
	defer t.Stop()
	for range t.C {
		if !u.alive.Load() || u.hwnd == 0 {
			return
		}
		win.PostMessage(u.hwnd, wmSuzuriBlink, 0, 0)
	}
}

// drainAndParse runs ONLY on the UI thread.
func (u *winUI) drainAndParse() {
	u.inMu.Lock()
	data := u.inBuf
	u.inBuf = nil
	u.inMu.Unlock()
	u.bytesMsg.Store(false)

	if len(data) == 0 {
		return
	}
	// Parse VT on UI thread — no cross-thread lock fights with paint.
	_, _ = u.term.Write(data)
	win.InvalidateRect(u.hwnd, nil, false)

	// If more bytes arrived while we parsed, schedule another pass.
	u.inMu.Lock()
	more := len(u.inBuf) > 0
	u.inMu.Unlock()
	if more {
		u.postBytes()
	}
}

func (u *winUI) handle(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriBytes:
		u.drainAndParse()
		return 0

	case wmSuzuriBlink:
		win.InvalidateRect(hwnd, nil, false)
		return 0

	case wmSuzuriClosed:
		// Show a note; do not block.
		_, _ = u.term.Write([]byte("\r\n[suzuri] session ended\r\n"))
		win.InvalidateRect(hwnd, nil, false)
		return 0

	case win.WM_ERASEBKGND:
		// We paint the full frame — skip erase to reduce flicker/extra work.
		return 1

	case win.WM_SIZE:
		u.width = int32(win.LOWORD(uint32(lParam)))
		u.height = int32(win.HIWORD(uint32(lParam)))
		if u.width > 0 && u.height > 0 {
			cols := int(u.width / cellW)
			rows := int(u.height / cellH)
			if cols < 20 {
				cols = 20
			}
			if rows < 5 {
				rows = 5
			}
			if cols != u.cols || rows != u.rows {
				u.cols, u.rows = cols, rows
				u.term.Resize(cols, rows)
				c, r := cols, rows
				go func() { _ = u.sess.Resize(c, r) }()
			}
		}
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
		switch ch {
		case 0x08, 0x09, 0x0a:
			return 0
		case 0x0d:
			u.sendKey([]byte("\r"))
			return 0
		}
		if ch >= 32 {
			var buf [4]byte
			n := utf8Encode(buf[:], ch)
			u.sendKey(buf[:n])
		} else if ch > 0 && ch < 32 {
			u.sendKey([]byte{byte(ch)})
		}
		return 0

	case win.WM_KEYDOWN:
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
			u.sendKey([]byte("\x1b[5~"))
		case win.VK_NEXT:
			u.sendKey([]byte("\x1b[6~"))
		case win.VK_ESCAPE:
			u.sendKey([]byte{0x1b})
		case win.VK_BACK:
			u.sendKey([]byte{0x08})
		case win.VK_TAB:
			u.sendKey([]byte{'\t'})
		}
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
		// Close write queue once.
		select {
		case <-u.writeCh:
		default:
		}
		// Closing may panic if already closed — use sync.Once pattern.
		u.closeWriter()
		go func() { _ = u.sess.Close() }()
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

func (u *winUI) closeWriter() {
	defer func() { _ = recover() }()
	close(u.writeCh)
}

func (u *winUI) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	var rect win.RECT
	win.GetClientRect(hwnd, &rect)

	// Build row strings on UI thread (term is only used here + drainAndParse).
	cols, rows := u.term.Size()
	cur := u.term.Cursor()
	curVis := u.term.CursorVisible()
	rowText := make([]string, rows)
	buf := make([]rune, cols)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			buf[x] = displayRune(u.term.Cell(x, y).Char)
		}
		rowText[y] = string(buf)
	}

	memDC := win.CreateCompatibleDC(hdc)
	if memDC == 0 {
		u.blit(hdc, rect, rowText, cur.X, cur.Y, curVis)
		return
	}
	defer win.DeleteDC(memDC)
	w := rect.Right - rect.Left
	h := rect.Bottom - rect.Top
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	bmp := win.CreateCompatibleBitmap(hdc, w, h)
	if bmp == 0 {
		u.blit(hdc, rect, rowText, cur.X, cur.Y, curVis)
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(bmp))
	old := win.SelectObject(memDC, win.HGDIOBJ(bmp))
	u.blit(memDC, rect, rowText, cur.X, cur.Y, curVis)
	win.BitBlt(hdc, 0, 0, w, h, memDC, 0, 0, win.SRCCOPY)
	win.SelectObject(memDC, old)
}

func (u *winUI) blit(hdc win.HDC, rect win.RECT, rows []string, curX, curY int, curVis bool) {
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.BLACK_BRUSH)))
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, oldFont)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(220, 220, 220))

	for y, line := range rows {
		if line == "" {
			continue
		}
		utf16, err := syscall.UTF16FromString(line)
		if err != nil || len(utf16) < 2 {
			continue
		}
		win.TextOut(hdc, 4, int32(y*cellH+2), &utf16[0], int32(len(utf16)-1))
	}

	if u.cfg.Cursor == config.CursorBlock && curVis {
		if curX < 0 {
			curX = 0
		}
		if curY < 0 {
			curY = 0
		}
		px := int32(4 + curX*cellW)
		py := int32(2 + curY*cellH)
		elapsed := time.Since(u.blinkStart).Seconds()
		period := cursorBlinkPeriod.Seconds()
		alpha := 0.5 + 0.5*math.Sin(2*math.Pi*(elapsed/period))
		level := byte(50 + alpha*205)
		lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(level, level, level)}
		brush := win.CreateBrushIndirect(&lb)
		if brush != 0 {
			oldBr := win.SelectObject(hdc, win.HGDIOBJ(brush))
			win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
			win.Rectangle_(hdc, px, py, px+cellW, py+cellH-2)
			win.SelectObject(hdc, oldBr)
			win.DeleteObject(win.HGDIOBJ(brush))
		}
	}
}

func displayRune(r rune) rune {
	if r == 0 || r == 0xFFFD {
		return ' '
	}
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
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
	var lf win.LOGFONT
	lf.LfHeight = -16
	lf.LfWeight = win.FW_NORMAL
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfOutPrecision = win.OUT_DEFAULT_PRECIS
	lf.LfClipPrecision = win.CLIP_DEFAULT_PRECIS
	lf.LfQuality = win.CLEARTYPE_QUALITY
	lf.LfPitchAndFamily = win.FIXED_PITCH | win.FF_MODERN
	face, _ := syscall.UTF16FromString("Consolas")
	copy(lf.LfFaceName[:], face)
	return win.CreateFontIndirect(&lf)
}

func lastErr(op string) error {
	return windows.Errno(win.GetLastError())
}
