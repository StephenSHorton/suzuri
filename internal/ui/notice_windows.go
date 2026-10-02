//go:build windows

package ui

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Windows notice stack.
//
// Mac shows the same RGBA cards in a non-activating, transparent panel and
// plays the wav on the main thread. This file is that panel: a layered popup
// (not the terminal HWND), clicks queued for pollNoticeInput, and winmm for
// the sounds. The bitmap is 2×; the window is half of that, matching the
// Mac point size. Click coordinates are scaled back into bitmap pixels so
// hitNotice sees the same space as on Mac.

const (
	noticeScaleDiv = 2

	ulwAlpha       = 0x00000002
	acSrcOver      = 0
	acSrcAlpha     = 1
	wmMouseLeave   = 0x02A3
	tmeLeave       = 0x00000002
	sndAsync       = 0x0001
	sndNoDefault   = 0x0002
	sndMemory      = 0x0004
	sndSync        = 0x0000
	pmRemove       = 0x0001
	swShowNoActive = 4
)

type noticePoint struct{ X, Y int32 }
type noticeSize struct{ CX, CY int32 }
type noticeBlend struct {
	Op, Flags, SrcAlpha, Format byte
}
type trackMouse struct {
	Size      uint32
	Flags     uint32
	HwndTrack win.HWND
	HoverTime uint32
}

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procUpdateLayered = user32.NewProc("UpdateLayeredWindow")
	procTrackMouse    = user32.NewProc("TrackMouseEvent")
	winmm             = windows.NewLazySystemDLL("winmm.dll")
	procPlaySound     = winmm.NewProc("PlaySoundW")

	noticeClassOnce sync.Once
	noticeOwner     uint32
	noticeHwnd      win.HWND
	noticeUp        bool
	noticeTrack     bool
	noticeWinW      int32
	noticeWinH      int32
	noticeBmpW      int32
	noticeBmpH      int32
	noticeLastHash  uint64
	noticeLastX     int32
	noticeLastY     int32

	noticeClick  int32
	noticeClickX int32
	noticeClickY int32
	noticeInside int32

	wavMu   sync.Mutex
	wavRing [4][]byte
	wavSlot int
)

func presentNoticeImage(pix []byte, stride, width, height, anchor int) {
	if width < 1 || height < 1 || len(pix) < stride*height {
		hideNoticePanel()
		return
	}
	if err := ensureNoticeWindow(); err != nil || noticeHwnd == 0 {
		return
	}
	winW := int32(width / noticeScaleDiv)
	winH := int32(height / noticeScaleDiv)
	if winW < 1 {
		winW = 1
	}
	if winH < 1 {
		winH = 1
	}
	left, top, right, bottom := noticeWorkArea()
	x, y := noticeScreenOrigin(left, top, right, bottom, winW, winH, noticeScreenInset, int32(anchor))
	sum := noticePixHash(pix, stride, width, height, anchor, x, y)
	if noticeUp && sum == noticeLastHash && x == noticeLastX && y == noticeLastY &&
		winW == noticeWinW && winH == noticeWinH {
		return
	}
	if !blitNotice(noticeHwnd, pix, stride, width, height, x, y, winW, winH) {
		return
	}
	noticeWinW, noticeWinH = winW, winH
	noticeBmpW, noticeBmpH = int32(width), int32(height)
	noticeLastHash = sum
	noticeLastX, noticeLastY = x, y
	if !noticeUp {
		win.ShowWindow(noticeHwnd, swShowNoActive)
		noticeUp = true
	}
}

func hideNoticePanel() {
	noticeLastHash = 0
	atomic.StoreInt32(&noticeInside, 0)
	if noticeHwnd != 0 && noticeUp {
		win.ShowWindow(noticeHwnd, win.SW_HIDE)
	}
	noticeUp = false
}

func noticePanelUp() bool { return noticeUp }

// noticeScreenOrigin is the panel's top-left in a y-down work area.
// Anchor rows match noticeRow: 0 bottom, 1 center, 2 top.
func noticeScreenOrigin(left, top, right, bottom, panelW, panelH, inset, anchor int32) (x, y int32) {
	visW := right - left
	visH := bottom - top
	switch noticeColumn(int(anchor)) {
	case 1:
		x = left + (visW-panelW)/2
	case 2:
		x = right - panelW - inset
	default:
		x = left + inset
	}
	switch noticeRow(int(anchor)) {
	case 1:
		y = top + (visH-panelH)/2
	case 2:
		y = top + inset
	default:
		y = bottom - panelH - inset
	}
	return x, y
}

