//go:build windows

package ui

import (
	"testing"

	"github.com/lxn/win"
)

func TestHideDWMCaptionButtonsClearsRects(t *testing.T) {
	var info titleBarInfoEx
	info.Rgrect[2] = win.RECT{Left: 10, Right: 20}
	info.Rgrect[5] = win.RECT{Left: 80, Right: 100}
	hideDWMCaptionButtons(&info)
	if info.Rgrect[2] != (win.RECT{}) || info.Rgrect[5] != (win.RECT{}) {
		t.Fatalf("DWM button rects still set: %+v", info.Rgrect)
	}
	if info.Rgstate[2]&stateSystemInvisible == 0 || info.Rgstate[5]&stateSystemInvisible == 0 {
		t.Fatal("DWM buttons must be marked invisible")
	}
}

func TestFrameChromeStyleHidesDWMButtons(t *testing.T) {
	if styleSysmenu != uint32(win.WS_SYSMENU) ||
		styleMinimizeBox != uint32(win.WS_MINIMIZEBOX) ||
		styleMaximizeBox != uint32(win.WS_MAXIMIZEBOX) ||
		styleCaption != uint32(win.WS_CAPTION) ||
		styleThickframe != uint32(win.WS_THICKFRAME) {
		t.Fatal("style constants drifted from Win32")
	}
	got := frameChromeStyle(uint32(win.WS_OVERLAPPEDWINDOW))
	if got&styleCaptionBtns != 0 {
		t.Fatalf("caption-button bits still set %#x", got)
	}
	if got&uint32(win.WS_CAPTION) == 0 || got&uint32(win.WS_THICKFRAME) == 0 {
		t.Fatalf("need caption+thickframe %#x", got)
	}
}

func TestWAClickActiveMatchesWin32(t *testing.T) {
	if waClickActive != uint32(win.WA_CLICKACTIVE) ||
		waActive != uint32(win.WA_ACTIVE) ||
		waInactive != uint32(win.WA_INACTIVE) {
		t.Fatalf("activate constants drifted: %#x %#x %#x", waInactive, waActive, waClickActive)
	}
}

func TestWindowCornersAreSquare(t *testing.T) {
	if dwmwcpDoNotRound != 1 {
		t.Fatalf("DWMWCP_DONOTROUND is 1, got %d", dwmwcpDoNotRound)
	}
	if dwmwcpRound == windowCornerPreference() {
		t.Fatal("chrome must not request DWMWCP_ROUND")
	}
	if windowCornerPreference() != dwmwcpDoNotRound {
		t.Fatalf("corner pref %d want DONOTROUND", windowCornerPreference())
	}
}
