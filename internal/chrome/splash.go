package chrome

// splashBody is the first-run welcome card.
func splashBody(windowCols int) string {
	outer := clampDialogWidth(42, windowCols)
	body := []string{
		styleDialogHint().Render("real terminal · charm chrome"),
		"",
		styleDialogValue().Render("Ctrl+K") + styleDialogLabel().Render("  commands"),
		styleDialogValue().Render("Ctrl+,") + styleDialogLabel().Render("  settings"),
		styleDialogValue().Render("Ctrl+/") + styleDialogLabel().Render("  shortcuts"),
		styleDialogValue().Render("⌃⇧T") + styleDialogLabel().Render("    new tab"),
	}
	footer := styleDialogHintKey().Render("enter") + styleDialogHint().Render(" continue")
	return renderDialogCard(outer, "硯  suzuri", body, footer)
}
