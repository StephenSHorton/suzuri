//go:build windows || darwin

package ui

import (
	"time"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// hitFrameButton returns 0 close, 1 minimize, 2 zoom, or -1.
// Visual order matches the strip: mac close/min/zoom, windows min/zoom/close.
func hitFrameButton(m chrome.Model, cellX int) int {
	if cellX < 0 {
		return -1
	}
	b := m.FrameButtonBounds()
	for i, span := range b {
		if span[1] > span[0] && cellX >= span[0] && cellX < span[1] {
			return i
		}
	}
	return -1
}

func frameActionForHit(frame chrome.Frame, hit int) chrome.FrameButton {
	if frame == chrome.FrameWindows {
		switch hit {
		case 0:
			return chrome.FrameMinimize
		case 1:
			return chrome.FrameZoom
		default:
			return chrome.FrameClose
		}
	}
	return chrome.FrameButton(hit)
}

type frameDrag struct {
	on      bool
	startSX int
	startSY int
	startWX int
	startWY int
	lastHit time.Time
	lastX   int
}

// Win32 WM_NCHITTEST codes. Local so hit tests do not need an HWND.
const (
	hitNowhere       = 0
	hitClient        = 1
	hitCaption       = 2
	hitLeft          = 10
	hitRight         = 11
	hitTop           = 12
	hitTopLeft       = 13
	hitTopRight      = 14
	hitBottom        = 15
	hitBottomLeft    = 16
	hitBottomRight   = 17
	hitMinButton     = 8
	hitMaxButton     = 9
	hitClose         = 20
	frameResizePx    = 6
	captionButtonDIP = 46
	captionButtonW   = captionButtonDIP // 96-DPI width
)

// titleStripHeightPx is the Mac rule: one text row, 50% taller than a shell cell.
func titleStripHeightPx(ch int32) int32 {
	if ch < 1 {
		ch = cellH
	}
	return ch + ch/2
}

// pixRect is a half-open client rectangle [L,R) × [T,B).
type pixRect struct {
	L, T, R, B int32
}

func (r pixRect) contains(x, y int32) bool {
	return x >= r.L && x < r.R && y >= r.T && y < r.B
}

func (r pixRect) empty() bool { return r.R <= r.L || r.B <= r.T }

// winCaptionButtons is minimize, zoom/restore, close — flush to the right edge,
// each captionButtonW wide and the full title-strip tall.
func winCaptionButtons(clientW, stripH int32) [3]pixRect {
	return winCaptionButtonsDPI(clientW, stripH, 96)
}

// titleHitQuery is a client-space hit. Buttons win over the resize band.
// Controls are tabs, +, bell, and the cup — not the brand mark.
type titleHitQuery struct {
	X, Y     int32
	ClientW  int32
	ClientH  int32
	StripH   int32
	Buttons  [3]pixRect
	Controls []pixRect
}

// hitTestTitleBar returns a Win32 HT code, or hitNowhere for the shell interior
// (the caller lets DefWindowProc answer that).
func hitTestTitleBar(q titleHitQuery) int {
	if q.ClientW < 1 || q.ClientH < 1 {
		return hitNowhere
	}
	for i, b := range q.Buttons {
		if b.contains(q.X, q.Y) {
			switch i {
			case 0:
				return hitMinButton
			case 1:
				return hitMaxButton
			default:
				return hitClose
			}
		}
	}
	const border = frameResizePx
	left := q.X < border
	right := q.X >= q.ClientW-border
	top := q.Y < border
	bottom := q.Y >= q.ClientH-border
	switch {
	case top && left:
		return hitTopLeft
	case top && right:
		return hitTopRight
	case bottom && left:
		return hitBottomLeft
	case bottom && right:
		return hitBottomRight
	case top:
		return hitTop
	case bottom:
		return hitBottom
	case left:
		return hitLeft
	case right:
		return hitRight
	}
	if q.StripH > 0 && q.Y >= 0 && q.Y < q.StripH {
		for _, c := range q.Controls {
			if c.contains(q.X, q.Y) {
				return hitClient
			}
		}
		return hitCaption
	}
	return hitNowhere
}

// presentCaptionHit is the HT code WM_NCHITTEST actually returns.
// DWM paints native min/max/close in any rect it believes is a caption
// button (DwmDefWindowProc hits, or cyTopHeight=-1). Maximize stays
// HTMAXBUTTON so Win11 Snap Layouts still appear; min/close are HTCLIENT
// and WM_LBUTTONDOWN owns the click. Never feed those through
// DwmDefWindowProc.
func presentCaptionHit(hit int) int {
	switch hit {
	case hitMinButton, hitClose:
		return hitClient
	default:
		return hit
	}
}
