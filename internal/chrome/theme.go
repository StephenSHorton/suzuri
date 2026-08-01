package chrome

import (
	"image/color"

	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Active palette (mutated by ApplyTheme). Tuned for calm, Crush-adjacent polish
// rather than loud “neon marketing” chrome.
var (
	colVoid   lipgloss.Color
	colBar    lipgloss.Color
	colPanel  lipgloss.Color
	colAccent lipgloss.Color // primary accent (was “neon”)
	colSoftA  lipgloss.Color // secondary accent
	colCyan   lipgloss.Color
	colText   lipgloss.Color
	colSoft   lipgloss.Color
	colDim    lipgloss.Color
	colMute   lipgloss.Color
	colSel    lipgloss.Color
	colMatch  lipgloss.Color
	colBorder lipgloss.Color // dialog border — quiet, not screaming
)

// Aliases kept for call sites that still say “neon/violet”.
var (
	colNeon   lipgloss.Color
	colViolet lipgloss.Color
)

// Bar / void for GDI.
var (
	BarR, BarG, BarB    byte
	VoidR, VoidG, VoidB byte
	DimR, DimG, DimB    byte // shell dim matte
)

// ShellANSI16 theme remap for SGR 0–15.
var ShellANSI16 [16][3]byte

// StockANSI16 conventional VT palette.
var StockANSI16 = [16][3]byte{
	{12, 12, 12},
	{205, 89, 89},
	{80, 180, 120},
	{210, 180, 70},
	{90, 140, 210},
	{180, 100, 180},
	{70, 170, 190},
	{200, 200, 200},
	{100, 100, 100},
	{230, 110, 110},
	{100, 210, 150},
	{230, 210, 100},
	{120, 170, 240},
	{210, 140, 210},
	{100, 200, 210},
	{240, 240, 240},
}

func init() {
	ApplyTheme(config.ThemeInkstone)
}

// ApplyTheme sets chrome colors, GDI bytes, and ShellANSI16.
func ApplyTheme(id string) {
	switch id {
	case config.ThemeCharmtone:
		// Crush Pantera–inspired: pepper base, soft purple primary.
		setPalette(
			hex("#1a1418"), // void
			hex("#201a1e"), // bar
			hex("#2a2228"), // panel
			hex("#c4b5fd"), // accent soft violet
			hex("#f0d9a8"), // secondary warm
			hex("#7dd3c0"), // mint
			hex("#f3e8ee"), // text
			hex("#c9b8c0"),
			hex("#8a7a84"),
			hex("#5a4a54"),
			hex("#3a2e40"),
			hex("#ddd6fe"),
			hex("#6b5a68"), // border
			hex("#100c10"), // dim matte
		)
		ShellANSI16 = [16][3]byte{
			rgbArr(hex("#1a1418")),
			rgbArr(hex("#e8a0a8")),
			rgbArr(hex("#a6d5a0")),
			rgbArr(hex("#e8d5a0")),
			rgbArr(hex("#b0a0e8")),
			rgbArr(hex("#e0b0d0")),
			rgbArr(hex("#90d0c8")),
			rgbArr(hex("#d0c4cc")),
			rgbArr(hex("#6a5a64")),
			rgbArr(hex("#f0b0b8")),
			rgbArr(hex("#b8e0b0")),
			rgbArr(hex("#f0e0b8")),
			rgbArr(hex("#c0b0f0")),
			rgbArr(hex("#e8c0e0")),
			rgbArr(hex("#a8e0d8")),
			rgbArr(hex("#f8f0f4")),
		}
	case config.ThemeHighContrast:
		setPalette(
			hex("#000000"),
			hex("#0a0a0a"),
			hex("#141414"),
			hex("#00e676"),
			hex("#ffeb3b"),
			hex("#00e5ff"),
			hex("#ffffff"),
			hex("#e0e0e0"),
			hex("#b0b0b0"),
			hex("#707070"),
			hex("#00331a"),
			hex("#69f0ae"),
			hex("#404040"),
			hex("#000000"),
		)
		ShellANSI16 = StockANSI16
		// Punch up a few for HC.
		ShellANSI16[1] = [3]byte{255, 80, 80}
		ShellANSI16[2] = [3]byte{0, 230, 120}
		ShellANSI16[4] = [3]byte{80, 160, 255}
		ShellANSI16[15] = [3]byte{255, 255, 255}
	default: // inkstone — calm ink dark, dusty mauve accent (not hot pink)
		setPalette(
			hex("#0c0c10"), // void almost black
			hex("#141418"), // bar
			hex("#1c1c22"), // panel
			hex("#b8a0c8"), // soft mauve
			hex("#9a9ab0"), // cool secondary
			hex("#7a9a9a"), // muted teal
			hex("#e8e6e3"), // warm white
			hex("#b0aea8"),
			hex("#787870"),
			hex("#484848"),
			hex("#2a2430"), // selection
			hex("#c8b0d8"),
			hex("#3a3840"), // quiet border
			hex("#08080a"), // dim
		)
		ShellANSI16 = [16][3]byte{
			rgbArr(hex("#0c0c10")),
			rgbArr(hex("#c07070")),
			rgbArr(hex("#70a078")),
			rgbArr(hex("#c0a860")),
			rgbArr(hex("#7090b8")),
			rgbArr(hex("#a080a8")),
			rgbArr(hex("#60a0a0")),
			rgbArr(hex("#b8b6b0")),
			rgbArr(hex("#585858")),
			rgbArr(hex("#d88888")),
			rgbArr(hex("#90c098")),
			rgbArr(hex("#d8c880")),
			rgbArr(hex("#90b0d0")),
			rgbArr(hex("#c0a0c8")),
			rgbArr(hex("#88c0c0")),
			rgbArr(hex("#e8e6e3")),
		}
	}
}

// RemapANSI16 returns shell 16-color table for mode.
func RemapANSI16(mode string) [16][3]byte {
	switch mode {
	case config.ANSIMapNone:
		return StockANSI16
	case config.ANSIMapFull:
		return ShellANSI16
	default:
		var out [16][3]byte
		for i := 0; i < 16; i++ {
			for c := 0; c < 3; c++ {
				out[i][c] = byte((int(ShellANSI16[i][c])*50 + int(StockANSI16[i][c])*50) / 100)
			}
		}
		return out
	}
}

func setPalette(void, bar, panel, accent, softA, cyan, text, soft, dim, mute, sel, match, border, dimMatte color.Color) {
	colVoid = toLG(void)
	colBar = toLG(bar)
	colPanel = toLG(panel)
	colAccent = toLG(accent)
	colSoftA = toLG(softA)
	colCyan = toLG(cyan)
	colText = toLG(text)
	colSoft = toLG(soft)
	colDim = toLG(dim)
	colMute = toLG(mute)
	colSel = toLG(sel)
	colMatch = toLG(match)
	colBorder = toLG(border)
	colNeon = colAccent
	colViolet = colSoftA

	BarR, BarG, BarB = rgb8(bar)
	VoidR, VoidG, VoidB = rgb8(void)
	DimR, DimG, DimB = rgb8(dimMatte)
}

func rgbArr(c color.Color) [3]byte {
	r, g, b := rgb8(c)
	return [3]byte{r, g, b}
}

func toLG(c color.Color) lipgloss.Color {
	r, g, b, _ := c.RGBA()
	return lipgloss.Color(hexStr(uint8(r>>8), uint8(g>>8), uint8(b>>8)))
}

func rgb8(c color.Color) (byte, byte, byte) {
	r, g, b, _ := c.RGBA()
	return byte(r >> 8), byte(g >> 8), byte(b >> 8)
}

func hex(s string) color.Color {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{A: 255}
	}
	var r, g, b uint8
	_, _ = parseHexByte(s[1:3], &r)
	_, _ = parseHexByte(s[3:5], &g)
	_, _ = parseHexByte(s[5:7], &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func parseHexByte(s string, out *uint8) (int, error) {
	var v uint8
	for i := 0; i < 2; i++ {
		c := s[i]
		var n byte
		switch {
		case c >= '0' && c <= '9':
			n = c - '0'
		case c >= 'a' && c <= 'f':
			n = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			n = c - 'A' + 10
		}
		v = v<<4 | n
	}
	*out = v
	return 2, nil
}

func hexStr(r, g, b uint8) string {
	const h = "0123456789abcdef"
	return string([]byte{'#', h[r>>4], h[r&0xf], h[g>>4], h[g&0xf], h[b>>4], h[b&0xf]})
}

// --- Styles: quiet, dense, no chunky boxes on the tab strip ---

func styleBar() lipgloss.Style {
	return lipgloss.NewStyle().Background(colBar).Foreground(colDim)
}

// Active tab: soft pill, no rounded border (borders look broken at 1 cell).
func styleActiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colSel).
		Bold(true).
		Padding(0, 2)
}

func styleInactiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colBar).
		Padding(0, 2)
}

func stylePlus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute).
		Background(colBar).
		Padding(0, 1)
}

func styleGap() lipgloss.Style {
	return lipgloss.NewStyle().Background(colBar)
}

func styleStatus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colBar).
		Padding(0, 1)
}

// Crush dialog pattern (quickstyle.go):
//   View: RoundedBorder + BorderForeground(primary) + padding
//   Title: Padding(0,1) + primary
//   SelectedItem: Padding(0,1) + Background(primary) + Foreground(onPrimary)
//   NormalItem: Padding(0,1) + fgBase
//   Help: keys fgMoreSubtle, desc fgMostSubtle
//   defaultDialogMaxWidth = 70 (we clamp lower for host width)

// DialogMaxWidth is Crush's 70, clamped for suzuri's typical window.
const DialogMaxWidth = 56

func stylePaletteBorder() lipgloss.Style {
	// Crush: s.Dialog.View + Quit.Frame ≈ Border(Rounded)+BorderForeground(primary)+Padding(1,2)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent). // primary, not mute grey
		BorderBackground(colPanel).
		Background(colPanel).
		Padding(1, 2)
}

func styleBrand() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colAccent).
		Background(colBar).
		Bold(true).
		Padding(0, 1)
}

