package chrome

import (
	"image/color"

	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Active palette colors (mutated by ApplyTheme for live settings preview).
var (
	colVoid   lipgloss.Color
	colBar    lipgloss.Color
	colPanel  lipgloss.Color
	colNeon   lipgloss.Color
	colViolet lipgloss.Color
	colCyan   lipgloss.Color
	colText   lipgloss.Color
	colSoft   lipgloss.Color
	colDim    lipgloss.Color
	colMute   lipgloss.Color
	colSel    lipgloss.Color
	colMatch  lipgloss.Color
)

// BarRGB is the tab-strip fill for host GDI full-bleed (kept in sync by ApplyTheme).
var (
	BarR, BarG, BarB    byte
	VoidR, VoidG, VoidB byte
)

// ShellANSI16 is the active theme's 16-color remap for shell SGR 0–15
// (Crush-style on-brand palette). Host blends with stock colors per config.
var ShellANSI16 [16][3]byte

// StockANSI16 is a conventional VT palette (Windows Terminal–ish).
var StockANSI16 = [16][3]byte{
	{0, 0, 0},
	{205, 49, 49},
	{13, 188, 121},
	{229, 229, 16},
	{36, 114, 200},
	{188, 63, 188},
	{17, 168, 205},
	{204, 204, 204},
	{118, 118, 118},
	{241, 76, 76},
	{35, 209, 139},
	{245, 245, 67},
	{59, 142, 234},
	{214, 112, 214},
	{41, 184, 219},
	{229, 229, 229},
}

func init() {
	ApplyTheme(config.ThemeInkstone)
}

// ApplyTheme sets chrome Lip Gloss colors, GDI bar/void bytes, and ShellANSI16.
func ApplyTheme(id string) {
	switch id {
	case config.ThemeCharmtone:
		// Crush-adjacent: deep pepper / charple / dolly-ish.
		setPalette(
			hex("#1b1216"), // void
			hex("#24181f"), // bar
			hex("#2a1e28"), // panel
			hex("#a78bfa"), // neon ≈ charple
			hex("#f9e2af"), // violet → dolly gold/cream for secondary
			hex("#89dceb"), // cyan
			hex("#f5e0dc"), // text
			hex("#c9b8c4"),
			hex("#7a6a74"),
			hex("#4a3a44"),
			hex("#3d2a48"),
			hex("#cba6f7"),
		)
		// Crush Pantera-ish ANSI remap.
		ShellANSI16 = [16][3]byte{
			rgbArr(hex("#1a1216")), // black ≈ BBQ
			rgbArr(hex("#f38ba8")), // red coral
			rgbArr(hex("#a6e3a1")), // green guac
			rgbArr(hex("#f9e2af")), // yellow mustard
			rgbArr(hex("#a78bfa")), // blue charple
			rgbArr(hex("#f5c2e7")), // magenta dolly
			rgbArr(hex("#89dceb")), // cyan malibu
			rgbArr(hex("#c9b8c4")), // white smoke
			rgbArr(hex("#6c5a66")), // bright black
			rgbArr(hex("#eba0ac")), // bright red
			rgbArr(hex("#94e2d5")), // bright green
			rgbArr(hex("#f5e0dc")), // bright yellow
			rgbArr(hex("#b4befe")), // bright blue
			rgbArr(hex("#cba6f7")), // bright magenta
			rgbArr(hex("#89dceb")), // bright cyan
			rgbArr(hex("#f5e0dc")), // bright white
		}
	case config.ThemeHighContrast:
		setPalette(
			hex("#000000"),
			hex("#111111"),
			hex("#1a1a1a"),
			hex("#00ff88"),
			hex("#ffff00"),
			hex("#00ffff"),
			hex("#ffffff"),
			hex("#dddddd"),
			hex("#aaaaaa"),
			hex("#666666"),
			hex("#003322"),
			hex("#00ff88"),
		)
		ShellANSI16 = [16][3]byte{
			{0, 0, 0},
			{255, 80, 80},
			{0, 255, 120},
			{255, 255, 0},
			{80, 160, 255},
			{255, 80, 255},
			{0, 255, 255},
			{255, 255, 255},
			{120, 120, 120},
			{255, 120, 120},
			{80, 255, 160},
			{255, 255, 120},
			{120, 200, 255},
			{255, 120, 255},
			{120, 255, 255},
			{255, 255, 255},
		}
	default: // inkstone
		setPalette(
			hex("#0d0b14"),
			hex("#12101c"),
			hex("#161422"),
			hex("#ff6ac1"),
			hex("#c792ea"),
			hex("#80ffea"),
			hex("#f8f8f2"),
			hex("#cfc9e0"),
			hex("#6e6a86"),
			hex("#3e3a50"),
			hex("#2a1830"),
			hex("#ff6ac1"),
		)
		ShellANSI16 = [16][3]byte{
			rgbArr(hex("#0d0b14")),
			rgbArr(hex("#ff6b6b")),
			rgbArr(hex("#69db7c")),
			rgbArr(hex("#ffd43b")),
			rgbArr(hex("#74c0fc")),
			rgbArr(hex("#ff6ac1")),
			rgbArr(hex("#80ffea")),
			rgbArr(hex("#cfc9e0")),
			rgbArr(hex("#5c5c72")),
			rgbArr(hex("#ff8787")),
			rgbArr(hex("#8ce99a")),
			rgbArr(hex("#ffe066")),
			rgbArr(hex("#91a7ff")),
			rgbArr(hex("#e599f7")),
			rgbArr(hex("#99e9f2")),
			rgbArr(hex("#f8f8f2")),
		}
	}
}

// RemapANSI16 returns an effective 16-color table for shell paint.
// mode: none → stock; full → theme table; soft → 55% theme + 45% stock.
func RemapANSI16(mode string) [16][3]byte {
	switch mode {
	case config.ANSIMapNone:
		return StockANSI16
	case config.ANSIMapFull:
		return ShellANSI16
	default: // soft
		var out [16][3]byte
		for i := 0; i < 16; i++ {
			for c := 0; c < 3; c++ {
				// weighted average
				out[i][c] = byte((int(ShellANSI16[i][c])*55 + int(StockANSI16[i][c])*45) / 100)
			}
		}
		return out
	}
}

func setPalette(void, bar, panel, neon, violet, cyan, text, soft, dim, mute, sel, match color.Color) {
	colVoid = toLG(void)
	colBar = toLG(bar)
	colPanel = toLG(panel)
	colNeon = toLG(neon)
	colViolet = toLG(violet)
	colCyan = toLG(cyan)
	colText = toLG(text)
	colSoft = toLG(soft)
	colDim = toLG(dim)
	colMute = toLG(mute)
	colSel = toLG(sel)
	colMatch = toLG(match)

	BarR, BarG, BarB = rgb8(bar)
	VoidR, VoidG, VoidB = rgb8(void)
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
	// #RRGGBB
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{R: 0, G: 0, B: 0, A: 255}
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
	return string([]byte{
		'#',
		h[r>>4], h[r&0xf],
		h[g>>4], h[g&0xf],
		h[b>>4], h[b&0xf],
	})
}

func styleBar() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colBar).
		Foreground(colDim)
}

func styleActiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colPanel).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colNeon).
		BorderBackground(colBar)
}

func styleInactiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colBar).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colMute).
		BorderBackground(colBar)
}

func stylePlus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colCyan).
		Background(colBar).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colMute).
		BorderBackground(colBar)
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

func stylePaletteBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colNeon).
		BorderBackground(colVoid).
		Background(colPanel).
		Padding(0, 1).
		Margin(0, 0)
}

func styleBrand() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colViolet).
		Background(colBar).
		Bold(true).
		Padding(0, 1)
}

func styleDialogTitle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colNeon).
		Bold(true).
		Padding(0, 1)
}

func styleDialogLabel() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colDim)
}

func styleDialogValue() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colText).Bold(true)
}

func styleDialogActive() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colSel).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colNeon)
}

func styleDialogHint() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colMute)
}
