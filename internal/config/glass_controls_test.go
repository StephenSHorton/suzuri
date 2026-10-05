package config

import "testing"

func TestGlassBlurControlLiveByHost(t *testing.T) {
	glass := Normalize(Config{Backdrop: BackdropGlass, GlassBlur: 36, GlassVeil: 0})
	accent := glass
	accent.GlassVeil = 40
	solid := Normalize(Config{})

	mac := GlassHost{GOOS: "darwin", WinBuild: 0}
	if !glassBlurControlLive(glass, mac) || !glassBlurControlLive(solid, mac) {
		t.Fatal("macOS blur must stay live")
	}
	win10 := GlassHost{GOOS: "windows", WinBuild: 19041}
	if !glassBlurControlLive(glass, win10) {
		t.Fatal("Win10 accent-acrylic blur is live")
	}
	win11 := GlassHost{GOOS: "windows", WinBuild: 26100}
	if glassBlurControlLive(glass, win11) {
		t.Fatal("Win11 HostBackdrop (veil 0) blur is a no-op")
	}
	if !glassBlurControlLive(accent, win11) {
		t.Fatal("Win11 veil>0 uses accent-acrylic; blur is live")
	}
	if !glassBlurControlLive(solid, win11) {
		t.Fatal("solid backdrop may pre-tune blur")
	}
	if WindowsGlassUsesAccent(glass, 26100) {
		t.Fatal("veil 0 on 26100 is HostBackdrop, not accent")
	}
	if !WindowsGlassUsesAccent(accent, 26100) {
		t.Fatal("veil 40 is accent")
	}
}

func TestGlassRimControlLiveByHost(t *testing.T) {
	if !glassRimControlLive(GlassHost{GOOS: "darwin"}) {
		t.Fatal("rim paints on macOS")
	}
	if glassRimControlLive(GlassHost{GOOS: "windows", WinBuild: 26100}) {
		t.Fatal("rim is a no-op on Windows GDI")
	}
	if glassRimControlLive(GlassHost{GOOS: "linux"}) {
		t.Fatal("rim is macOS-only")
	}
}
