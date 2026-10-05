//go:build windows || darwin

package ui

import "testing"

func TestFrameClientFromWindowKeepsTopAndOppositeEdge(t *testing.T) {
	const border int32 = 7
	window := frameRect{Left: 100, Top: 0, Right: 900, Bottom: 1399}
	work := frameRect{Left: 0, Top: 0, Right: 1920, Bottom: 1399}

	got := frameClientFromWindow(window, work, false, border, border)
	if got.Top != window.Top {
		t.Fatalf("top moved %d → %d (opposite edge must stay put)", window.Top, got.Top)
	}
	if got.Left != window.Left+border || got.Right != window.Right-border {
		t.Fatalf("L/R insets %+v", got)
	}
	if got.Bottom != window.Bottom-border {
		t.Fatalf("bottom %d want %d", got.Bottom, window.Bottom-border)
	}
	if got.height() != window.height()-border {
		t.Fatalf("height %d want window-border %d", got.height(), window.height()-border)
	}

	// Same window rect again — no creep.
	again := frameClientFromWindow(window, work, false, border, border)
	if again != got {
		t.Fatalf("NCCALCSIZE must be idempotent: %+v → %+v", got, again)
	}

	// Drag the bottom edge only: top stays 0, height tracks the window.
	drag := window
	drag.Bottom = 1000
	after := frameClientFromWindow(drag, work, false, border, border)
	if after.Top != 0 {
		t.Fatalf("top shifted on bottom-edge resize: %d", after.Top)
	}
	if after.Bottom != 1000-border {
		t.Fatalf("bottom %d", after.Bottom)
	}

	// Drag the top edge only: bottom stays, top follows the window top.
	up := window
	up.Top = 40
	fromTop := frameClientFromWindow(up, work, false, border, border)
	if fromTop.Bottom != window.Bottom-border {
		t.Fatalf("bottom moved on top-edge resize: %d", fromTop.Bottom)
	}
	if fromTop.Top != 40 {
		t.Fatalf("top %d want 40", fromTop.Top)
	}
}

func TestFrameClientFromWindowZoomedFillsWorkArea(t *testing.T) {
	window := frameRect{Left: -7, Top: -7, Right: 1927, Bottom: 1406}
	work := frameRect{Left: 0, Top: 0, Right: 1920, Bottom: 1399}
	got := frameClientFromWindow(window, work, true, 7, 7)
	if got != work {
		t.Fatalf("zoomed client %+v want work %+v", got, work)
	}
}

func TestFrameResizeBorderUsesPaddedFrame(t *testing.T) {
	x, y := frameResizeBorder(4, 4, 4)
	if x != 8 || y != 8 {
		t.Fatalf("border %d,%d want 8,8", x, y)
	}
	x, y = frameResizeBorder(0, 0, 0)
	if x < 1 || y < 1 {
		t.Fatal("border must stay at least 1px")
	}
}

func TestGlassPresentBitsAreColorKeySafe(t *testing.T) {
	if glassPresentBits != 24 {
		t.Fatalf("backbuffer %d-bit realizes alpha; DWM key needs 24", glassPresentBits)
	}
}
