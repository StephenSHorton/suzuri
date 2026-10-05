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
	if glassBackdropType(sharp) != dwmsbtNone {
		t.Fatalf("glass kind %d want none (accent acrylic, not HostBackdrop)", glassBackdropType(sharp))
	}
	if !glassUsesColorKey(sharp) {
		t.Fatal("glass veil 0 should color-key")
	}

	live := sharp
	live.GlassBlur = 16
	if glassBackdropType(live) != dwmsbtNone {
		t.Fatalf("blur 16 kind %d want none", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurDefault
	if glassBackdropType(live) != dwmsbtNone {
		t.Fatalf("default blur kind %d want none", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurMax
	if glassBackdropType(live) != dwmsbtNone {
		t.Fatalf("max blur kind %d want none", glassBackdropType(live))
	}

	s0, f0, c0 := glassAccentForBlur(0, 0)
	s16, f16, c16 := glassAccentForBlur(16, 0)
	s36, f36, c36 := glassAccentForBlur(36, 0)
	s40, f40, c40 := glassAccentForBlur(40, 0)
	s48, f48, c48 := glassAccentForBlur(48, 0)
	for _, tc := range []struct {
		blur  int
		state uint32
		flags uint32
		color uint32
	}{
		{0, s0, f0, c0},
		{16, s16, f16, c16},
		{36, s36, f36, c36},
		{40, s40, f40, c40},
		{48, s48, f48, c48},
	} {
		if tc.state != accentAcrylic {
			t.Fatalf("blur %d should stay acrylic, got %d", tc.blur, tc.state)
		}
		if tc.flags != accentFlagUseGradient {
			t.Fatalf("blur %d Flags=%d want 2 (else DWM uses the system accent)", tc.blur, tc.flags)
		}
		if rgb := tc.color & 0x00FFFFFF; rgb != glassThemeTint(0)&0x00FFFFFF {
			t.Fatalf("blur %d RGB %#x want theme void", tc.blur, tc.color)
		}
	}
	if c16>>24 >= c48>>24 {
		t.Fatalf("blur 48 must frost more than 16: %#x %#x", c16, c48)
	}
	if int(c40>>24)-int(c36>>24) > 12 {
		t.Fatalf("36→40 must not cliff: %#x %#x", c36, c40)
	}

	st11, fl11, col11 := glassCompositionAccent(22621, 16, 0)
	if st11 != accentAcrylic || fl11 != accentFlagUseGradient {
		t.Fatalf("Win11 must keep Flags=2 acrylic (not HostBackdrop): %d %d %#x", st11, fl11, col11)
	}
	if col11>>24 == 0 || col11&0x00FFFFFF != glassThemeTint(0)&0x00FFFFFF {
		t.Fatalf("Win11 tint must be theme void AABBGGRR %#x", col11)
	}
	st10, fl10, col10 := glassCompositionAccent(19041, 16, 0)
	if st10 != accentAcrylic || fl10 != accentFlagUseGradient || col10>>24 == 0 {
		t.Fatalf("Win10 fallback %#x flags %d color %#x", st10, fl10, col10)
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
			if glassBackdropType(c) != dwmsbtNone {
				t.Fatalf("blur %d veil %d kind %d", blur, veil, glassBackdropType(c))
			}
			st, fl, col := glassAccentForBlur(c.GlassBlur, c.GlassVeil)
			if st != accentAcrylic || fl != accentFlagUseGradient {
				t.Fatalf("blur %d veil %d accent %d flags %d", blur, veil, st, fl)
			}
			if col&0x00FFFFFF != glassThemeTint(0)&0x00FFFFFF {
				t.Fatalf("blur %d veil %d tint %#x", blur, veil, col)
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
	if glassBackdropType(got) != dwmsbtNone {
		t.Fatalf("loaded kind %d", glassBackdropType(got))
	}
	if !glassUsesColorKey(got) {
		t.Fatal("loaded glass should color-key")
	}
}
