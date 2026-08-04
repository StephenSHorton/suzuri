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

// themeSpec is one full chrome + shell ANSI palette (hex strings).
// Order matches setPalette args; ansi is 16× hex for SGR 0–15.
// empty ansi + stockANSI uses StockANSI16 (optionally punched for HC).
type themeSpec struct {
	void, bar, panel       string
	primary, secondary     string
	onPrimary              string
	text, soft, dim, mute  string
	sel, match, cyan       string
	border, dimMatte       string
	ansi                   [16]string // empty → stock
	stockANSI              bool
	punchHC                bool // brighten a few stock slots (high contrast)
}

// themeCatalog is keyed by config.Theme* ids. Unknown ids fall back to inkstone.
var themeCatalog = map[string]themeSpec{
	config.ThemeInkstone: {
		void: "#0c0c10", bar: "#141418", panel: "#1c1c22",
		primary: "#b8a0c8", secondary: "#9a9ab0", onPrimary: "#0c0c10",
		text: "#e8e6e3", soft: "#b0aea8", dim: "#787870", mute: "#484848",
		sel: "#2a2430", match: "#c8b0d8", cyan: "#7a9a9a",
		border: "#b8a0c8", dimMatte: "#08080a",
		ansi: [16]string{
			"#0c0c10", "#c07070", "#70a078", "#c0a860",
			"#7090b8", "#a080a8", "#60a0a0", "#b8b6b0",
			"#585858", "#d88888", "#90c098", "#d8c880",
			"#90b0d0", "#c0a0c8", "#88c0c0", "#e8e6e3",
		},
	},
	config.ThemeCharmtone: {
		// Crush Pantera roles: primary≈charple, onPrimary dark on lavender.
		void: "#1a1418", bar: "#201a1e", panel: "#2a2228",
		primary: "#a78bfa", secondary: "#f0d9a8", onPrimary: "#1a1418",
		text: "#f3e8ee", soft: "#c9b8c0", dim: "#8a7a84", mute: "#5a4a54",
		sel: "#3a2e40", match: "#c4b5fd", cyan: "#7dd3c0",
		border: "#a78bfa", dimMatte: "#100c10",
		ansi: [16]string{
			"#1a1418", "#e8a0a8", "#a6d5a0", "#e8d5a0",
			"#b0a0e8", "#e0b0d0", "#90d0c8", "#d0c4cc",
			"#6a5a64", "#f0b0b8", "#b8e0b0", "#f0e0b8",
			"#c0b0f0", "#e8c0e0", "#a8e0d8", "#f8f0f4",
		},
	},
	config.ThemeHighContrast: {
		// Deep green fills (not neon glare); border stays bright for outlines.
		void: "#000000", bar: "#0a0a0a", panel: "#141414",
		primary: "#0a5c32", secondary: "#ffeb3b", onPrimary: "#e8fff0",
		text: "#ffffff", soft: "#e0e0e0", dim: "#b0b0b0", mute: "#707070",
		sel: "#064528", match: "#69f0ae", cyan: "#00e5ff",
		border: "#00e676", dimMatte: "#000000",
		stockANSI: true, punchHC: true,
	},
	config.ThemeNord: {
		void: "#2e3440", bar: "#3b4252", panel: "#434c5e",
		primary: "#88c0d0", secondary: "#ebcb8b", onPrimary: "#2e3440",
		text: "#eceff4", soft: "#d8dee9", dim: "#a3b1c6", mute: "#4c566a",
		sel: "#4c566a", match: "#8fbcbb", cyan: "#8fbcbb",
		border: "#88c0d0", dimMatte: "#242933",
		ansi: [16]string{
			"#3b4252", "#bf616a", "#a3be8c", "#ebcb8b",
			"#81a1c1", "#b48ead", "#88c0d0", "#e5e9f0",
			"#4c566a", "#bf616a", "#a3be8c", "#ebcb8b",
			"#81a1c1", "#b48ead", "#8fbcbb", "#eceff4",
		},
	},
	config.ThemeDracula: {
		void: "#21222c", bar: "#282a36", panel: "#343746",
		primary: "#bd93f9", secondary: "#ff79c6", onPrimary: "#21222c",
		text: "#f8f8f2", soft: "#e2e2dc", dim: "#a0a0a0", mute: "#6272a4",
		sel: "#44475a", match: "#ffb86c", cyan: "#8be9fd",
		border: "#bd93f9", dimMatte: "#191a21",
		ansi: [16]string{
			"#21222c", "#ff5555", "#50fa7b", "#f1fa8c",
			"#bd93f9", "#ff79c6", "#8be9fd", "#f8f8f2",
			"#6272a4", "#ff6e6e", "#69ff94", "#ffffa5",
			"#d6acff", "#ff92df", "#a4ffff", "#ffffff",
		},
	},
	config.ThemeTokyoNight: {
		void: "#1a1b26", bar: "#1f2335", panel: "#24283b",
		primary: "#7aa2f7", secondary: "#bb9af7", onPrimary: "#1a1b26",
		text: "#c0caf5", soft: "#a9b1d6", dim: "#565f89", mute: "#414868",
		sel: "#3b4261", match: "#7dcfff", cyan: "#7dcfff",
		border: "#7aa2f7", dimMatte: "#16161e",
		ansi: [16]string{
			"#15161e", "#f7768e", "#9ece6a", "#e0af68",
			"#7aa2f7", "#bb9af7", "#7dcfff", "#a9b1d6",
			"#414868", "#f7768e", "#9ece6a", "#e0af68",
			"#7aa2f7", "#bb9af7", "#7dcfff", "#c0caf5",
		},
	},
	config.ThemeCatppuccin: {
		// Mocha
		void: "#1e1e2e", bar: "#181825", panel: "#313244",
		primary: "#cba6f7", secondary: "#f9e2af", onPrimary: "#1e1e2e",
		text: "#cdd6f4", soft: "#bac2de", dim: "#6c7086", mute: "#45475a",
		sel: "#45475a", match: "#89b4fa", cyan: "#89dceb",
		border: "#cba6f7", dimMatte: "#11111b",
		ansi: [16]string{
			"#45475a", "#f38ba8", "#a6e3a1", "#f9e2af",
			"#89b4fa", "#f5c2e7", "#94e2d5", "#bac2de",
			"#585b70", "#f38ba8", "#a6e3a1", "#f9e2af",
			"#89b4fa", "#f5c2e7", "#94e2d5", "#cdd6f4",
		},
	},
	config.ThemeGruvbox: {
		void: "#1d2021", bar: "#282828", panel: "#3c3836",
		primary: "#d79921", secondary: "#fe8019", onPrimary: "#1d2021",
		text: "#ebdbb2", soft: "#d5c4a1", dim: "#a89984", mute: "#665c54",
		sel: "#504945", match: "#fabd2f", cyan: "#8ec07c",
		border: "#d79921", dimMatte: "#141617",
		ansi: [16]string{
			"#282828", "#cc241d", "#98971a", "#d79921",
			"#458588", "#b16286", "#689d6a", "#a89984",
			"#928374", "#fb4934", "#b8bb26", "#fabd2f",
			"#83a598", "#d3869b", "#8ec07c", "#ebdbb2",
		},
	},
	config.ThemeOneDark: {
		void: "#21252b", bar: "#282c34", panel: "#2c313a",
		primary: "#61afef", secondary: "#c678dd", onPrimary: "#21252b",
		text: "#abb2bf", soft: "#9da5b4", dim: "#5c6370", mute: "#3e4451",
		sel: "#3e4451", match: "#e5c07b", cyan: "#56b6c2",
		border: "#61afef", dimMatte: "#1b1f23",
		ansi: [16]string{
			"#282c34", "#e06c75", "#98c379", "#e5c07b",
			"#61afef", "#c678dd", "#56b6c2", "#abb2bf",
			"#5c6370", "#e06c75", "#98c379", "#e5c07b",
			"#61afef", "#c678dd", "#56b6c2", "#ffffff",
		},
	},
	config.ThemeSolarized: {
		void: "#002b36", bar: "#073642", panel: "#0a3a45",
		primary: "#268bd2", secondary: "#b58900", onPrimary: "#fdf6e3",
		text: "#839496", soft: "#93a1a1", dim: "#586e75", mute: "#073642",
		sel: "#073642", match: "#2aa198", cyan: "#2aa198",
		border: "#268bd2", dimMatte: "#001f27",
		ansi: [16]string{
			"#073642", "#dc322f", "#859900", "#b58900",
			"#268bd2", "#d33682", "#2aa198", "#eee8d5",
			"#002b36", "#cb4b16", "#586e75", "#657b83",
			"#839496", "#6c71c4", "#93a1a1", "#fdf6e3",
		},
	},
	config.ThemeRosePine: {
		void: "#191724", bar: "#1f1d2e", panel: "#26233a",
		primary: "#c4a7e7", secondary: "#ebbcba", onPrimary: "#191724",
		text: "#e0def4", soft: "#908caa", dim: "#6e6a86", mute: "#403d52",
		sel: "#403d52", match: "#9ccfd8", cyan: "#9ccfd8",
		border: "#c4a7e7", dimMatte: "#12101a",
		ansi: [16]string{
			"#26233a", "#eb6f92", "#31748f", "#f6c177",
			"#9ccfd8", "#c4a7e7", "#ebbcba", "#e0def4",
			"#6e6a86", "#eb6f92", "#31748f", "#f6c177",
			"#9ccfd8", "#c4a7e7", "#ebbcba", "#e0def4",
		},
	},
	config.ThemeKanagawa: {
		void: "#1f1f28", bar: "#2a2a37", panel: "#363646",
		primary: "#7e9cd8", secondary: "#e6c384", onPrimary: "#1f1f28",
		text: "#dcd7ba", soft: "#c8c093", dim: "#727169", mute: "#54546d",
		sel: "#2d4f67", match: "#7aa89f", cyan: "#7fb4ca",
		border: "#7e9cd8", dimMatte: "#16161d",
		ansi: [16]string{
			"#090618", "#c34043", "#76946a", "#c0a36e",
			"#7e9cd8", "#957fb8", "#6a9589", "#c8c093",
			"#727169", "#e82424", "#98bb6c", "#e6c384",
			"#7fb4ca", "#938aa9", "#7aa89f", "#dcd7ba",
		},
	},
	config.ThemeMonokai: {
		void: "#1e1f1c", bar: "#272822", panel: "#3e3d32",
		// onPrimary must be pure black: primary pink is a fill for active tabs /
		// settings selection — near-black inkstone tones wash out on #f92672.
		primary: "#f92672", secondary: "#e6db74", onPrimary: "#000000",
		text: "#f8f8f2", soft: "#cfcfc2", dim: "#75715e", mute: "#49483e",
		sel: "#49483e", match: "#a6e22e", cyan: "#66d9ef",
		border: "#f92672", dimMatte: "#141510",
		ansi: [16]string{
			"#272822", "#f92672", "#a6e22e", "#e6db74",
			"#66d9ef", "#ae81ff", "#a1efe4", "#f8f8f2",
			"#75715e", "#f92672", "#a6e22e", "#e6db74",
			"#66d9ef", "#ae81ff", "#a1efe4", "#f9f8f5",
		},
	},
	config.ThemeForest: {
		void: "#0d1410", bar: "#121c16", panel: "#1a2a20",
		primary: "#6abf8a", secondary: "#c4a35a", onPrimary: "#0d1410",
		text: "#dce8e0", soft: "#a8c0b0", dim: "#6a8878", mute: "#3a5044",
		sel: "#24382c", match: "#8fd4a8", cyan: "#5aa8a0",
		border: "#6abf8a", dimMatte: "#0a100c",
		ansi: [16]string{
			"#0d1410", "#c07070", "#6abf8a", "#c4a35a",
			"#5a90b0", "#a080a8", "#5aa8a0", "#b8c8bc",
			"#3a5044", "#d88888", "#8fd4a8", "#d8c080",
			"#80b0d0", "#c0a0c8", "#80c8c0", "#e8f0ea",
		},
	},
	config.ThemeOcean: {
		void: "#0a1218", bar: "#0e1a22", panel: "#152832",
		primary: "#4ec0e0", secondary: "#7ab0e0", onPrimary: "#0a1218",
		text: "#d8e8f0", soft: "#a0b8c8", dim: "#608098", mute: "#304858",
		sel: "#1c3848", match: "#6ad0f0", cyan: "#40d0c0",
		border: "#4ec0e0", dimMatte: "#070e14",
		ansi: [16]string{
			"#0a1218", "#e07080", "#60c090", "#e0c070",
			"#5090d0", "#b080d0", "#40d0c0", "#b0c8d8",
			"#304858", "#f090a0", "#80e0b0", "#f0d890",
			"#70b0f0", "#d0a0f0", "#60e8d8", "#e8f4f8",
		},
	},
	config.ThemeAmber: {
		// Phosphor CRT: amber on near-black.
		void: "#0c0a00", bar: "#141200", panel: "#1c1800",
		primary: "#ffb000", secondary: "#ffcc33", onPrimary: "#0c0a00",
		text: "#ffd070", soft: "#c8a040", dim: "#8a7020", mute: "#4a3c10",
		sel: "#3a3000", match: "#ffe080", cyan: "#e0b040",
		border: "#ffb000", dimMatte: "#080600",
		ansi: [16]string{
			"#0c0a00", "#cc4040", "#80a040", "#ffb000",
			"#8080c0", "#c08040", "#c0a040", "#c8a040",
			"#4a3c10", "#ff6060", "#a0c050", "#ffd040",
			"#a0a0e0", "#e0a050", "#e0c060", "#ffe8a0",
		},
	},
}

