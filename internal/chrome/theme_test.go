package chrome

import (
	"testing"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestApplyThemeAllIDs(t *testing.T) {
	// Every config ThemeIDs entry must have a catalog palette (or inkstone fallback).
	// Apply each and smoke-check GDI primary + text are non-zero for readability.
	for _, id := range config.ThemeIDs() {
		ApplyTheme(id)
		if TextR == 0 && TextG == 0 && TextB == 0 {
			t.Fatalf("theme %q left text black-on-black", id)
		}
		// Catalog must own every id (no silent inkstone fallback for listed themes).
		if _, ok := themeCatalog[id]; !ok {
			t.Fatalf("theme %q missing from themeCatalog", id)
		}
		// ANSI table should not be all zeros.
		sum := 0
		for i := 0; i < 16; i++ {
			sum += int(ShellANSI16[i][0]) + int(ShellANSI16[i][1]) + int(ShellANSI16[i][2])
		}
		if sum == 0 {
			t.Fatalf("theme %q ANSI table empty", id)
		}
	}
	// Unknown id falls back without panic.
	ApplyTheme("totally_unknown_theme_xyz")
	if _, ok := themeCatalog[config.ThemeInkstone]; !ok {
		t.Fatal("inkstone missing")
	}
}

func TestOnPrimaryWhiteForBrightPrimaries(t *testing.T) {
	// These themes use bright primary fills for selection — onPrimary must be
	// white so active tabs / settings rows stay readable.
	wantWhite := []string{
		config.ThemeMonokai,
		config.ThemeCharmtone,
		config.ThemeKanagawa,
		config.ThemeOneDark,
	}
	for _, id := range wantWhite {
		ApplyTheme(id)
		if OnPrimR < 240 || OnPrimG < 240 || OnPrimB < 240 {
			t.Fatalf("theme %q onPrimary want near-white, got #%02x%02x%02x",
				id, OnPrimR, OnPrimG, OnPrimB)
		}
	}
}

func TestSettingsShowcaseIntro(t *testing.T) {
	m := Model{}
	if m.SettingsShowcaseIntro() {
		t.Fatal("closed settings should not showcase intro")
	}
	m.SettingsOpen = true
	m.settings.field = fieldFontFace
	if m.SettingsShowcaseIntro() {
		t.Fatal("default field should showcase ambient, not intro")
	}
	m.settings.field = fieldIntro
	if !m.SettingsShowcaseIntro() {
		t.Fatal("Intro field should showcase intro")
	}
	m.settings.field = fieldShellAmbient
	if m.SettingsShowcaseIntro() {
		t.Fatal("Ambient field should showcase ambient")
	}
}
