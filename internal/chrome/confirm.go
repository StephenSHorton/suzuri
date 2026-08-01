package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// confirmState is a simple yes/no modal.
type confirmState struct {
	title    string
	body     string
	yesLabel string
	noLabel  string
	yesAction HostAction
}

func (c confirmState) render(width int) string {
	if width < 28 {
		width = 28
	}
	inner := width - 6
	title := styleDialogTitle().Render(c.title)
	rule := styleDialogRule().Render(strings.Repeat("─", inner))
	body := styleDialogLabel().Width(inner).Render(c.body)
	yes := c.yesLabel
	if yes == "" {
		yes = "Yes"
	}
	no := c.noLabel
	if no == "" {
		no = "Cancel"
	}
	btns := styleDialogActive().Render(" "+yes+" ") +
		styleDialogHint().Render("  enter") +
		styleDialogHint().Render("   "+no+"  esc")
	return stylePaletteBorder().Width(width).Render(
		strings.Join([]string{title, rule, body, "", btns}, "\n"),
	)
}

var _ = lipgloss.NewStyle
