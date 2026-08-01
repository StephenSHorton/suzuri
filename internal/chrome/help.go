package chrome

// helpBody is the keybind reference card (Crush help contrast roles).
// Key labels adapt to the active font via SetKeyGlyphSupport (fancy ⌃⇧ or ASCII).
func helpBody(windowCols int) string {
	outer := clampDialogWidth(48, windowCols)
	inner := dialogInnerWidth(outer)

	var body []string
	body = append(body, styleDialogHint().Render("Tabs"))
	body = append(body, helpRow(inner, KeyCtrlShift("T"), "New tab"))
	body = append(body, helpRow(inner, KeyCtrl("W"), "Close tab"))
	body = append(body, helpRow(inner, KeyCtrl("Tab"), "Next / prev"))
	body = append(body, helpRow(inner, KeyCtrl("1-9"), "Jump"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Chrome"))
	body = append(body, helpRow(inner, KeyCtrl("K"), "Palette"))
	body = append(body, helpRow(inner, KeyCtrl(","), "Settings"))
	body = append(body, helpRow(inner, KeyCtrl("/"), "Help"))
	body = append(body, helpRow(inner, "Esc", "Dismiss"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Command line"))
	body = append(body, helpRow(inner, "Enter", "Run command"))
	body = append(body, helpRow(inner, KeyShift("Enter"), "New line"))
	body = append(body, helpRow(inner, KeyUpDown(), "Line / history"))
	body = append(body, helpRow(inner, "Esc", "Clear line"))
	body = append(body, helpRow(inner, KeyCtrl("C"), "Clear / interrupt"))
	body = append(body, helpRow(inner, KeyCtrl("V"), "Paste into bar"))
	body = append(body, "")
	body = append(body, styleDialogHint().Render("Terminal"))
	body = append(body, helpRow(inner, KeyCtrlShift("C"), "Copy selection"))
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
	// Wider key column when using "Ctrl+Shift+…" ASCII forms.
	kw := 10
	if !keyFancyOn() {
		kw = 14
	}
	k := styleDialogHintKey().Width(kw).Render(key)
	d := styleDialogHint().Render(info)
	return styleDialogNormalItem().Width(inner).Render(k + "  " + d)
}
