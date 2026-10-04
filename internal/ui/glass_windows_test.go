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
		t.Fatalf("blur 0 kind %d", glassBackdropType(sharp))
	}
	if !glassUsesColorKey(sharp) {
		t.Fatal("glass veil 0 should color-key")
	}

	live := sharp
	live.GlassBlur = 16
	if glassBackdropType(live) != dwmsbtTransientWindow {
		t.Fatalf("blur 16 kind %d want acrylic", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurDefault
	if glassBackdropType(live) != dwmsbtTabbedWindow {
		t.Fatalf("default blur kind %d want tabbed", glassBackdropType(live))
	}
	live.GlassBlur = config.GlassBlurMax
	if glassBackdropType(live) != dwmsbtTabbedWindow {
		t.Fatalf("max blur kind %d want tabbed", glassBackdropType(live))
	}
	s16, c16 := glassAccentForBlur(16, 0)
	s48, c48 := glassAccentForBlur(48, 0)
	if s16 != accentAcrylic || s48 != accentAcrylic {
		t.Fatalf("positive blur should enable acrylic accent %d %d", s16, s48)
	}
	if c16>>24 >= c48>>24 {
		t.Fatalf("blur 48 must frost more than 16: %#x %#x", c16, c48)
	}
	if st, _ := glassAccentForBlur(0, 0); st != accentDisabled {
		t.Fatal("blur 0 is mica — no accent")
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
