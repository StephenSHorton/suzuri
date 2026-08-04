//go:build windows || darwin

package ui

import (
	"math"
	"strings"
	"time"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// rainCell is one underlay glyph stamp in cell coordinates (shared by rain,
// grain, waves, fireflies, and intro curtains on both hosts).
type rainCell struct {
	X, Y       int
	Ch         rune
	FR, FG, FB byte
}

// ambientColors is the theme accent set used by non-rain underlays.
type ambientColors struct {
	pr, pg, pb byte // primary / border
	sr, sg, sb byte // soft
	mr, mg, mb byte // mute
	tr, tg, tb byte // text
}

// ambientGlyphCells returns underlay stamps for grain / waves / fireflies.
// Rain stays on the classic matrixRainCells path. CRT is pixel-level (scanlines).
func ambientGlyphCells(kind string, cols, rows int, t0 time.Time, now time.Time, col ambientColors, intensity float64) []rainCell {
	if cols < 2 || rows < 1 || intensity <= 0 {
		return nil
	}
	if intensity > 1 {
		intensity = 1
	}
	if t0.IsZero() {
		t0 = now
	}
	t := now.Sub(t0).Seconds()
	if t < 0 {
		t = 0
	}
	switch strings.ToLower(kind) {
	case config.AmbientGrain:
		return grainCells(cols, rows, t, col, intensity)
	case config.AmbientWaves:
		return waveCells(cols, rows, t, col, intensity)
	case config.AmbientFireflies:
		return fireflyCells(cols, rows, t, col, intensity)
	default:
		return nil
	}
}

func grainCells(cols, rows int, t float64, col ambientColors, intensity float64) []rainCell {
	// Nearly static paper grain: epoch advances ~once every 4s (not TV static).
	// Sparse · only, very dim mute→soft mix.
	epoch := int(t / 4.0)
	var out []rainCell
	// ~0.6% of cells at full intensity (was ~3% and reseeded 12×/s).
	thresh := int(6 * intensity)
	if thresh < 1 {
		thresh = 1
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			h := hash2(x, y, epoch)
			if h%1000 >= thresh {
				continue
			}
			// Keep brightness very low so it reads as texture, not noise.
			a := 0.12 + float64(h%20)/200.0 // 0.12–0.22
			r := blendB(col.mr, col.sr, a)
			g := blendB(col.mg, col.sg, a)
			b := blendB(col.mb, col.sb, a)
			// Dim further by intensity.
			r = scaleB(r, 0.35*intensity+0.15)
			g = scaleB(g, 0.35*intensity+0.15)
			b = scaleB(b, 0.35*intensity+0.15)
			out = append(out, rainCell{X: x, Y: y, Ch: '·', FR: r, FG: g, FB: b})
		}
	}
	return out
}

func waveCells(cols, rows int, t float64, col ambientColors, intensity float64) []rainCell {
	var out []rainCell
	// Seigaiha-ish: concentric-ish sine crests drifting slowly.
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			fx := float64(x) / float64(cols)
			fy := float64(y) / float64(rows)
			v := math.Sin(fx*10+t*0.7) + math.Sin(fy*8-t*0.5)*0.7 + math.Sin((fx+fy)*6+t*0.35)*0.5
			// Crest band.
			if math.Abs(v) < 1.15 {
				continue
			}
			w := (math.Abs(v) - 1.15) / 0.85
			if w > 1 {
				w = 1
			}
			w *= intensity
			if w < 0.08 {
				continue
			}
			r := blendB(col.mr, col.pr, w)
			g := blendB(col.mg, col.pg, w)
			b := blendB(col.mb, col.pb, w)
			ch := rune('~')
			if w > 0.55 {
				ch = '≈'
			}
			out = append(out, rainCell{X: x, Y: y, Ch: ch, FR: r, FG: g, FB: b})
		}
	}
	return out
}

func fireflyCells(cols, rows int, t float64, col ambientColors, intensity float64) []rainCell {
	// Few slow-drifting sparks (not a glitter storm). Soft ·/• only.
	n := 3 + int(4*intensity) // 3–7
	if n < 3 {
		n = 3
	}
	if n > 8 {
		n = 8
	}
	out := make([]rainCell, 0, n)
	for i := 0; i < n; i++ {
		seed := float64(i*97 + 13)
		// Very slow orbits (~30–50s period).
		px := (math.Sin(t*0.08+seed) + 1) * 0.5
		py := (math.Cos(t*0.06+seed*1.3) + 1) * 0.5
		px = math.Mod(px+float64(i%5)*0.11, 1)
		py = math.Mod(py+float64(i%3)*0.09+t*0.004, 1)
		x := int(px * float64(cols))
		y := int(py * float64(rows))
		if x < 0 || x >= cols || y < 0 || y >= rows {
			continue
		}
		// Slow gentle pulse (not stroboscopic).
		pulse := 0.25 + 0.45*(0.5+0.5*math.Sin(t*0.55+seed))
		pulse *= intensity
		if pulse < 0.1 {
			continue
		}
		r := blendB(col.mr, col.pr, pulse*0.7)
		g := blendB(col.mg, col.pg, pulse*0.7)
		b := blendB(col.mb, col.pb, pulse*0.7)
		ch := rune('·')
		if pulse > 0.45 {
			ch = '•'
		}
		out = append(out, rainCell{X: x, Y: y, Ch: ch, FR: r, FG: g, FB: b})
	}
	return out
}

