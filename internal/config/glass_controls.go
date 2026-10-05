package config

import (
	"runtime"
	"sync"
	"testing"
)

// GlassHost is the OS the settings glass sliders should assume.
type GlassHost struct {
	GOOS     string
	WinBuild uint32
}

var (
	glassHostMu        sync.Mutex
	glassHostOverride  *GlassHost
	windowsBuildCached uint32
	windowsBuildOnce   sync.Once
)

// CurrentGlassHost is this process (runtime GOOS + Windows build).
func CurrentGlassHost() GlassHost {
	return GlassHost{GOOS: runtime.GOOS, WinBuild: windowsBuildNumberCached()}
}

func liveGlassHost() GlassHost {
	glassHostMu.Lock()
	defer glassHostMu.Unlock()
	if glassHostOverride != nil {
		return *glassHostOverride
	}
	return CurrentGlassHost()
}

// TestingSetGlassHost pins the host for settings-control tests. Callers
// must pass t so the override is cleared.
func TestingSetGlassHost(t testing.TB, h GlassHost) {
	t.Helper()
	glassHostMu.Lock()
	prev := glassHostOverride
	cp := h
	glassHostOverride = &cp
	glassHostMu.Unlock()
	t.Cleanup(func() {
		glassHostMu.Lock()
		glassHostOverride = prev
		glassHostMu.Unlock()
	})
}

func windowsBuildNumberCached() uint32 {
	windowsBuildOnce.Do(func() {
		windowsBuildCached = probeWindowsBuildNumber()
	})
	return windowsBuildCached
}

// GlassBlurControlLive is true when the Blur slider actually changes frost.
// macOS always. Windows: only the accent-acrylic path (Win10, or Win11
// with veil in 1–99). Win11 HostBackdrop (glass + veil 0) is a no-op.
func GlassBlurControlLive(c Config) bool {
	return glassBlurControlLive(c, liveGlassHost())
}

func glassBlurControlLive(c Config, host GlassHost) bool {
	if host.GOOS != "windows" {
		return true
	}
	if c.Backdrop != BackdropGlass {
		return true // pre-tune before switching to glass
	}
	return WindowsGlassUsesAccent(c, host.WinBuild)
}

// GlassRimControlLive is true when Rim paints (Mac software outline only).
func GlassRimControlLive() bool {
	return glassRimControlLive(liveGlassHost())
}

func glassRimControlLive(host GlassHost) bool {
	return host.GOOS == "darwin"
}

// WindowsGlassUsesAccent is the custom acrylic path where blur/frost work.
func WindowsGlassUsesAccent(c Config, winBuild uint32) bool {
	if c.Backdrop != BackdropGlass {
		return false
	}
	if c.GlassVeil > 0 && c.GlassVeil < 100 {
		return true
	}
	return winBuild > 0 && winBuild < Win11BackdropBuild
}

// GlassBlurIdleHint is the settings value when Blur is a no-op on Windows.
const GlassBlurIdleHint = "Windows controls frost"

// GlassRimIdleHint is the settings value when Rim does nothing (not macOS).
const GlassRimIdleHint = "macOS only"
