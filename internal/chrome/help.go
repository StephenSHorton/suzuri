package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpBody is the keybind reference card (Crush help contrast roles).
// Key labels adapt to the active font via SetKeyGlyphSupport (fancy ⌃⇧ or ASCII).
func helpBody(windowCols int) string {
	outer := clampDialogWidth(48, windowCols)
	inner := dialogInnerWidth(outer)

	var body []string
	body = append(body, helpSection(inner, "Tabs"))
	body = append(body, helpRow(inner, KeyCtrlShift("T"), "New tab"))
	body = append(body, helpRow(inner, KeyCtrlShift("N"), "New window"))
	body = append(body, helpRow(inner, KeyCtrl("W"), "Close tab"))
	body = append(body, helpRow(inner, KeyCtrl("Tab"), "Next / prev"))
	body = append(body, helpRow(inner, KeyCtrl("1-9"), "Jump"))
	body = append(body, helpRow(inner, "Rename tab", "Palette · double-click tab"))
	body = append(body, "")
	body = append(body, helpSection(inner, "Panes"))
	body = append(body, helpRow(inner, KeyCtrlShift("D"), "Split right"))
	body = append(body, helpRow(inner, KeyCtrlShift("E"), "Split down"))
	body = append(body, helpRow(inner, "Alt+arrows", "Focus pane"))
	body = append(body, helpRow(inner, "Ctrl+Alt+arrows", "Focus pane (same)"))
	body = append(body, helpRow(inner, "F2", "Rename pane"))
	body = append(body, helpRow(inner, "Double-click", "Rename tab / pane title"))
	body = append(body, helpRow(inner, KeyCtrlShift("W"), "Close pane"))
	body = append(body, "")
	body = append(body, helpSection(inner, "Chrome"))
	body = append(body, helpRow(inner, KeyCtrl("K"), "Palette (commands, updates, intro)"))
	body = append(body, helpRow(inner, KeyCtrl(","), "Settings"))
	body = append(body, helpRow(inner, KeyCtrl("/"), "Help"))
	body = append(body, helpRow(inner, "Esc", "Dismiss"))
	body = append(body, "")
	body = append(body, helpSection(inner, "Command line"))
	body = append(body, helpRow(inner, "Enter", "Run command"))
	body = append(body, helpRow(inner, KeyShift("Enter"), "New line"))
	body = append(body, helpRow(inner, KeyUpDown(), "Line / history"))
	body = append(body, helpRow(inner, "Esc", "Clear line"))
	body = append(body, helpRow(inner, KeyCtrl("C"), "Clear / interrupt"))
	body = append(body, helpRow(inner, KeyCtrl("V"), "Paste into bar"))
	body = append(body, "")
	body = append(body, helpSection(inner, "Terminal"))
	body = append(body, helpRow(inner, KeyCtrlShift("C"), "Copy selection"))
	body = append(body, helpRow(inner, "Wheel", "Scrollback"))

	// Footer: keep gap styled with panel bg (unstyled " " punches holes in VT paint).
	footer := styleDialogHintKey().Render("esc") + styleDialogHint().Render(" close")
	return renderDialogCard(outer, "Shortcuts", body, footer)
}

func helpSection(inner int, title string) string {
	// Full-width panel fill so section headers don't leave void gutters.
	return styleDialogHint().Width(inner).MaxHeight(1).Render(padFit(title, inner))
}

// helpRow is one key | description line.
// Built like settingsRow: fixed columns, then style segments so *every* cell
// (including the gap before the description) carries panel background.
// Unstyled "  " between separately rendered SGR runs used to show as breaks
// over the dim 猫咪 underlay.
func helpRow(inner int, key, desc string) string {
	// Crush: ShortKey = fgMoreSubtle, ShortDesc = fgMostSubtle
	info := hideInfoIfCrowded(desc, inner, commandInfoMaxPercent)
	if info == "" {
		info = desc
	}
	// Wider key column when using "Ctrl+Shift+…" ASCII forms.
	kw := 10
	if !keyFancyOn() {
		kw = 14
	}
	const gapW = 2
	if kw+gapW >= inner {
		kw = inner - gapW - 1
		if kw < 4 {
			kw = 4
		}
	}
	descW := inner - kw - gapW
	if descW < 1 {
		descW = 1
	}
	keyCol := padFit(key, kw)
	descCol := padFit(info, descW)
	// Gap is part of the key style run — continuous panel bg into the desc.
	k := styleDialogHintKey().Render(keyCol + strings.Repeat(" ", gapW))
	d := styleDialogHint().Render(descCol)
	row := k + d
	// Pad any remaining width with panel-styled spaces (safety).
	rw := lipgloss.Width(row)
	if rw < inner {
		row += styleDialogHint().Render(strings.Repeat(" ", inner-rw))
	}
	return row
}
