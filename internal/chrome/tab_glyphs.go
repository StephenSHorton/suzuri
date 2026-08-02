package chrome

import (
	"sync"
	"sync/atomic"
)

// Tab strip state glyphs. Prefer braille (the classic CLI spinner family);
// geometric and ASCII are fallbacks when the font cannot draw braille.
//
// Apps like Grok also put braille spinners in the OSC title — shortTitle strips
// those so only this host glyph shows (never two markers).
//
// Host probes the active HFONT and calls SetTabStateGlyphs.
// Busy states use Spin frames when non-empty (host advances via AdvanceTabSpinner).

// TabGlyphSet is one complete pack of strip markers (each string ends with a space).
type TabGlyphSet struct {
	Dead    string // session ended
	AltBusy string // fullscreen + recent output (static fallback if Spin empty)
	AltIdle string // fullscreen idle
	Busy    string // shell has recent output (static fallback if Spin empty)
	// Spin is the animated braille (or other) cycle for Busy / AltBusy.
	// Empty → use Busy / AltBusy as static marks.
	Spin []string
}

var (
	tabGlyphMu sync.RWMutex
	// Default ASCII — safe before the first font probe.
	tabGlyphCur = TabGlyphsASCII
	spinFrame   atomic.Uint64
)

// Classic CLI braille spinner (cli-spinners "dots").
var brailleSpinFrames = []string{
	"⠋ ", "⠙ ", "⠹ ", "⠸ ", "⠼ ", "⠴ ", "⠦ ", "⠧ ", "⠇ ", "⠏ ",
}

// Preset packs.
var (
	// Braille: static idle/dead + animated spin for activity.
	TabGlyphsBraille = TabGlyphSet{
		Dead:    "⠁ ",
		AltBusy: "⣿ ", // fallback if Spin empty
		AltIdle: "⠿ ",
		Busy:    "⣷ ",
		Spin:    brailleSpinFrames,
	}
	// Geometric shapes — mild half-circle spin when busy.
	TabGlyphsGeometric = TabGlyphSet{
		Dead:    "○ ",
		AltBusy: "◉ ",
		AltIdle: "◎ ",
		Busy:    "● ",
		Spin:    []string{"◐ ", "◓ ", "◑ ", "◒ "},
	}
	// Last-resort monospaced-safe (no animation).
	TabGlyphsASCII = TabGlyphSet{
		Dead:    "x ",
		AltBusy: "* ",
		AltIdle: "# ",
		Busy:    "+ ",
	}
)

// SetTabStateGlyphs installs the strip markers for the current UI font.
func SetTabStateGlyphs(set TabGlyphSet) {
	tabGlyphMu.Lock()
	tabGlyphCur = set
	tabGlyphMu.Unlock()
}

// AdvanceTabSpinner steps the busy-tab animation (call from a UI timer).
func AdvanceTabSpinner() {
	spinFrame.Add(1)
}

// TabSpinnerFrame is the current busy glyph (for tests / debug).
func TabSpinnerFrame() string {
	g := tabGlyphs()
	if len(g.Spin) == 0 {
		return g.Busy
	}
	i := spinFrame.Load() % uint64(len(g.Spin))
	return g.Spin[i]
}

func tabGlyphs() TabGlyphSet {
	tabGlyphMu.RLock()
	defer tabGlyphMu.RUnlock()
	return tabGlyphCur
}

func busyGlyph(g TabGlyphSet, alt bool) string {
	if len(g.Spin) > 0 {
		i := spinFrame.Load() % uint64(len(g.Spin))
		return g.Spin[i]
	}
	if alt {
		return g.AltBusy
	}
	return g.Busy
}

// TabGlyphRunes returns the distinct non-space runes in a set (for font probing).
func TabGlyphRunes(set TabGlyphSet) []rune {
	seen := map[rune]struct{}{}
	var out []rune
	add := func(s string) {
		for _, r := range s {
			if r == ' ' {
				continue
			}
			if _, ok := seen[r]; ok {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	for _, s := range []string{set.Dead, set.AltBusy, set.AltIdle, set.Busy} {
		add(s)
	}
	for _, s := range set.Spin {
		add(s)
	}
	return out
}
