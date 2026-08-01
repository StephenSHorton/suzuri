package chrome

import "sync"

// Key glyph mode: prefer compact Unicode (⌃⇧) when the host font has them;
// otherwise fall back to ASCII "Ctrl+" / "Shift+" so we never show □.
//
// The UI probes the active HFONT (GetGlyphIndicesW) and calls SetKeyGlyphSupport.

var (
	keyMu     sync.RWMutex
	keyFancy  bool // ⌃ ⇧ available
	keyArrows bool // ↑ ↓ available
)

// SetKeyGlyphSupport updates whether fancy key glyphs may be used in chrome
// labels and related copy. Safe to call from the UI thread when the font changes.
func SetKeyGlyphSupport(fancyModifiers, arrows bool) {
	keyMu.Lock()
	keyFancy = fancyModifiers
	keyArrows = arrows
	keyMu.Unlock()
}

func keyFancyOn() bool {
	keyMu.RLock()
	defer keyMu.RUnlock()
	return keyFancy
}

func keyArrowsOn() bool {
	keyMu.RLock()
	defer keyMu.RUnlock()
	return keyArrows
}

// --- Labeled chords used in help, splash, and host placeholder ---

func KeyCtrl(s string) string {
	if keyFancyOn() {
		return "⌃" + s
	}
	return "Ctrl+" + s
}

func KeyShift(s string) string {
	if keyFancyOn() {
		return "⇧" + s
	}
	return "Shift+" + s
}

func KeyCtrlShift(s string) string {
	if keyFancyOn() {
		return "⌃⇧" + s
	}
	return "Ctrl+Shift+" + s
}

func KeyUpDown() string {
	if keyArrowsOn() {
		return "↑ / ↓"
	}
	return "Up / Down"
}

// InputBarPlaceholder is the empty Warp-bar hint (host paints this string).
func InputBarPlaceholder() string {
	if keyFancyOn() {
		return "type a command — Enter to run · ⇧Enter newline"
	}
	return "type a command — Enter to run · Shift+Enter newline"
}