func noticeWorkArea() (left, top, right, bottom int32) {
	var host win.HWND
	uiMu.Lock()
	for h := range uiMap {
		if h != noticeHwnd {
			host = h
			break
		}
	}
	uiMu.Unlock()
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	mon := win.MonitorFromWindow(host, win.MONITOR_DEFAULTTONEAREST)
	if mon != 0 && win.GetMonitorInfo(mon, &mi) {
		return mi.RcWork.Left, mi.RcWork.Top, mi.RcWork.Right, mi.RcWork.Bottom
	}
	return 0, 0, win.GetSystemMetrics(win.SM_CXSCREEN), win.GetSystemMetrics(win.SM_CYSCREEN)
}

func noticePixHash(pix []byte, stride, width, height, anchor int, x, y int32) uint64 {
	h := fnv.New64a()
	var hdr [16]byte
	hdr[0] = byte(width)
	hdr[1] = byte(width >> 8)
	hdr[2] = byte(height)
	hdr[3] = byte(height >> 8)
	hdr[4] = byte(anchor)
	hdr[5] = byte(x)
	hdr[6] = byte(y)
	_, _ = h.Write(hdr[:])
	row := width * 4
	for yy := 0; yy < height; yy++ {
		off := yy * stride
		if off+row > len(pix) {
			break
		}
		_, _ = h.Write(pix[off : off+row])
	}
	return h.Sum64()
}

// premulBGRA converts straight RGBA rows into premultiplied BGRA, tightly packed.
func premulBGRA(src []byte, stride, width, height int) []byte {
	dst := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		srow := src[y*stride:]
		drow := dst[y*width*4:]
		for x := 0; x < width; x++ {
			r := uint32(srow[x*4])
			g := uint32(srow[x*4+1])
			b := uint32(srow[x*4+2])
			a := uint32(srow[x*4+3])
			drow[x*4] = byte(b * a / 255)
			drow[x*4+1] = byte(g * a / 255)
			drow[x*4+2] = byte(r * a / 255)
			drow[x*4+3] = byte(a)
		}
	}
	return dst
}

func blitNotice(hwnd win.HWND, pix []byte, stride, width, height int, x, y, winW, winH int32) bool {
	bgra := premulBGRA(pix, stride, width, height)
	hdcScreen := win.GetDC(0)
	if hdcScreen == 0 {
		return false
	}
	defer win.ReleaseDC(0, hdcScreen)
	hdcMem := win.CreateCompatibleDC(hdcScreen)
	if hdcMem == 0 {
		return false
	}
	defer win.DeleteDC(hdcMem)

	bmi := win.BITMAPINFOHEADER{
		BiSize:        uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})),
		BiWidth:       int32(width),
		BiHeight:      -int32(height),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: win.BI_RGB,
	}
	var bits unsafe.Pointer
	dib := win.CreateDIBSection(hdcScreen, &bmi, dibRGBColors, &bits, 0, 0)
	if dib == 0 || bits == nil {
		return false
	}
	defer win.DeleteObject(win.HGDIOBJ(dib))
	dst := unsafe.Slice((*byte)(bits), len(bgra))
	copy(dst, bgra)
	old := win.SelectObject(hdcMem, win.HGDIOBJ(dib))
	defer win.SelectObject(hdcMem, old)

	dstPt := noticePoint{X: x, Y: y}
	sz := noticeSize{CX: winW, CY: winH}
	srcPt := noticePoint{}
	blend := noticeBlend{Op: acSrcOver, SrcAlpha: 255, Format: acSrcAlpha}
	r, _, _ := procUpdateLayered.Call(
		uintptr(hwnd),
		uintptr(hdcScreen),
		uintptr(unsafe.Pointer(&dstPt)),
		uintptr(unsafe.Pointer(&sz)),
		uintptr(hdcMem),
		uintptr(unsafe.Pointer(&srcPt)),
		0,
		uintptr(unsafe.Pointer(&blend)),
		ulwAlpha,
	)
	return r != 0
}

func ensureNoticeWindow() error {
	ensureNoticeClass()
	if noticeHwnd != 0 {
		return nil
	}
	hinst := win.GetModuleHandle(nil)
	name, _ := syscall.UTF16PtrFromString("suzuri_notice")
	title, _ := syscall.UTF16PtrFromString("suzuri")
	hwnd := win.CreateWindowEx(
		win.WS_EX_LAYERED|win.WS_EX_TOPMOST|win.WS_EX_TOOLWINDOW|win.WS_EX_NOACTIVATE,
		name,
		title,
		win.WS_POPUP,
		0, 0, 0, 0,
		0, 0, hinst, nil,
	)
	if hwnd == 0 {
		return lastErr("CreateWindowEx notice")
	}
	noticeHwnd = hwnd
	noticeOwner = windows.GetCurrentThreadId()
	return nil
}