func styleDialogTitle() lipgloss.Style {
	// Crush: s.Dialog.Title = Padding(0,1).Foreground(primary)
	return lipgloss.NewStyle().
		Foreground(colAccent).
		Bold(true).
		Padding(0, 1)
}

func styleDialogLabel() lipgloss.Style {
	// meta / secondary
	return lipgloss.NewStyle().Foreground(colDim)
}

func styleDialogValue() lipgloss.Style {
	// fgBase body
	return lipgloss.NewStyle().Foreground(colText)
}

func styleDialogActive() lipgloss.Style {
	// Crush SelectedItem: primary fill + onPrimary text (full contrast)
	return lipgloss.NewStyle().
		Foreground(colVoid). // onPrimary stand-in (dark on accent)
		Background(colAccent).
		Bold(true).
		Padding(0, 1)
}

func styleDialogHint() lipgloss.Style {
	// Crush Help: ShortDesc = fgMostSubtle
	return lipgloss.NewStyle().Foreground(colMute)
}

func styleDialogHintKey() lipgloss.Style {
	// Crush Help: ShortKey = fgMoreSubtle (slightly stronger than desc)
	return lipgloss.NewStyle().Foreground(colDim)
}

func styleDialogRule() lipgloss.Style {
	// separator role
	return lipgloss.NewStyle().Foreground(colMute).Background(colPanel)
}

func styleDialogNormalItem() lipgloss.Style {
	// Crush NormalItem
	return lipgloss.NewStyle().
		Foreground(colText).
		Padding(0, 1)
}

// clampDialogWidth mirrors Crush min(max, window - chrome).
func clampDialogWidth(want, window int) int {
	if window < 20 {
		window = 20
	}
	maxW := DialogMaxWidth
	if window-4 < maxW {
		maxW = window - 4
	}
	if want > maxW {
		want = maxW
	}
	if want < 28 {
		want = min(28, maxW)
	}
	return want
}
