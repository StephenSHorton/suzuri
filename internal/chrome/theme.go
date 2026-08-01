package chrome

import "github.com/charmbracelet/lipgloss"

// Windows Terminal–inspired dark chrome, still Charm/Lip Gloss.
// Active tab uses the same near-black as the shell so it “opens” into content.
var (
	colShell  = lipgloss.Color("#0c0c0c") // matches shell default bg
	colBar    = lipgloss.Color("#1f1f1f") // tab strip (slightly raised)
	colHover  = lipgloss.Color("#2b2b2b") // reserved / inactive chip
	colInk    = lipgloss.Color("#0c0c0c") // palette panel
	colAccent = lipgloss.Color("#60cdff") // Fluent accent (WT-like)
	colText   = lipgloss.Color("#f3f3f3")
	colSoft   = lipgloss.Color("#c5c5c5")
	colDim    = lipgloss.Color("#8a8a8a")
	colMute   = lipgloss.Color("#5a5a5a")
	colSel    = lipgloss.Color("#1e3a4c") // palette selection (cool tint)
	colMatch  = lipgloss.Color("#60cdff")
	colRule   = lipgloss.Color("#2a2a2a") // hairline under strip
)

func styleBar() lipgloss.Style {
	return lipgloss.NewStyle().Background(colBar).Foreground(colDim)
}

func styleActiveTab() lipgloss.Style {
	// Same bg as shell → tab feels attached to the viewport.
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colShell).
		Bold(true).
		Padding(0, 2)
}

func styleInactiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colBar).
		Padding(0, 2)
}

func styleGap() lipgloss.Style {
	return lipgloss.NewStyle().Background(colBar)
}

func stylePlus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute).
		Background(colBar).
		Padding(0, 1)
}

func styleRule() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colRule).
		Background(colBar)
}

func styleRuleActive() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colAccent).
		Background(colShell)
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
		BorderForeground(colAccent).
		BorderBackground(colInk).
		Background(colInk).
		Padding(0, 1)
}
