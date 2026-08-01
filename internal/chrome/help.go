package chrome

import "strings"

// helpBody is the keybind reference card.
func helpBody(width int) string {
	if width < 32 {
		width = 32
	}
	inner := width - 6
	title := styleDialogTitle().Render("Shortcuts")
	rule := styleDialogRule().Render(strings.Repeat("─", inner))
	lines := []string{
		title,
		rule,
		styleDialogHint().Render("Tabs"),
		helpRow("⌃⇧T", "New tab"),
		helpRow("⌃W", "Close tab"),
		helpRow("⌃Tab", "Next / prev"),
		helpRow("⌃1–9", "Jump"),
		"",
		styleDialogHint().Render("Chrome"),
		helpRow("⌃K", "Palette"),
		helpRow("⌃,", "Settings"),
		helpRow("⌃/", "Help"),
		helpRow("Esc", "Dismiss"),
		"",
		styleDialogHint().Render("Terminal"),
		helpRow("⌃⇧C/V", "Copy / paste"),
		helpRow("Wheel", "Scrollback"),
		rule,
		styleDialogHintKey().Render("esc") + styleDialogHint().Render(" close"),
	}
	return stylePaletteBorder().Width(clampDialogWidth(width, width+4)).Render(strings.Join(lines, "\n"))
}

func helpRow(key, desc string) string {
	// Crush Help: ShortKey = fgMoreSubtle, ShortDesc = fgMostSubtle
	k := styleDialogHintKey().Width(8).Render(key)
	d := styleDialogHint().Render(desc)
	return k + "  " + d
}
