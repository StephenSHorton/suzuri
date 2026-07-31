//go:build windows

package ui

import (
	"io"
	"math"
	"sync"
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

	wmSuzuriOutput = win.WM_APP + 1
	wmSuzuriBlink  = win.WM_APP + 2

	cursorBlinkPeriod = 1200 * time.Millisecond
	cursorBlinkTick   = 33 * time.Millisecond

	cellW = 9
	cellH = 18
)

// Run opens a native Win32 window and drives the ConPTY session until closed.
func Run(sess *host.Session) error {
	cols, rows := 100, 30
	ui := &winUI{
		sess: sess,
		cols: cols,
		rows: rows,
		cfg:  config.Default(),
		term: vt10x.New(vt10x.WithSize(cols, rows)),
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

	// Serialize term writes (readLoop) vs paint/resize (UI thread).
	// vt10x has Lock/Unlock for reads; Write also locks internally.
	// We only paint under term.Lock.

	blinkStart time.Time
	destroyed  sync.Once
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
	u.blinkStart = time.Now()
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)

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

func (u *winUI) readLoop() {
	buf := make([]byte, 8192)
	for {
		n, err := u.sess.Read(buf)
		if n > 0 {
			// vt10x interprets CSI/OSC/UTF-8 and maintains a cell grid.
			// This replaces the broken "strip + append lines" approach that
			// duplicated text whenever the shell redrew the line.
			_, _ = u.term.Write(buf[:n])
			if u.hwnd != 0 {
				win.PostMessage(u.hwnd, wmSuzuriOutput, 0, 0)
			}
		}
		if err != nil {
			if err != io.EOF && u.hwnd != 0 {
				_, _ = u.term.Write([]byte("\r\n[suzuri] session ended\r\n"))
				win.PostMessage(u.hwnd, wmSuzuriOutput, 0, 0)
			}
			return
		}
	}
}

func (u *winUI) blinkLoop() {
	t := time.NewTicker(cursorBlinkTick)
	defer t.Stop()
	for range t.C {
		if u.hwnd == 0 {
			return
		}
		win.PostMessage(u.hwnd, wmSuzuriBlink, 0, 0)
	}
}

func (u *winUI) wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSuzuriOutput, wmSuzuriBlink:
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
				_ = u.sess.Resize(cols, rows)
			}
		}
		return 0

	case win.WM_CHAR:
		ch := rune(wParam)
		// Backspace arrives as WM_CHAR 0x08 *and* WM_KEYDOWN VK_BACK.
		// Handle only on KEYDOWN to avoid double-send.
		// Keys handled exclusively on WM_KEYDOWN — ignore their WM_CHAR twins.
		switch ch {
		case 0x08, 0x09, 0x0a: // BS, TAB, LF
			return 0
		case 0x0d: // Enter
			_, _ = u.sess.Write([]byte("\r"))
			return 0
		}
		if ch >= 32 {
			var buf [4]byte
			n := utf8Encode(buf[:], ch)
			_, _ = u.sess.Write(buf[:n])
		} else if ch > 0 && ch < 32 {
			_, _ = u.sess.Write([]byte{byte(ch)})
		}
		return 0

	case win.WM_KEYDOWN:
		switch wParam {
		case win.VK_UP:
			_, _ = u.sess.Write([]byte("\x1b[A"))
		case win.VK_DOWN:
			_, _ = u.sess.Write([]byte("\x1b[B"))
		case win.VK_RIGHT:
			_, _ = u.sess.Write([]byte("\x1b[C"))
		case win.VK_LEFT:
			_, _ = u.sess.Write([]byte("\x1b[D"))
		case win.VK_DELETE:
			_, _ = u.sess.Write([]byte("\x1b[3~"))
		case win.VK_HOME:
			_, _ = u.sess.Write([]byte("\x1b[H"))
		case win.VK_END:
			_, _ = u.sess.Write([]byte("\x1b[F"))
		case win.VK_PRIOR:
			_, _ = u.sess.Write([]byte("\x1b[5~"))
		case win.VK_NEXT:
			_, _ = u.sess.Write([]byte("\x1b[6~"))
		case win.VK_ESCAPE:
			_, _ = u.sess.Write([]byte{0x1b})
		case win.VK_BACK:
			// Windows ConPTY: BS. (DEL is Forward-delete / different.)
			_, _ = u.sess.Write([]byte{0x08})
		case win.VK_TAB:
			// Prefer KEYDOWN for Tab; ignore duplicate WM_CHAR if any.
			_, _ = u.sess.Write([]byte{'\t'})
		case win.VK_SPACE:
			// Space is normally WM_CHAR; do not also write here or it doubles.
		}
		return 0

	case win.WM_PAINT:
		u.paint(hwnd)
		return 0

	case win.WM_DESTROY:
		u.destroyed.Do(func() {
			_ = u.sess.Close()
			if u.font != 0 {
				win.DeleteObject(win.HGDIOBJ(u.font))
			}
			u.hwnd = 0
		})
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
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.BLACK_BRUSH)))
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)

	old := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, old)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(220, 220, 220))

	u.term.Lock()
	cols, rows := u.term.Size()
	cur := u.term.Cursor()
	curVis := u.term.CursorVisible()

	// Build each row as a string of displayable runes (spaces for empty).
	for y := 0; y < rows; y++ {
		// row string — use runes to avoid invalid UTF-16 later
		runes := make([]rune, cols)
		for x := 0; x < cols; x++ {
			g := u.term.Cell(x, y)
			runes[x] = displayRune(g.Char)
		}
		// trim trailing spaces for slightly cheaper TextOut (optional full width)
		line := string(runes)
		if line != "" {
			utf16, err := syscall.UTF16FromString(line)
			if err == nil && len(utf16) > 1 {
				win.TextOut(hdc, 4, int32(y*cellH+2), &utf16[0], int32(len(utf16)-1))
			}
		}
	}

	cx, cy := cur.X, cur.Y
	u.term.Unlock()

	// Block cursor with opacity pulse
	if u.cfg.Cursor == config.CursorBlock && curVis {
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

// displayRune maps terminal cell contents to something TextOut can show.
// Empty cells and non-printables become a space — never U+FFFD tofu.
func displayRune(r rune) rune {
	if r == 0 || r == utf8RuneError {
		return ' '
	}
	if r == 0xFFFD {
		return ' '
	}
	// Skip other C0/C1 controls that sometimes leak into the grid.
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return ' '
	}
	if !unicode.IsPrint(r) && r != ' ' {
		return ' '
	}
	return r
}

const utf8RuneError = '\uFFFD'

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
