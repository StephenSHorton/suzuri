//go:build windows

package ui

import (
	"math"
	"syscall"
	"time"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// Ripple uses the same 猫咪 pair as non-settings modal underlays.
var rippleGlyphs [][]uint16

func init() {
	for _, chs := range []string{"猫", "咪"} {
		s, err := syscall.UTF16FromString(chs)
		if err != nil || len(s) < 2 {
			continue
		}
		rippleGlyphs = append(rippleGlyphs, s)
	}
}

// paintRippleIntro draws expanding 猫/咪 rings from the shell center (puddle).
// Color along each crest: theme primary → white → primary → black.
// No matte — pure black shell so the center 硯 has no cutout; rain of rings
// paints over the fading logo. Spawn ~2s then rings expand off-screen.
func (u *winUI) paintRippleIntro(hdc win.HDC, rect win.RECT) {
	defer applog.Recover("paintRippleIntro", false)
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY || len(rippleGlyphs) == 0 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Fullwidth glyphs (same as 猫咪 underlay).
	stepX := cw * 2
	if stepX < 2 {
		stepX = 2
	}
	rows := int((bot - padY + ch - 1) / ch)
	cols := int((rect.Right + stepX - 1) / stepX)
	if rows < 2 || cols < 2 {
		return
	}

	font := u.cjkFont
	if font == 0 {
		font = u.font
	}
	if font == 0 {
		return
	}

	saved := win.SaveDC(hdc)
	if saved == 0 {
		return
	}
	defer win.RestoreDC(hdc, saved)
	_ = win.IntersectClipRect(hdc, 0, padY, rect.Right, bot)

	oldF := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldF)
	win.SetBkMode(hdc, win.TRANSPARENT)

	t0 := u.matrixIntroStart
	if t0.IsZero() {
		t0 = u.blinkStart
	}
	t := time.Since(t0).Seconds()
	if t < 0 {
		t = 0
	}
	spawnT := matrixIntroSpawn.Seconds()
	windDown := t > spawnT

	// Geometry in fullwidth-column × row cells (y scaled for aspect).
	cx := float64(cols-1) / 2
	cy := float64(rows-1) / 2
	// Max radius to corner (cells).
	maxR := math.Hypot(cx, cy*float64(ch)/float64(cw)) + 2
	if maxR < 4 {
		maxR = 4
	}

	// Expanding rings with random gaps between births (stable for the intro).
	const (
		expandSpd = 9.5 // cells per second
		maxRings  = 4
		minGap    = 0.45 // seconds between rings
		maxGap    = 1.15
	)
	// Deterministic PRNG from intro start so every paint frame agrees.
	rng := uint64(t0.UnixNano())
	if rng == 0 {
		rng = 0xC0FFEE1234ABCD
	}
	nextF := func() float64 {
		// xorshift64*
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return float64(rng%10000) / 10000.0
	}
	births := make([]float64, 0, maxRings)
	bt := 0.0
	for len(births) < maxRings && bt <= spawnT+0.001 {
		births = append(births, bt)
		gap := minGap + nextF()*(maxGap-minGap)
		bt += gap
	}

	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB
	// White for the crest peak.
	const wr, wg, wb byte = 245, 245, 250

	drew := false
	// Per-cell best intensity from any ring (avoid overdraw mud).
	type cellHit struct {
		u     float64 // 0..1 along crest gradient
		str   float64 // strength 0..1
		glyph int
	}
	hits := make([]cellHit, cols*rows)

	for i, birth := range births {
		// After spawn, no new rings — existing ones keep expanding.
		if windDown && birth > spawnT {
			continue
		}
		age := t - birth
		if age < 0 {
			continue
		}
		// Vary crest thickness so some rings read larger (cells).
		bandW := 2.15 + float64(i%3)*0.55 // ~2.15, 2.70, 3.25
		radius := age * expandSpd
		if radius > maxR+bandW*2 {
			continue // fully off-screen
		}
		for gy := 0; gy < rows; gy++ {
			for gx := 0; gx < cols; gx++ {
				// Slight vertical squash so rings look round in pixels.
				dx := float64(gx) - cx
				dy := (float64(gy) - cy) * float64(ch) / float64(stepX)
				d := math.Hypot(dx, dy)
				band := math.Abs(d - radius)
				if band > bandW {
					continue
				}
				// u: 0 at inner edge of band, 1 at outer — theme→white→theme→black.
				uCrest := (band/bandW)*.5 + .5*(d-radius+bandW)/(2*bandW)
				if uCrest < 0 {
					uCrest = 0
				}
				if uCrest > 1 {
					uCrest = 1
				}
				// Stronger on the crest center.
				str := 1 - band/bandW
				str = str * str
				// Soften very far rings.
				if radius > maxR*0.75 {
					str *= 1 - (radius-maxR*0.75)/(maxR*0.35)
					if str < 0 {
						continue
					}
				}
				idx := gy*cols + gx
				if str > hits[idx].str {
					hits[idx] = cellHit{
						u:     uCrest,
						str:   str,
						glyph: (gx + gy + i) % len(rippleGlyphs),
					}
				}
			}
		}
	}

	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			h := hits[gy*cols+gx]
			if h.str < 0.08 {
				continue
			}
			fr, fg, fb := rippleWaveColor(h.u, pr, pg, pb, wr, wg, wb)
			// Mix toward black by inverse strength so tails stay soft.
			fr, fg, fb = blendRGB(0, 0, 0, fr, fg, fb, h.str)
			if int(fr)+int(fg)+int(fb) < 36 {
				continue
			}
			s := rippleGlyphs[h.glyph%len(rippleGlyphs)]
			win.SetTextColor(hdc, win.RGB(fr, fg, fb))
			x := int32(gx) * stepX
			y := padY + int32(gy)*ch
			win.TextOut(hdc, x, y, &s[0], int32(len(s)-1))
			drew = true
		}
	}

	if windDown && !drew {
		u.finishMatrixIntro()
	}
}

// rippleWaveColor maps crest parameter u∈[0,1] along:
// primary → white → primary → black.
func rippleWaveColor(u float64, pr, pg, pb, wr, wg, wb byte) (byte, byte, byte) {
	if u < 0 {
		u = 0
	}
	if u > 1 {
		u = 1
	}
	switch {
	case u < 1.0/3:
		// primary → white
		t := u * 3
		return blendRGB(pr, pg, pb, wr, wg, wb, t)
	case u < 2.0/3:
		// white → primary
		t := (u - 1.0/3) * 3
		return blendRGB(wr, wg, wb, pr, pg, pb, t)
	default:
		// primary → black
		t := (u - 2.0/3) * 3
		return blendRGB(pr, pg, pb, 0, 0, 0, t)
	}
}
