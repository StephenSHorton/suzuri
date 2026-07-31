//go:build windows

package ui

import (
	"io"
	"math"
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

	wmSuzuriRedraw = win.WM_APP + 1

	cursorBlinkPeriod = 1200 * time.Millisecond
	// Blink ticks are infrequent — full-frame invalidates every 33ms starved the message loop.
	cursorBlinkTick = 100 * time.Millisecond

	cellW = 9
	cellH = 18
)

// Run opens a native Win32 window and drives the ConPTY session until closed.
func Run(sess *host.Session) error {
	cols, rows := 100, 30
	ui := &winUI{
		sess:      sess,
		cols:      cols,
		rows:      rows,
		cfg:       config.Default(),
		term:      vt10x.New(vt10x.WithSize(cols, rows)),
		writeCh:   make(chan []byte, 256),
		blinkStart: time.Now(),
	}
	return ui.loop()
}

type winUI struct {
	sess *host.Session
	term vt10x.Terminal

	hwnd   win.HWND
	font   win.HFONT
	width  int32
	height int32
	cols   int
	rows   int
	cfg    config.Config

	// writeCh decouples keyboard handling from ConPTY writes so a full pipe
	// never freezes the Win32 message loop ("Not Responding").
	writeCh chan []byte

	blinkStart time.Time
	// redrawPending coalesces PostMessage spam from the PTY reader.
	redrawPending atomic.Bool
	alive         atomic.Bool
}

func (u *winUI) loop() error {
	hInst := win.GetModuleHandle(nil)
	if hInst == 0 {
		return lastErr("GetModuleHandle")
	}

	cname, _ := syscall.UTF16PtrFromString(className)
	title, _ := syscall.UTF16PtrFromString(appTitle)

	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		LpfnWndProc:   syscall.NewCallback(u.wndProc),
		HInstance:     hInst,
		LpszClassName: cname,
		HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_IBEAM)),
		HbrBackground: win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)),
		Style:         win.CS_DBLCLKS,
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
	u.alive.Store(true)
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

func (u *winUI) writeLoop() {
	for b := range u.writeCh {
		if len(b) == 0 {
			continue
		}
		if _, err := u.sess.Write(b); err != nil {
			// Session gone — stop accepting.
			return
		}
	}
}

// sendKey never blocks the UI thread for more than a brief channel send.
func (u *winUI) sendKey(b []byte) {
	if !u.alive.Load() || len(b) == 0 {
		return
	}
	// Copy — callers often pass stack-backed slices.
	p := append([]byte(nil), b...)
	select {
	case u.writeCh <- p:
	default:
		// Drop if flooded; better than freezing the window.
	}
}

func (u *winUI) requestRedraw() {
	if !u.alive.Load() || u.hwnd == 0 {
		return
	}
	// Coalesce: at most one outstanding redraw message.
	if u.redrawPending.CompareAndSwap(false, true) {
		win.PostMessage(u.hwnd, wmSuzuriRedraw, 0, 0)
	}
}

func (u *winUI) readLoop() {
	buf := make([]byte, 8192)
	for {
		n, err := u.sess.Read(buf)
		if n > 0 {
			_, _ = u.term.Write(buf[:n])
			u.requestRedraw()
		}
		if err != nil {
			if err != io.EOF {
				_, _ = u.term.Write([]byte("\r\n[suzuri] session ended\r\n"))
				u.requestRedraw()
			}
			return
		}
	}
}

func (u *winUI) blinkLoop() {
	t := time.NewTicker(cursorBlinkTick)
	defer t.Stop()
	for range t.C {
		if !u.alive.Load() {
			return
		}
		u.requestRedraw()
	}
}

func (u *winUI) wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriRedraw:
		u.redrawPending.Store(false)
		win.InvalidateRect(hwnd, nil, false)
		return 0

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
				// Resize PTY off the UI thread — ConPTY can block.
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

	case win.WM_DESTROY:
		u.alive.Store(false)
		// Unblock writeLoop
		close(u.writeCh)
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

// rowSnapshot is a paint-friendly copy of one terminal row.
type rowSnapshot struct {
	text string
}

type frameSnapshot struct {
	rows   []rowSnapshot
	curX   int
	curY   int
	curVis bool
}

func (u *winUI) snapshot() frameSnapshot {
	u.term.Lock()
	defer u.term.Unlock()

	cols, rows := u.term.Size()
	cur := u.term.Cursor()
	vis := u.term.CursorVisible()
	out := make([]rowSnapshot, rows)
	buf := make([]rune, cols)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			buf[x] = displayRune(u.term.Cell(x, y).Char)
		}
		out[y] = rowSnapshot{text: string(buf)}
	}
	return frameSnapshot{
		rows:   out,
		curX:   cur.X,
		curY:   cur.Y,
		curVis: vis,
	}
}

func (u *winUI) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	// Snapshot under lock only — never call ConPTY or do heavy work while locked.
	frame := u.snapshot()

	var rect win.RECT
	win.GetClientRect(hwnd, &rect)

	// Double-buffer to avoid flicker and reduce time with a half-drawn frame.
	memDC := win.CreateCompatibleDC(hdc)
	if memDC == 0 {
		u.paintFrame(hdc, rect, frame)
		return
	}
	defer win.DeleteDC(memDC)
	bmp := win.CreateCompatibleBitmap(hdc, rect.Right-rect.Left, rect.Bottom-rect.Top)
	if bmp == 0 {
		u.paintFrame(hdc, rect, frame)
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(bmp))
	oldBmp := win.SelectObject(memDC, win.HGDIOBJ(bmp))
	defer win.SelectObject(memDC, oldBmp)

	u.paintFrame(memDC, rect, frame)
	win.BitBlt(hdc, 0, 0, rect.Right-rect.Left, rect.Bottom-rect.Top, memDC, 0, 0, win.SRCCOPY)
}

func (u *winUI) paintFrame(hdc win.HDC, rect win.RECT, frame frameSnapshot) {
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.BLACK_BRUSH)))
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)

	old := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, old)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(220, 220, 220))

	for y, row := range frame.rows {
		if row.text == "" {
			continue
		}
		utf16, err := syscall.UTF16FromString(row.text)
		if err != nil || len(utf16) < 2 {
			continue
		}
		win.TextOut(hdc, 4, int32(y*cellH+2), &utf16[0], int32(len(utf16)-1))
	}

	if u.cfg.Cursor == config.CursorBlock && frame.curVis {
		cx, cy := frame.curX, frame.curY
		if cx < 0 {
			cx = 0
		}
		if cy < 0 {
			cy = 0
		}
		px := int32(4 + cx*cellW)
		py := int32(2 + cy*cellH)

		elapsed := time.Since(u.blinkStart).Seconds()
		period := cursorBlinkPeriod.Seconds()
		phase := 2 * math.Pi * (elapsed / period)
		alpha := 0.5 + 0.5*math.Sin(phase)
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
