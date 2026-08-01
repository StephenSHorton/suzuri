package chrome

import "github.com/charmbracelet/lipgloss"

// Charm marketing aesthetic: deep void panels with thin neon rounded borders
// (the look from charm.land / Glow / Crush screenshots — not Fluent/WT chrome).
var (
	colVoid  = lipgloss.Color("#0d0b14") // page / shell-adjacent
	colBar   = lipgloss.Color("#12101c") // tab strip surface
	colPanel = lipgloss.Color("#161422") // card fill
	colNeon  = lipgloss.Color("#ff6ac1") // hot pink neon (primary)
	colViolet = lipgloss.Color("#c792ea") // soft purple
	colCyan  = lipgloss.Color("#80ffea") // mint accent
	colText  = lipgloss.Color("#f8f8f2")
	colSoft  = lipgloss.Color("#cfc9e0")
	colDim   = lipgloss.Color("#6e6a86")
	colMute  = lipgloss.Color("#3e3a50")
	colSel   = lipgloss.Color("#2a1830") // palette selection (pink-tinted)
	colMatch = lipgloss.Color("#ff6ac1")
)

// BarRGB is the tab-strip fill for host GDI full-bleed (matches colBar).
const (
	BarR, BarG, BarB = 0x12, 0x10, 0x1c
	VoidR, VoidG, VoidB = 0x0d, 0x0b, 0x14
)

func styleBar() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colBar).
		Foreground(colDim)
}

// Active tab: thin neon-pink rounded card.
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

// Inactive tab: quiet violet outline, no fill shout.
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

// Palette: floating neon card — the classic Charm “glamorous panel”.
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
