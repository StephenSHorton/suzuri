//go:build windows

package ui

import "testing"

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
