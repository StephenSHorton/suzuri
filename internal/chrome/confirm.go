package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// confirmState is a simple yes/no modal (last-tab quit, etc.).
type confirmState struct {
	title   string
	body    string
	yesLabel string
	noLabel  string
	// On yes → host ActionQuit (or other via pendingAction).
	yesAction HostAction
}

func (c confirmState) render(width int) string {
	if width < 28 {
		width = 28
	}
	title := styleDialogTitle().Render(c.title)
	body := styleDialogLabel().Width(width - 4).Render(c.body)
	yes := c.yesLabel
	if yes == "" {
		yes = "Yes"
	}
	no := c.noLabel
	if no == "" {
		no = "No"
	}
	// Highlight Enter = yes, Esc = no
	btns := styleDialogActive().Render("  "+yes+" · enter  ") + "  " +
		styleDialogHint().Render(no+" · esc")
	rows := []string{title, "", body, "", btns}
	return stylePaletteBorder().Width(width).Render(strings.Join(rows, "\n"))
}

var _ = lipgloss.NewStyle
