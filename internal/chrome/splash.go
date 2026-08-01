package chrome

import "strings"

// splashBody is the first-run welcome card.
func splashBody(width int) string {
	if width < 32 {
		width = 32
	}
	title := styleDialogTitle().Render("硯  suzuri")
	body := styleDialogLabel().Render(
		"A real Windows terminal host —\n" +
			"ConPTY shell, Charm chrome.\n\n" +
			"Ctrl+K  command palette\n" +
			"Ctrl+,  settings\n" +
			"Ctrl+/  keyboard shortcuts\n" +
			"Ctrl+Shift+T  new tab",
	)
	hint := styleDialogHint().Render("enter or esc · don't show again")
	return stylePaletteBorder().Width(width).Render(
		strings.Join([]string{title, "", body, "", hint}, "\n"),
	)
}