// inkWashCells is the intro curtain: expanding ink blot from center.
func inkWashCells(cols, rows int, t0 time.Time, spawnFor time.Duration, now time.Time, col ambientColors) []rainCell {
	if cols < 2 || rows < 1 {
		return nil
	}
	if t0.IsZero() {
		t0 = now
	}
	t := now.Sub(t0).Seconds()
	if t < 0 {
		t = 0
	}
	spawn := spawnFor.Seconds()
	if spawn < 0.5 {
		spawn = 2
	}
	// Radius grows for spawn, then ink fades while expanding slowly.
	cx := float64(cols-1) / 2
	cy := float64(rows-1) / 2
	maxR := math.Hypot(cx, cy) + 2
	progress := t / spawn
	if progress > 1 {
		progress = 1
	}
	front := progress * maxR * 1.15
	// Fade after spawn window.
	fade := 1.0
	if t > spawn {
		fade = 1 - (t-spawn)/(spawn*1.2)
		if fade < 0 {
			return nil
		}
		front = maxR * (1.1 + (t-spawn)*0.15)
	}
	var out []rainCell
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			// Soft edge band.
			edge := front - d
			if edge < -1.5 || edge > front*0.85+2 {
				// Outside ring or deep inside core — core is denser ink.
				if d > front {
					continue
				}
			}
			// Density higher near front and center blotches.
			core := 1 - d/(front+0.01)
			if core < 0 {
				core = 0
			}
			band := 1 - math.Abs(edge)/2.5
			if band < 0 {
				band = 0
			}
			a := (core*0.55 + band*0.7) * fade
			// Speckle
			h := hash2(x, y, int(t*8))
			if h%100 > int(a*95) {
				continue
			}
			a = a * (0.5 + float64(h%50)/100)
			if a < 0.08 {
				continue
			}
			if a > 1 {
				a = 1
			}
			r := blendB(0x08, col.pr, a)
			g := blendB(0x08, col.pg, a)
			b := blendB(0x0a, col.pb, a)
			ch := rune('░')
			if a > 0.55 {
				ch = '▒'
			}
			if a > 0.8 {
				ch = '▓'
			}
			out = append(out, rainCell{X: x, Y: y, Ch: ch, FR: r, FG: g, FB: b})
		}
	}
	return out
}

// crtIntroCells: dense scanline glyphs that wash out during boot.
func crtIntroCells(cols, rows int, t0 time.Time, spawnFor time.Duration, now time.Time, col ambientColors) []rainCell {
	if cols < 2 || rows < 1 {
		return nil
	}
	if t0.IsZero() {
		t0 = now
	}
	t := now.Sub(t0).Seconds()
	spawn := spawnFor.Seconds()
	if spawn < 0.5 {
		spawn = 2
	}
	fade := 1.0
	if t > spawn*0.6 {
		fade = 1 - (t-spawn*0.6)/(spawn*0.8)
		if fade < 0 {
			return nil
		}
	}
	// Brightness pulse at start (phosphor hit).
	flash := 1.0
	if t < 0.25 {
		flash = 1.4
	}
	var out []rainCell
	for y := 0; y < rows; y++ {
		if y%2 != 0 {
			continue // scanline gaps
		}
		for x := 0; x < cols; x++ {
			// Rolling bright band.
			band := 0.35 + 0.65*math.Sin(float64(y)*0.4-t*8+float64(x)*0.05)
			if band < 0.2 {
				continue
			}
			a := band * fade * flash
			if a > 1 {
				a = 1
			}
			r := blendB(0, col.pr, a)
			g := blendB(0, col.pg, a)
			b := blendB(0, col.pb, a)
			out = append(out, rainCell{X: x, Y: y, Ch: '─', FR: r, FG: g, FB: b})
		}
	}
	return out
}

func hash2(x, y, epoch int) int {
	// Integer hash (no allocations).
	n := x*374761393 + y*668265263 + epoch*362437
	n = (n ^ (n >> 13)) * 1274126177
	n = n ^ (n >> 16)
	if n < 0 {
		n = -n
	}
	return n
}

func blendB(a, b byte, t float64) byte {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return byte(float64(a)*(1-t) + float64(b)*t)
}

func scaleB(v byte, intensity float64) byte {
	if intensity >= 1 {
		return v
	}
	if intensity <= 0 {
		return 0
	}
	return byte(float64(v) * intensity)
}

// effectiveAmbientIntensity matches rain scaling.
func effectiveAmbientIntensity(cfg config.Config, altScreen bool) float64 {
	return effectiveShellMatrixIntensity(cfg, altScreen)
}
