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
