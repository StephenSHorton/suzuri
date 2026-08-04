//go:build windows || darwin

package ui

import (
	"testing"
	"time"

	"github.com/StephenSHorton/suzuri/internal/config"
)

func TestAmbientGlyphCellsKinds(t *testing.T) {
	col := ambientColors{pr: 200, pg: 180, pb: 100, sr: 120, sg: 120, sb: 120, mr: 60, mg: 60, mb: 60}
	now := time.Now()
	t0 := now.Add(-time.Second)
	for _, kind := range []string{config.AmbientGrain, config.AmbientWaves, config.AmbientFireflies} {
		cells := ambientGlyphCells(kind, 40, 20, t0, now, col, 0.8)
		if len(cells) == 0 {
			t.Fatalf("%s produced no cells", kind)
		}
		for _, c := range cells {
			if c.X < 0 || c.X >= 40 || c.Y < 0 || c.Y >= 20 {
				t.Fatalf("%s cell out of bounds: %+v", kind, c)
			}
		}
	}
	// Rain kind is not handled here (uses matrixRainCells).
	if cells := ambientGlyphCells(config.AmbientRain, 40, 20, t0, now, col, 1); len(cells) != 0 {
		t.Fatalf("rain should not use glyph path, got %d", len(cells))
	}
}

func TestInkWashAndCRTIntro(t *testing.T) {
	col := ambientColors{pr: 180, pg: 160, pb: 200, sr: 100, sg: 100, sb: 100, mr: 40, mg: 40, mb: 40}
	t0 := time.Now()
	now := t0.Add(500 * time.Millisecond)
	ink := inkWashCells(50, 24, t0, 2*time.Second, now, col)
	if len(ink) == 0 {
		t.Fatal("ink wash empty mid-spawn")
	}
	crt := crtIntroCells(50, 24, t0, 2*time.Second, now, col)
	if len(crt) == 0 {
		t.Fatal("crt intro empty mid-spawn")
	}
	// After long fade, ink should empty.
	late := inkWashCells(50, 24, t0, 2*time.Second, t0.Add(10*time.Second), col)
	if len(late) != 0 {
		t.Fatalf("ink wash should finish, got %d cells", len(late))
	}
}