// ApplyTheme sets chrome colors, GDI bytes, and ShellANSI16.
func ApplyTheme(id string) {
	// Re-assert on every theme change (some lipgloss paths re-detect).
	lipgloss.SetColorProfile(termenv.TrueColor)
	spec, ok := themeCatalog[id]
	if !ok {
		spec = themeCatalog[config.ThemeInkstone]
	}
	setPalette(
		hex(spec.void), hex(spec.bar), hex(spec.panel),
		hex(spec.primary), hex(spec.secondary), hex(spec.onPrimary),
		hex(spec.text), hex(spec.soft), hex(spec.dim), hex(spec.mute),
		hex(spec.sel), hex(spec.match), hex(spec.cyan),
		hex(spec.border), hex(spec.dimMatte),
	)
	if spec.stockANSI {
		ShellANSI16 = StockANSI16
		if spec.punchHC {
			ShellANSI16[1] = [3]byte{255, 80, 80}
			ShellANSI16[2] = [3]byte{0, 230, 120}
			ShellANSI16[4] = [3]byte{80, 160, 255}
			ShellANSI16[15] = [3]byte{255, 255, 255}
		}
		return
	}
	var ansi [16][3]byte
	for i := 0; i < 16; i++ {
		if spec.ansi[i] == "" {
			ansi[i] = StockANSI16[i]
			continue
		}
		ansi[i] = rgbArr(hex(spec.ansi[i]))
	}
	ShellANSI16 = ansi
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

// styleCaffeineOff: dim empty-ish cup (sleep allowed).
func styleCaffeineOff() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute).
		Background(colBar).
		Padding(0, 1)
}

// styleCaffeineOn: full cup — warm secondary accent when awake.
func styleCaffeineOn() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSecondary).
		Background(colBar).
		Bold(true).
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
