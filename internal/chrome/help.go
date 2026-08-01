package chrome

import "strings"

// helpBody is the keybind reference card.
func helpBody(width int) string {
	if width < 28 {
		width = 28
	}
	title := styleDialogTitle().Render("Keyboard shortcuts")
	lines := []string{
		title,
		"",
		styleDialogLabel().Render("Tabs"),
		helpRow("Ctrl+Shift+T", "New tab"),
		helpRow("Ctrl+W", "Close tab"),
		helpRow("Ctrl+Tab", "Next tab"),
		helpRow("Ctrl+Shift+Tab", "Previous tab"),
		helpRow("Ctrl+1…9", "Jump to tab"),
		helpRow("Click +", "New tab"),
		"",
		styleDialogLabel().Render("Chrome"),
		helpRow("Ctrl+K / Ctrl+P", "Command palette"),
		helpRow("Ctrl+,", "Settings"),
		helpRow("Ctrl+/", "This help"),
		helpRow("Esc", "Dismiss overlay"),
		"",
		styleDialogLabel().Render("Terminal"),
		helpRow("Ctrl+Shift+C", "Copy"),
		helpRow("Ctrl+Shift+V", "Paste"),
		helpRow("Wheel / PgUp·Dn", "Scrollback"),
		"",
		styleDialogHint().Render("esc close"),
	}
	return stylePaletteBorder().Width(width).Render(strings.Join(lines, "\n"))
}

func helpRow(key, desc string) string {
	k := styleDialogValue().Render(key)
	d := styleDialogLabel().Render("  " + desc)
	return k + d
}
