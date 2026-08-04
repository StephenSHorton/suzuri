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
