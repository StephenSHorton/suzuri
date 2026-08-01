package chrome

import "strings"

// splashBody is the first-run welcome card.
func splashBody(width int) string {
	if width < 34 {
		width = 34
	}
	inner := width - 6
	title := styleDialogTitle().Render("硯  suzuri")
	sub := styleDialogHint().Render("real terminal · charm chrome")
	rule := styleDialogRule().Render(strings.Repeat("─", inner))
	body := styleDialogLabel().Render(
		"Ctrl+K   commands\n" +
			"Ctrl+,   settings\n" +
			"Ctrl+/   shortcuts\n" +
			"⌃⇧T     new tab",
	)
	hint := styleDialogHint().Render("enter to continue")
	return stylePaletteBorder().Width(width).Render(
		strings.Join([]string{title, sub, rule, body, "", hint}, "\n"),
	)
}
