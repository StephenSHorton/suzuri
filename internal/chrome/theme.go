package chrome

import "github.com/charmbracelet/lipgloss"

// Inkstone palette — deep sumi blacks with a soft indigo accent.
// Truecolor so the host paints the same hues as Lip Gloss intends.
var (
	colInk    = lipgloss.Color("#0f0f14") // deepest bg
	colPaper  = lipgloss.Color("#16161e") // tab bar
	colStone  = lipgloss.Color("#1c1c28") // status strip
	colActive = lipgloss.Color("#34344a") // active tab fill
	colAccent = lipgloss.Color("#8b9cf7") // indigo highlight
	colText   = lipgloss.Color("#e8e6e3") // primary ink
	colSoft   = lipgloss.Color("#a8a6b3") // secondary text
	colDim    = lipgloss.Color("#5c5c72") // hints / idle
	colMute   = lipgloss.Color("#3a3a4c") // separators
	colSel    = lipgloss.Color("#454563") // palette selection
	colMatch  = lipgloss.Color("#c4b5fd") // filter match
)

func styleBrand() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colAccent).
		Background(colPaper).
		Bold(true).
		Padding(0, 1)
}

func styleActiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colText).
		Background(colActive).
		Bold(true).
		Padding(0, 1)
}

func styleInactiveTab() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft).
		Background(colPaper).
		Padding(0, 1)
}

func styleTabBar() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(colPaper).
		Foreground(colDim)
}

func stylePlus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colPaper).
		Padding(0, 1)
}

func styleSep() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colMute).
		Background(colPaper)
}

func styleStatus() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colStone).
		Padding(0, 1)
}

func styleStatusKey() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colSoft).
		Background(colStone).
		Bold(true)
}

func styleStatusHint() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(colDim).
		Background(colStone)
}

func stylePaletteBorder() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colAccent).
		BorderBackground(colInk).
		Background(colInk).
		Padding(0, 1)
}
