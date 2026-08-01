package chrome

// confirmState is a simple yes/no modal (Crush quit dialog pattern).
type confirmState struct {
	title     string
	body      string
	yesLabel  string
	noLabel   string
	yesAction HostAction
}

func (c confirmState) render(windowCols int) string {
	outer := clampDialogWidth(40, windowCols)
	yes := c.yesLabel
	if yes == "" {
		yes = "Yes"
	}
	no := c.noLabel
	if no == "" {
		no = "Cancel"
	}
	body := []string{
		styleDialogValue().Width(dialogInnerWidth(outer)).Render(c.body),
		"",
		styleDialogActive().Render(" "+yes+" ") +
			styleDialogHint().Render(" enter") +
			styleDialogHint().Render("   ") +
			styleDialogHintKey().Render(no) +
			styleDialogHint().Render(" esc"),
	}
	return renderDialogCard(outer, c.title, body, "")
}
