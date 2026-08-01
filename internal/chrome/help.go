package chrome

// helpBody is the keybind reference card (Crush help contrast roles).
func helpBody(windowCols int) string {
	outer := clampDialogWidth(48, windowCols)
	inner := dialogInnerWidth(outer)

	var body []string
	body = append(body, styleDialogHint().Render("Tabs"))
	body = append(body, helpRow(inner, "⌃⇧T", "New tab"))
	body = append(body, helpRow(inner, "⌃W", "Close tab"))
	body = append(body, helpRow(inner, "⌃Tab", "Next / prev"))
	body = append(body, helpRow(inner, "⌃1–9", "Jump"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Chrome"))
	body = append(body, helpRow(inner, "⌃K", "Palette"))
	body = append(body, helpRow(inner, "⌃,", "Settings"))
	body = append(body, helpRow(inner, "⌃/", "Help"))
	body = append(body, helpRow(inner, "Esc", "Dismiss"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Command line"))
	body = append(body, helpRow(inner, "Enter", "Run command"))
	body = append(body, helpRow(inner, "⇧Enter", "New line"))
	body = append(body, helpRow(inner, "↑ / ↓", "Line / history"))
	body = append(body, helpRow(inner, "Esc", "Clear line"))
	body = append(body, helpRow(inner, "⌃C", "Clear / interrupt"))
	body = append(body, helpRow(inner, "⌃V", "Paste into bar"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Terminal"))
	body = append(body, helpRow(inner, "⌃⇧C", "Copy selection"))
	body = append(body, helpRow(inner, "Wheel", "Scrollback"))

	footer := styleDialogHintKey().Render("esc") + styleDialogHint().Render(" close")
	return renderDialogCard(outer, "Shortcuts", body, footer)
}

func helpRow(inner int, key, desc string) string {
	// Crush: ShortKey = fgMoreSubtle, ShortDesc = fgMostSubtle; Padding via spacing
	info := hideInfoIfCrowded(desc, inner, commandInfoMaxPercent)
	if info == "" {
		info = desc
	}
	k := styleDialogHintKey().Width(8).Render(key)
	d := styleDialogHint().Render(info)
	return styleDialogNormalItem().Width(inner).Render(k + "  " + d)
}