func ensureNoticeClass() {
	noticeClassOnce.Do(func() {
		hinst := win.GetModuleHandle(nil)
		name, _ := syscall.UTF16PtrFromString("suzuri_notice")
		wc := win.WNDCLASSEX{
			CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
			LpfnWndProc:   syscall.NewCallback(noticeWndProc),
			HInstance:     hinst,
			LpszClassName: name,
			HCursor:       win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)),
		}
		if atom := win.RegisterClassEx(&wc); atom == 0 {
			if errno := windows.GetLastError(); errno != windows.ERROR_CLASS_ALREADY_EXISTS {
				return
			}
		}
	})
}

func noticeWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_MOUSEMOVE:
		atomic.StoreInt32(&noticeInside, 1)
		if !noticeTrack {
			ev := trackMouse{Flags: tmeLeave, HwndTrack: hwnd}
			// C sizeof, not Go's trailing pad (20 on amd64, 16 on 386).
			ev.Size = uint32(unsafe.Offsetof(ev.HoverTime) + unsafe.Sizeof(ev.HoverTime))
			if r, _, _ := procTrackMouse.Call(uintptr(unsafe.Pointer(&ev))); r != 0 {
				noticeTrack = true
			}
		}
		return 0
	case wmMouseLeave:
		noticeTrack = false
		atomic.StoreInt32(&noticeInside, 0)
		return 0
	case win.WM_LBUTTONDOWN, win.WM_RBUTTONDOWN:
		kind := int32(1)
		if msg == win.WM_RBUTTONDOWN {
			kind = 3
		}
		x := int32(int16(lParam & 0xffff))
		y := int32(int16((lParam >> 16) & 0xffff))
		bx, by := noticeBitmapPoint(x, y, noticeWinW, noticeWinH, noticeBmpW, noticeBmpH)
		atomic.StoreInt32(&noticeClickX, bx)
		atomic.StoreInt32(&noticeClickY, by)
		atomic.StoreInt32(&noticeClick, kind)
		atomic.StoreInt32(&noticeInside, 1)
		return 0
	case win.WM_SETCURSOR:
		win.SetCursor(win.LoadCursor(0, win.MAKEINTRESOURCE(win.IDC_ARROW)))
		return 1
	}
	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

// noticeBitmapPoint maps a client pixel in the half-size window into the 2× bitmap.
func noticeBitmapPoint(clientX, clientY, winW, winH, bmpW, bmpH int32) (int32, int32) {
	if winW < 1 {
		winW = 1
	}
	if winH < 1 {
		winH = 1
	}
	if bmpW < 1 {
		bmpW = winW * noticeScaleDiv
	}
	if bmpH < 1 {
		bmpH = winH * noticeScaleDiv
	}
	return clientX * bmpW / winW, clientY * bmpH / winH
}

func playWAV(b []byte) {
	if len(b) < 44 {
		return
	}
	hold := append([]byte(nil), b...)
	wavMu.Lock()
	wavRing[wavSlot] = hold
	wavSlot = (wavSlot + 1) % len(wavRing)
	wavMu.Unlock()
	_, _, _ = procPlaySound.Call(
		uintptr(unsafe.Pointer(&hold[0])),
		0,
		sndAsync|sndMemory|sndNoDefault,
	)
}

func playWAVSync(b []byte) {
	if len(b) < 44 {
		return
	}
	hold := append([]byte(nil), b...)
	_, _, _ = procPlaySound.Call(
		uintptr(unsafe.Pointer(&hold[0])),
		0,
		sndSync|sndMemory|sndNoDefault,
	)
}

func focusNoticeHost() {
	var hwnd win.HWND
	uiMu.Lock()
	for h := range uiMap {
		if h != noticeHwnd {
			hwnd = h
			break
		}
	}
	uiMu.Unlock()
	if hwnd == 0 {
		return
	}
	if win.IsIconic(hwnd) {
		win.ShowWindow(hwnd, win.SW_RESTORE)
	}
	win.SetForegroundWindow(hwnd)
}

func initNoticeApp() {
	ensureNoticeClass()
	if noticeOwner == 0 {
		noticeOwner = windows.GetCurrentThreadId()
	}
}

func onMainThread() bool {
	if noticeOwner == 0 {
		return true
	}
	return windows.GetCurrentThreadId() == noticeOwner
}

func pumpUI(d time.Duration) {
	if d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var msg win.MSG
		if win.PeekMessage(&msg, 0, 0, 0, pmRemove) {
			win.TranslateMessage(&msg)
			win.DispatchMessage(&msg)
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	pollNoticeInput()
}

func pollNoticeInput() {
	kind := atomic.SwapInt32(&noticeClick, 0)
	x := atomic.LoadInt32(&noticeClickX)
	y := atomic.LoadInt32(&noticeClickY)
	noticeMu.Lock()
	noticeHover = atomic.LoadInt32(&noticeInside) != 0
	noticeMu.Unlock()
	switch int(kind) {
	case 1, 3:
		onNoticeMouse(int(x), int(y), int(kind))
	}
}
