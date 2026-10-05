//go:build windows

package ui

import (
	"testing"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestGlassBackdropFollowsConfig(t *testing.T) {
	solid := config.Normalize(config.Config{})
	if solid.Backdrop != config.BackdropSolid {
		t.Fatalf("default backdrop %q", solid.Backdrop)
	}
	if glassBackdropType(solid) != dwmsbtNone {
		t.Fatalf("solid kind %d", glassBackdropType(solid))
	}
	if glassUsesColorKey(solid) {
		t.Fatal("solid must not color-key")
	}

	sharp := config.Normalize(config.Config{Backdrop: config.BackdropGlass, GlassBlur: 0, GlassVeil: 0})
	if glassBackdropType(sharp) != dwmsbtMainWindow {
		t.Fatalf("blur 0 kind %d want Mica (classic HostBackdrop)", glassBackdropType(sharp))
	}
	if !glassUsesColorKey(sharp) {
		t.Fatal("glass veil 0 should color-key")
	}

	live := sharp
	live.GlassBlur = 16
	if glassBackdropType(live) != dwmsbtTransientWindow {
		t.Fatalf("blur 16 kind %d want Desktop Acrylic", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurDefault
	if glassBackdropType(live) != dwmsbtTransientWindow {
		t.Fatalf("default blur kind %d want Desktop Acrylic", glassBackdropType(live))
	}
	live.GlassBlur = 36
	if glassBackdropType(live) != dwmsbtTransientWindow {
		t.Fatalf("blur 36 kind %d want Desktop Acrylic (pre-PR86 classic)", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurMax
	if glassBackdropType(live) != dwmsbtTransientWindow {
		t.Fatalf("max blur kind %d want Desktop Acrylic", glassBackdropType(live))
	}

	if glassUseAccent(22621, true, 0) {
		t.Fatal("Win11 veil 0 must leave HostBackdrop alone (accent replaces it)")
	}
	if !glassUseAccent(19041, true, 0) {
		t.Fatal("Win10 has no HostBackdrop; accent is the blur")
	}
	if !glassUseAccent(22621, true, 40) {
		t.Fatal("veil tint still uses accent")
	}
	if glassUseAccent(22621, false, 0) {
		t.Fatal("solid must not set accent")
	}

	st, fl, col := glassAccentFor(19041, 36, 0)
	if st != accentAcrylic || fl != accentFlagUseGradient || col>>24 == 0 {
		t.Fatalf("Win10 accent %#x flags %d color %#x", st, fl, col)
	}
	st11, fl11, col11 := glassAccentFor(22621, 36, 0)
	if st11 != accentDisabled || fl11 != 0 || col11 != 0 {
		t.Fatalf("Win11 veil 0 must disable accent: %d %d %#x", st11, fl11, col11)
	}
	_, flV, colV := glassAccentFor(22621, 36, 40)
	if flV != accentFlagUseGradient {
		t.Fatalf("veil Flags=%d want 2 (else DWM uses the system accent)", flV)
	}
	if colV>>24 == 0 || colV&0x00FFFFFF != 0 {
		t.Fatalf("veil tint must be black AABBGGRR %#x", colV)
	}
	_, _, colMore := glassAccentFor(22621, 60, 40)
	if colMore>>24 <= colV>>24 {
		t.Fatalf("accent-path blur must raise frost: %#x %#x", colV, colMore)
	}

	opaque := live
	opaque.GlassVeil = 100
	if glassUsesColorKey(opaque) {
		t.Fatal("veil 100 must be an opaque wash, not the color key")
	}
	if glassVeilABGR(0) != 0 {
		t.Fatalf("veil 0 tint %#x", glassVeilABGR(0))
	}
	if glassVeilABGR(100) != 0xFF000000 {
		t.Fatalf("veil 100 tint %#x", glassVeilABGR(100))
	}
	if a := glassVeilABGR(40) >> 24; a < 100 || a > 104 {
		t.Fatalf("veil 40 alpha %d", a)
	}

	var u *winUI
	if u.shellGlass() {
		t.Fatal("nil ui")
	}
	if glassMaterialName(true, dwmsbtTransientWindow, false) != "desktop-acrylic" {
		t.Fatalf("material %s", glassMaterialName(true, dwmsbtTransientWindow, false))
	}
	if glassMaterialName(true, dwmsbtMainWindow, false) != "mica" {
		t.Fatalf("material %s", glassMaterialName(true, dwmsbtMainWindow, false))
	}
}

func TestGlassFrameMarginsAreSheetOfGlass(t *testing.T) {
	on := glassFrameMargins(true)
	if on.Left != -1 || on.Right != -1 || on.Top != -1 || on.Bottom != -1 {
		t.Fatalf("glass must be sheet-of-glass for color-key: %+v", on)
	}
	off := glassFrameMargins(false)
	if off.Top != 0 || off.Left != 0 || off.Right != 0 || off.Bottom != 1 {
		t.Fatalf("solid margins %+v want 1px bottom", off)
	}
}

func TestEveryGlassFrostSetting(t *testing.T) {
	for blur := 0; blur <= config.GlassBlurMax; blur++ {
		for _, veil := range []int{0, 36, 50, 99, 100} {
			c := config.Normalize(config.Config{
				Backdrop: config.BackdropGlass, GlassBlur: blur, GlassVeil: veil,
			})
			want := dwmsbtMainWindow
			if blur > 0 {
				want = dwmsbtTransientWindow
			}
			if glassBackdropType(c) != want {
				t.Fatalf("blur %d veil %d kind %d want %d", blur, veil, glassBackdropType(c), want)
			}
			if veil < 100 && !glassUsesColorKey(c) {
				t.Fatalf("blur %d veil %d must color-key", blur, veil)
			}
			if veil >= 100 && glassUsesColorKey(c) {
				t.Fatalf("blur %d veil 100 must be opaque", blur)
			}
			if m := glassFrameMargins(true); m.Top != -1 || m.Left != -1 || m.Right != -1 || m.Bottom != -1 {
				t.Fatalf("blur %d veil %d margins %+v", blur, veil, m)
			}
			if glassUseAccent(22621, true, veil) != (veil > 0 && veil < 100) {
				t.Fatalf("blur %d veil %d Win11 accent", blur, veil)
			}
		}
	}
}

func TestGlassConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	want := config.Default()
	want.Backdrop = config.BackdropGlass
	want.GlassBlur = 16
	want.GlassVeil = 40
	want.GlassRim = 10
	if err := config.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Backdrop != config.BackdropGlass || got.GlassBlur != 16 || got.GlassVeil != 40 || got.GlassRim != 10 {
		t.Fatalf("round trip %+v", got)
	}
	if glassBackdropType(got) != dwmsbtTransientWindow {
		t.Fatalf("loaded kind %d", glassBackdropType(got))
	}
	if !glassUsesColorKey(got) {
		t.Fatal("loaded glass should color-key")
	}
}

func TestGlassTintRGBIsNotACellFill(t *testing.T) {
	r, g, b := glassTintRGB()
	if r == 0 && g == 0 && b == 0 {
		t.Fatal("tint RGB must not be the color key")
	}
	if !cellBGShowsGlass(0, 0, 0) {
		t.Fatal("holes stay 0,0,0")
	}
}
