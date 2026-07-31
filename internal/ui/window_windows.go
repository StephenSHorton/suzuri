//go:build windows

package ui

import (
	"io"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/host"
	"github.com/StephenSHorton/suzuri/internal/vt"
)

const (
	className = "SuzuriTerminalClass"
	appTitle  = "suzuri（硯）"
)

// Run opens a native Win32 window and drives the ConPTY session until closed.
func Run(sess *host.Session) error {
	ui := &winUI{
		sess: sess,
		// rough cell metrics for default window
		cols: 100,
		rows: 30,
		cfg:  config.Default(), // CursorBlock by default
	}
	return ui.loop()
}

type winUI struct {
	sess *host.Session

	hwnd   win.HWND
	font   win.HFONT
	width  int32
	height int32
	cols   int
	rows   int

	mu     sync.Mutex
	lines  []string // scrollback lines (plain text after strip)
	cfg    config.Config
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
		// class may already exist from a previous run in-process
		if errno := windows.GetLastError(); errno != windows.ERROR_CLASS_ALREADY_EXISTS {
			return lastErr("RegisterClassEx")
		}
	}

	// ~10×20 px cells as a starting guess
	cw, ch := int32(u.cols*9+32), int32(u.rows*18+48)
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
	win.ShowWindow(hwnd, win.SW_SHOW)
	win.UpdateWindow(hwnd)

	// PTY → UI
	go u.readLoop()

	var msg win.MSG
	for {
		ret := win.GetMessage(&msg, 0, 0, 0)
		if ret == 0 {
			// WM_QUIT
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
			text := string(vt.StripCSI(buf[:n]))
			if text != "" {
				u.append(text)
			}
		}
		if err != nil {
			if err != io.EOF {
				u.append("\n[suzuri] session ended\n")
			}
			return
		}
	}
}

func (u *winUI) append(s string) {
	u.mu.Lock()
	// Normalize to lines; keep last N
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.Split(s, "\n")
	if len(u.lines) == 0 {
		u.lines = []string{""}
	}
	u.lines[len(u.lines)-1] += parts[0]
	for i := 1; i < len(parts); i++ {
		u.lines = append(u.lines, parts[i])
	}
	const maxLines = 5000
	if len(u.lines) > maxLines {
		u.lines = u.lines[len(u.lines)-maxLines:]
	}
	u.mu.Unlock()
	if u.hwnd != 0 {
		win.InvalidateRect(u.hwnd, nil, true)
	}
}

func (u *winUI) wndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_SIZE:
		u.width = int32(win.LOWORD(uint32(lParam)))
		u.height = int32(win.HIWORD(uint32(lParam)))
		// Approx cols/rows from pixel size
		if u.width > 0 && u.height > 0 {
			cols := int(u.width / 9)
			rows := int(u.height / 18)
			if cols < 20 {
				cols = 20
			}
			if rows < 5 {
				rows = 5
			}
			u.cols, u.rows = cols, rows
			_ = u.sess.Resize(cols, rows)
		}
		return 0

	case win.WM_CHAR:
		// Printable / control chars. Backspace is handled only on WM_KEYDOWN
		// (VK_BACK) so we don't double-send when TranslateMessage also emits
		// WM_CHAR 0x08.
		ch := rune(wParam)
		if ch == 0x08 {
			return 0
		}
		if ch == 0x0d { // CR
			_, _ = u.sess.Write([]byte("\r"))
			return 0
		}
		if ch == 0x0a {
			return 0
		}
		if ch >= 32 {
			_, _ = u.sess.Write([]byte(string(ch)))
		} else if ch == 0x03 { // Ctrl+C
			_, _ = u.sess.Write([]byte{0x03})
		} else if ch == 0x09 {
			_, _ = u.sess.Write([]byte{'\t'})
		} else if ch < 32 {
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
		case win.VK_PRIOR: // page up
			_, _ = u.sess.Write([]byte("\x1b[5~"))
		case win.VK_NEXT:
			_, _ = u.sess.Write([]byte("\x1b[6~"))
		case win.VK_ESCAPE:
			_, _ = u.sess.Write([]byte{0x1b})
		case win.VK_BACK:
			// ConPTY + Windows shells expect BS (0x08), not DEL (0x7f).
			_, _ = u.sess.Write([]byte{0x08})
		}
		return 0

	case win.WM_PAINT:
		u.paint(hwnd)
		return 0

	case win.WM_DESTROY:
		_ = u.sess.Close()
		if u.font != 0 {
			win.DeleteObject(win.HGDIOBJ(u.font))
		}
		win.PostQuitMessage(0)
		return 0
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func (u *winUI) paint(hwnd win.HWND) {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	defer win.EndPaint(hwnd, &ps)

	// Background
	var rect win.RECT
	win.GetClientRect(hwnd, &rect)
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.BLACK_BRUSH)))
	win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
	win.Rectangle_(hdc, rect.Left, rect.Top, rect.Right, rect.Bottom)

	old := win.SelectObject(hdc, win.HGDIOBJ(u.font))
	defer win.SelectObject(hdc, old)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(220, 220, 220))

	u.mu.Lock()
	lines := append([]string(nil), u.lines...)
	u.mu.Unlock()

	// Draw last N lines that fit
	lineH := int32(18)
	maxRows := int(rect.Bottom / lineH)
	if maxRows < 1 {
		maxRows = 1
	}
	start := 0
	if len(lines) > maxRows {
		start = len(lines) - maxRows
	}
	y := int32(4)
	var lastLine string
	var lastY int32
	for _, line := range lines[start:] {
		// Expand tabs simply
		line = strings.ReplaceAll(line, "\t", "    ")
		lastLine = line
		lastY = y
		utf16, err := syscall.UTF16FromString(line)
		if err != nil {
			continue
		}
		win.TextOut(hdc, 8, y, &utf16[0], int32(len(utf16)-1))
		y += lineH
		if y > rect.Bottom {
			break
		}
	}

	// Provisional caret: block at end of last visible line until the VT
	// grid tracks a real cursor position. Default style is CursorBlock.
	if u.cfg.Cursor == config.CursorBlock {
		cellW := int32(9)
		// Approximate monospace advance (Consolas ~0.5em of height).
		col := int32(len([]rune(lastLine)))
		cx := int32(8) + col*cellW
		cy := lastY
		if cy == 0 {
			cy = 4
		}
		win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.WHITE_BRUSH)))
		win.SelectObject(hdc, win.HGDIOBJ(win.GetStockObject(win.NULL_PEN)))
		win.Rectangle_(hdc, cx, cy, cx+cellW, cy+lineH-2)
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
