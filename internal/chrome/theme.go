package chrome

import (
	"image/color"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Active palette — Crush quickStyleOpts roles (mutated by ApplyTheme).
var (
	colVoid      lipgloss.Color // bgBase-adjacent / dim
	colBar       lipgloss.Color // chrome strip surface
	colPanel     lipgloss.Color // dialog fill (bgLessVisible-ish)
	colPrimary   lipgloss.Color // Crush primary — borders, titles, selection bg
	colSecondary lipgloss.Color // Crush secondary
	colOnPrimary lipgloss.Color // text ON primary fills (contrast pair)
	colText      lipgloss.Color // fgBase
	colSoft      lipgloss.Color // fgMoreSubtle
	colDim       lipgloss.Color // between soft and mute
	colMute      lipgloss.Color // fgMostSubtle
	colSel       lipgloss.Color // softer selection (tabs)
	colMatch     lipgloss.Color // filter match
	colCyan      lipgloss.Color // info/success-ish accent
	colBorder    lipgloss.Color // usually = primary for dialogs
)

// Aliases for older call sites.
var (
	colAccent lipgloss.Color
	colNeon   lipgloss.Color
	colViolet lipgloss.Color
	colSoftA  lipgloss.Color
)

// Bar / void for GDI.
var (
	BarR, BarG, BarB       byte
	VoidR, VoidG, VoidB    byte
	DimR, DimG, DimB       byte // shell dim matte
	PanelR, PanelG, PanelB byte // input bar / panel surface
	PrimR, PrimG, PrimB       byte // primary accent (prompt glyph, border)
	OnPrimR, OnPrimG, OnPrimB byte // text on primary fills (selection / block caret)
	TextR, TextG, TextB       byte // primary fg
	SoftR, SoftG, SoftB       byte // muted fg (hints)
	MuteR, MuteG, MuteB       byte // most subtle fg (ghost completion, etc.)
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
	// Host paints Lip Gloss → mini VT (no real TTY). Without an explicit
	// truecolor profile, lipgloss downgrades to monochrome and every tab /
	// dialog loses its theme fill (active tab, settings selection, etc.).
	lipgloss.SetColorProfile(termenv.TrueColor)
	ApplyTheme(config.ThemeHighContrast)
}

// ApplyTheme sets chrome colors, GDI bytes, and ShellANSI16.
func ApplyTheme(id string) {
	// Re-assert on every theme change (some lipgloss paths re-detect).
	lipgloss.SetColorProfile(termenv.TrueColor)
	switch id {
	case config.ThemeCharmtone:
		// Crush Pantera roles: primary≈charple, onPrimary≈butter, bg pepper.
		setPalette(
			hex("#1a1418"), // void / dim base
			hex("#201a1e"), // bar
			hex("#2a2228"), // panel
			hex("#a78bfa"), // primary
			hex("#f0d9a8"), // secondary
			hex("#1a1418"), // onPrimary (dark on lavender)
			hex("#f3e8ee"), // fgBase
			hex("#c9b8c0"), // fgMoreSubtle
			hex("#8a7a84"), // dim
			hex("#5a4a54"), // fgMostSubtle
			hex("#3a2e40"), // tab sel
			hex("#c4b5fd"), // match
			hex("#7dd3c0"), // cyan/info
			hex("#a78bfa"), // border = primary
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
		// Primary is the fill for active tabs + settings selection — keep it a
		// deep green so it doesn't glare. Border stays neon for outlines.
		setPalette(
			hex("#000000"),
			hex("#0a0a0a"),
			hex("#141414"),
			hex("#0a5c32"), // primary — dark green fills (was neon #00e676)
			hex("#ffeb3b"), // secondary
			hex("#e8fff0"), // onPrimary — light text on dark green fills
			hex("#ffffff"),
			hex("#e0e0e0"),
			hex("#b0b0b0"),
			hex("#707070"),
			hex("#064528"), // tab sel (even deeper)
			hex("#69f0ae"),
			hex("#00e5ff"),
			hex("#00e676"), // border — bright outline / accents
			hex("#000000"),
		)
		ShellANSI16 = StockANSI16
		// Punch up a few for HC.
		ShellANSI16[1] = [3]byte{255, 80, 80}
		ShellANSI16[2] = [3]byte{0, 230, 120}
		ShellANSI16[4] = [3]byte{80, 160, 255}
		ShellANSI16[15] = [3]byte{255, 255, 255}
	default: // inkstone — primary mauve, onPrimary dark ink
		setPalette(
			hex("#0c0c10"),
			hex("#141418"),
			hex("#1c1c22"),
			hex("#b8a0c8"), // primary
			hex("#9a9ab0"), // secondary
			hex("#0c0c10"), // onPrimary
			hex("#e8e6e3"), // fgBase
			hex("#b0aea8"), // more subtle
			hex("#787870"),
			hex("#484848"), // most subtle
			hex("#2a2430"), // soft tab sel
			hex("#c8b0d8"),
			hex("#7a9a9a"), // cyan
			hex("#b8a0c8"), // border = primary (Crush rule)
			hex("#08080a"),
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

func setPalette(void, bar, panel, primary, secondary, onPrimary, text, soft, dim, mute, sel, match, cyan, border, dimMatte color.Color) {
	colVoid = toLG(void)
	colBar = toLG(bar)
	colPanel = toLG(panel)
	colPrimary = toLG(primary)
	colSecondary = toLG(secondary)
	colOnPrimary = toLG(onPrimary)
	colText = toLG(text)
	colSoft = toLG(soft)
	colDim = toLG(dim)
	colMute = toLG(mute)
	colSel = toLG(sel)
	colMatch = toLG(match)
	colCyan = toLG(cyan)
	colBorder = toLG(border)

	colAccent = colPrimary
	colNeon = colPrimary
	colViolet = colSecondary
	colSoftA = colSecondary

	BarR, BarG, BarB = rgb8(bar)
	VoidR, VoidG, VoidB = rgb8(void)
	DimR, DimG, DimB = rgb8(dimMatte)
	PanelR, PanelG, PanelB = rgb8(panel)
	// GDI accents (prompt, hairlines) follow border so HC can keep neon
	// outlines while selection fills use a darker primary.
	PrimR, PrimG, PrimB = rgb8(border)
	OnPrimR, OnPrimG, OnPrimB = rgb8(onPrimary)
	TextR, TextG, TextB = rgb8(text)
	SoftR, SoftG, SoftB = rgb8(soft)
	MuteR, MuteG, MuteB = rgb8(mute)
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

// --- Styles (Crush quickstyle roles) ---

func styleBar() lipgloss.Style {
	return lipgloss.NewStyle().Background(colBar).Foreground(colDim)
}

// Active tab: treat like Crush selected — primary fill + onPrimary.
func styleActiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colOnPrimary).
		Background(colPrimary).
		Bold(true).
		Padding(0, 2)
}

// Inactive: fgMoreSubtle on bar (Crush inactive tab text).
func styleInactiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft).
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
	// Use soft (not dim) so toasts stay readable on high-contrast / dark bars.
	return lipgloss.NewStyle().
		Foreground(colSoft).
		Background(colBar).
		Padding(0, 1)
}

// styleDialogView = Crush s.Dialog.View + Quit.Frame padding.
func styleDialogView() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		BorderBackground(colPanel).
		Background(colPanel).
		Padding(1, 2)
}

// stylePaletteBorder is an alias for dialog view (palette is a dialog).
func stylePaletteBorder() lipgloss.Style {
	return styleDialogView()
}

func styleBrand() lipgloss.Style {
	// Tight around 硯 — no side padding (tabs keep a single gap after the mark).
	// Border color keeps brand bright on HC while selection fills stay dark green.
	return lipgloss.NewStyle().
		Foreground(colBorder).
		Background(colBar).
		Bold(true).
		Padding(0, 0)
}

func styleDialogTitle() lipgloss.Style {
	// Crush: Padding(0,1).Foreground(primary) — panel bg so width-fill is seamless.
	// Use border so HC titles stay neon while primary is the dark selection fill.
	return lipgloss.NewStyle().
		Foreground(colBorder).
		Background(colPanel).
		Bold(true).
		Padding(0, 1)
}

func styleDialogLabel() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft). // fgMoreSubtle
		Background(colPanel)
}

func styleDialogValue() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colText). // fgBase
		Background(colPanel)
}

func styleDialogActive() lipgloss.Style {
	// Crush SelectedItem: Background(primary).Foreground(onPrimary).Padding(0,1)
	return lipgloss.NewStyle().
		Foreground(colOnPrimary).
		Background(colPrimary).
		Bold(true).
		Padding(0, 1)
}

func styleDialogHint() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute). // fgMostSubtle
		Background(colPanel)
}

// Settings help caption under the modal — panel fill, no border (not interactive).
func styleSettingsHelpPanel() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colPanel).
		Foreground(colMute).
		Padding(1, 2)
}

func styleSettingsHelpTitle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft).
		Background(colPanel).
		Bold(true)
}

func styleSettingsHelpBody() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute).
		Background(colPanel)
}

func styleDialogHintKey() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft). // fgMoreSubtle
		Background(colPanel)
}

func styleDialogRule() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(colMute).Background(colPanel)
}

func styleDialogNormalItem() lipgloss.Style {
	// Crush NormalItem: Padding(0,1).Foreground(fgBase)
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colPanel).
		Padding(0, 1)
}
