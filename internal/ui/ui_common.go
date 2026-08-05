//go:build windows || darwin

package ui

import (
	"strings"
	"time"
)

const (
	appTitle = "suzuri（硯）"

	// Hard caps so a bad metric cannot ask PTY/VT for a multi-megagrid.
	maxTermCols = 400
	maxTermRows = 200

	// Smooth opacity pulse (sine), not a hard on/off blink.
	cursorBlinkPeriod = 1200 * time.Millisecond
	cursorBlinkTick   = 40 * time.Millisecond // ~25 fps for soft fade

	// Startup rain: spawn new streams for this long, then let them fall off.
	matrixIntroSpawn = 2 * time.Second
	// Safety cap so wind-down cannot run forever on a stuck paint path.
	matrixIntroMaxTotal = 12 * time.Second

	// How long after last PTY output a tab counts as "busy" for the strip glyph.
	tabBusyWindow = 600 * time.Millisecond
	// Fullscreen apps (Grok, etc.) stream in bursts — keep the spinner alive
	// longer between chunks so it doesn't freeze mid-response.
	tabBusyWindowAlt = 2500 * time.Millisecond
	// Advance braille spinner every N blink ticks (40ms → ~80ms/frame).
	tabSpinEveryNTicks = 2

	// conPtyIOQuiet: do not ResizePseudoConsole while a pane has recent I/O.
	// Dual alt-screen Grok + mid-stream resize hard-crashes the Windows host
	// (no Go panic). Title spinners alone must NOT block forever — only bytes.
	conPtyIOQuiet = 300 * time.Millisecond
	// layoutDeferMaxWait: after this, force settle even if I/O is still hot so
	// split/window resize reflows (avoids permanent letterbox under Grok).
	layoutDeferMaxWait = 1500 * time.Millisecond

	cellW = 9
	cellH = 18

	maxTabs        = 16
	tabBarFallback = 36 // used before first paint measures font

	// Store longer OSC titles; chrome truncates to the strip budget at paint.
	maxStoredTitleRunes = 96
)

// shortTitle normalizes an OSC / process title for the tab strip (path basenaming).
// Truncation to strip width happens in chrome.tabLabel, not here.
//
// Strips leading braille/spinner/status marks that apps (e.g. Grok) put in the
// process title — the host already paints one tab-state glyph, so leaving those
// would show two indicators. Call titleReportsBusy on the raw title first if
// you need the app's activity signal.
func shortTitle(s string) string {
	s = strings.TrimSpace(s)
	s = stripLeadingStatusGlyphs(s)
	if s == "" {
		return "shell"
	}
	if i := strings.LastIndexAny(s, `/\`); i >= 0 && i+1 < len(s) {
		s = s[i+1:]
	}
	// Path basenames can still start with a spinner if the path was odd; strip again.
	s = stripLeadingStatusGlyphs(s)
	if s == "" {
		return "shell"
	}
	rs := []rune(s)
	if len(rs) > maxStoredTitleRunes {
		return string(rs[:maxStoredTitleRunes-1]) + "…"
	}
	return s
}

// titleReportsBusy is true when an app embeds a CLI spinner in its OSC title
// (Grok updates the window title with braille frames while a turn is running).
func titleReportsBusy(raw string) bool {
	rs := []rune(strings.TrimSpace(raw))
	if len(rs) == 0 {
		return false
	}
	i := 0
	for i < len(rs) && (rs[i] == ' ' || rs[i] == '\t') {
		i++
	}
	if i >= len(rs) {
		return false
	}
	return isSpinnerFrameRune(rs[i])
}

// isSpinnerFrameRune is a frame from common braille spinners (cli-spinners
// "dots" / "dots2"), not a static badge like ⠿ or ⠁.
func isSpinnerFrameRune(r rune) bool {
	switch r {
	case '⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏':
		return true
	case '⣾', '⣽', '⣻', '⢿', '⡿', '⣟', '⣯', '⣷':
		return true
	case '◐', '◓', '◑', '◒':
		return true
	default:
		return false
	}
}

// stripLeadingStatusGlyphs removes leading activity marks apps embed in OSC titles.
func stripLeadingStatusGlyphs(s string) string {
	rs := []rune(s)
	for len(rs) > 0 {
		r := rs[0]
		if r == ' ' || r == '\t' {
			rs = rs[1:]
			continue
		}
		// Braille / geometric / blocks: always drop as a title prefix spinner.
		if isTitleStatusGlyph(r) {
			rs = rs[1:]
			continue
		}
		// ASCII marks only when used as a badge ("* Grok"), not "xargs".
		if isAsciiTitleBadge(r) && len(rs) > 1 && (rs[1] == ' ' || rs[1] == '\t') {
			rs = rs[1:]
			continue
		}
		break
	}
	return strings.TrimSpace(string(rs))
}

func isTitleStatusGlyph(r rune) bool {
	// Braille Patterns (CLI spinners: ⠋⠙⠿⣿ …) — what Grok puts in the title.
	if r >= 0x2800 && r <= 0x28FF {
		return true
	}
	// Geometric shapes often used as status (●○◉◎◆◇…)
	if r >= 0x25A0 && r <= 0x25FF {
		return true
	}
	// Block elements occasionally used as meters
	if r >= 0x2580 && r <= 0x259F {
		return true
	}
	switch r {
	case '•', '·', '∙', '…', '⋯':
		return true
	}
	return false
}

func isAsciiTitleBadge(r rune) bool {
	switch r {
	case '*', '+', '#', '!', 'x', 'X', 'o', 'O':
		return true
	}
	return false
}
