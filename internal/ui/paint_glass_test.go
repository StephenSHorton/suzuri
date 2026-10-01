//go:build darwin

package ui

import (
	"image"
	"testing"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

func TestGlassLeavesChromaHolesAndKeepsRealBands(t *testing.T) {
	p := newSoftwarePainter("", 14)
	if p == nil {
		t.Fatal("painter")
	}
	t.Cleanup(p.close)
	cw, ch := p.metrics()
	if cw < 4 || ch < 4 {
		t.Fatalf("metrics %d x %d", cw, ch)
	}
	w := 4 + cw*2 + 8
	h := ch * 3
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	shell := [][]cellPix{{
		{Ch: ' ', BR: 26, BG: 27, BB: 38},
		{Ch: ' ', BR: 40, BG: 80, BB: 160},
	}}
	chromeRow := []cellPix{{Ch: ' ', BR: chrome.BarR, BG: chrome.BarG, BB: chrome.BarB}}
	p.paintFrame(dst, paintOpts{
		Shell:    shell,
		Chrome:   [][]cellPix{chromeRow},
		PadY:     ch,
		ShellBot: h,
		Glass:    true,
		GlassRim: 210,
	})

	hole := dst.RGBAAt(4+cw/2, ch+ch/2)
	if hole.A != 0 {
		t.Fatalf("chroma cell alpha %d color %v", hole.A, hole)
	}
	margin := dst.RGBAAt(1, ch+2)
	if margin.A != 0 {
		t.Fatalf("shell margin alpha %d", margin.A)
	}
	band := dst.RGBAAt(4+cw+cw/2, ch+ch/2)
	if band.A != 255 {
		t.Fatalf("colored cell alpha %d", band.A)
	}
	strip := dst.RGBAAt(2, 2)
	if strip.A != 255 {
		t.Fatalf("chrome alpha %d", strip.A)
	}

	solid := image.NewRGBA(image.Rect(0, 0, w, h))
	p.paintFrame(solid, paintOpts{
		Shell:    shell,
		Chrome:   [][]cellPix{chromeRow},
		PadY:     ch,
		ShellBot: h,
	})
	if solid.RGBAAt(1, ch+2).A != 255 {
		t.Fatal("solid shell margin should stay opaque")
	}
}

func TestGlassSettingsKeepsHoles(t *testing.T) {
	p := newSoftwarePainter("", 14)
	if p == nil {
		t.Fatal("painter")
	}
	t.Cleanup(p.close)
	cw, ch := p.metrics()
	w := 4 + cw*2 + 8
	h := ch * 3
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	p.paintFrame(dst, paintOpts{
		Shell: [][]cellPix{{
			{Ch: ' ', BR: 0, BG: 0, BB: 0},
		}},
		PadY:         ch,
		ShellBot:     h,
		Glass:        true,
		SettingsOpen: true,
		DimShell:     true,
		MatrixCells: []rainCell{{
			X: 0, Y: 0, Ch: 'ｱ', FR: 80, FG: 255, FB: 120,
		}},
	})
	if dst.RGBAAt(1, ch+2).A != 0 {
		t.Fatalf("settings covered a glass hole, alpha %d", dst.RGBAAt(1, ch+2).A)
	}
}

func TestGlassInkKeepsADarkRim(t *testing.T) {
	p := newSoftwarePainter("", 14)
	if p == nil || p.face == nil {
		t.Skip("no face")
	}
	t.Cleanup(p.close)
	cw, ch := p.metrics()
	w := 4 + cw*2 + 8
	h := ch * 3
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	p.paintFrame(dst, paintOpts{
		Shell: [][]cellPix{{
			{Ch: 'M', FR: 255, FG: 255, FB: 255},
		}},
		PadY:     ch,
		ShellBot: h,
		Glass:    true,
		GlassRim: 210,
	})
	cell := image.Rect(4, ch, 4+cw, ch+ch)
	var rim, ink int
	for y := cell.Min.Y - 1; y < cell.Max.Y+1; y++ {
		for x := cell.Min.X - 1; x < cell.Max.X+1; x++ {
			if !image.Pt(x, y).In(dst.Bounds()) {
				continue
			}
			c := dst.RGBAAt(x, y)
			if c.A > 140 && int(c.R)+int(c.G)+int(c.B) < 80 {
				rim++
			}
			if c.A > 200 && c.R > 180 && c.G > 180 && c.B > 180 {
				ink++
			}
		}
	}
	if ink < 4 {
		t.Fatalf("ink pixels %d", ink)
	}
	if rim < 4 {
		t.Fatalf("halo pixels %d", rim)
	}
}

func TestGlassVeilTintsHolesOnly(t *testing.T) {
	p := newSoftwarePainter("", 14)
	if p == nil {
		t.Fatal("painter")
	}
	t.Cleanup(p.close)
	cw, ch := p.metrics()
	w := 4 + cw*2 + 8
	h := ch * 3
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	shell := [][]cellPix{{
		{Ch: ' ', BR: 26, BG: 27, BB: 38},
		{Ch: ' ', BR: 40, BG: 80, BB: 160},
	}}
	chromeRow := []cellPix{{Ch: ' ', BR: chrome.BarR, BG: chrome.BarG, BB: chrome.BarB}}
	p.paintFrame(dst, paintOpts{
		Shell:     shell,
		Chrome:    [][]cellPix{chromeRow},
		PadY:      ch,
		ShellBot:  h,
		Glass:     true,
		GlassVeil: 100,
	})
	hole := dst.RGBAAt(4+cw/2, ch+ch/2)
	if hole.A != 100 {
		t.Fatalf("veiled hole alpha %d", hole.A)
	}
	band := dst.RGBAAt(4+cw+cw/2, ch+ch/2)
	if band.A != 255 {
		t.Fatalf("colored cell alpha %d", band.A)
	}
	strip := dst.RGBAAt(2, 2)
	if strip.A != 255 {
		t.Fatalf("chrome alpha %d", strip.A)
	}
}
