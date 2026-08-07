package chrome

import (
	"runtime"
	"sync"
)

// Key glyph mode: prefer compact Unicode (⌃⇧⌘) when the host font has them;
// otherwise fall back to ASCII "Ctrl+" / "Cmd+" / "Shift+" so we never show □.
//
// Primary modifier is platform-aware: macOS → Cmd/⌘, Windows/Linux → Ctrl/⌃.
// The UI probes the active font (GetGlyphIndicesW / Core Text) and calls
// SetKeyGlyphSupport.

var (
	keyMu     sync.RWMutex
	keyFancy  bool // ⌃ ⇧ ⌘ available
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

func isDarwin() bool { return runtime.GOOS == "darwin" }

// primaryMod is the host "command" key: Cmd on macOS, Ctrl elsewhere.
func primaryMod() string {
	if isDarwin() {
		if keyFancyOn() {
			return "⌘"
		}
		return "Cmd+"
	}
	if keyFancyOn() {
		return "⌃"
	}
	return "Ctrl+"
}

// primaryShift is primary+Shift (⌘⇧ / Cmd+Shift+ / ⌃⇧ / Ctrl+Shift+).
func primaryShift() string {
	if isDarwin() {
		if keyFancyOn() {
			return "⌘⇧"
		}
		return "Cmd+Shift+"
	}
	if keyFancyOn() {
		return "⌃⇧"
	}
	return "Ctrl+Shift+"
}

// --- Labeled chords used in help, splash, and host placeholder ---

// KeyCtrl labels the platform primary modifier + key (Cmd on macOS, Ctrl else).
// Named KeyCtrl for historical call sites; the string is platform-correct.
func KeyCtrl(s string) string {
	return primaryMod() + s
}

func KeyShift(s string) string {
	if keyFancyOn() {
		return "⇧" + s
	}
	return "Shift+" + s
}

func KeyCtrlShift(s string) string {
	return primaryShift() + s
}

// KeyAlt is Option on macOS, Alt elsewhere.
func KeyAlt(s string) string {
	if isDarwin() {
		if keyFancyOn() {
			return "⌥" + s
		}
		return "Opt+" + s
	}
	return "Alt+" + s
}

// KeyCtrlAlt is Cmd+Opt on macOS, Ctrl+Alt elsewhere.
func KeyCtrlAlt(s string) string {
	if isDarwin() {
		if keyFancyOn() {
			return "⌘⌥" + s
		}
		return "Cmd+Opt+" + s
	}
	if keyFancyOn() {
		return "⌃⎇" + s
	}
	return "Ctrl+Alt+" + s
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
