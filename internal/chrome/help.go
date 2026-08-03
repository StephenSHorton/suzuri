package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpBody is the keybind reference card (Crush help contrast roles).
// Wide windows use two columns so the list fits without vertical overflow.
// Key labels adapt to the active font via SetKeyGlyphSupport (fancy ⌃⇧ or ASCII).
func helpBody(windowCols int) string {
	// Two columns need ~60+ terminal cols; narrower → single stack.
	if windowCols >= 60 {
		return helpBodyTwoCol(windowCols)
	}
	return helpBodyOneCol(windowCols)
}

func helpBodyOneCol(windowCols int) string {
	outer := clampDialogWidth(48, windowCols)
	inner := dialogInnerWidth(outer)
	body := helpJoinSections(inner, helpAllSections(inner)...)
	footer := styleDialogHintKey().Render("esc") + styleDialogHint().Render(" close")
	return renderDialogCard(outer, "Shortcuts", body, footer)
}

func helpBodyTwoCol(windowCols int) string {
	// Prefer full Crush max so each column keeps readable key + desc.
	outer := clampDialogWidth(70, windowCols)
	inner := dialogInnerWidth(outer)
	const gapW = 2
	colW := (inner - gapW) / 2
	if colW < 22 {
		// Too tight even when windowCols was ok — fall back.
		return helpBodyOneCol(windowCols)
	}
	// Give remainder to the right column.
	leftW := colW
	rightW := inner - gapW - leftW

	// Compact two-col (no blank between sections) so the card fits ~24–28 rows.
	leftSecs := helpSectionsLeft(leftW)
	rightSecs := helpSectionsRight(rightW)
	left := helpJoinSectionsEx(leftW, true, leftSecs...)
	right := helpJoinSectionsEx(rightW, true, rightSecs...)

	// Pad shorter column so JoinHorizontal rows align.
	lh, rh := len(left), len(right)
	for lh < rh {
		left = append(left, panelFillLine(leftW, ""))
		lh++
	}
	for rh < lh {
		right = append(right, panelFillLine(rightW, ""))
		rh++
	}

	gap := styleDialogHint().Render(strings.Repeat(" ", gapW))
	var body []string
	for i := 0; i < lh; i++ {
		row := left[i] + gap + right[i]
		// Ensure full inner width (panel bg continuity).
		rw := lipgloss.Width(row)
		if rw < inner {
			row += styleDialogHint().Render(strings.Repeat(" ", inner-rw))
		}
		body = append(body, row)
	}

	footer := styleDialogHintKey().Render("esc") + styleDialogHint().Render(" close")
	// Compact vertical padding so two-col fits typical shell heights.
	return renderHelpCard(outer, "Shortcuts", body, footer)
}

// renderHelpCard is renderDialogCard with less vertical padding (0,2 vs 1,2)
// so the two-column shortcuts sheet fits ~24-row shells.
func renderHelpCard(outerWidth int, title string, body []string, footer string) string {
	outerWidth = clampDialogWidth(outerWidth, outerWidth+8)
	inner := dialogInnerWidth(outerWidth)

	var lines []string
	if title != "" {
		lines = append(lines, styleDialogTitle().
			Background(colPanel).
			Width(inner).
			MaxHeight(1).
			Render(title))
	}
	if title != "" && len(body) > 0 {
		lines = append(lines, dialogRuleLine(inner))
	}
	for _, b := range body {
		if b == "" {
			lines = append(lines, panelFillLine(inner, ""))
			continue
		}
		if strings.Contains(b, "\n") {
			for _, line := range strings.Split(b, "\n") {
				lines = append(lines, panelFillLine(inner, line))
			}
			continue
		}
		lines = append(lines, panelFillLine(inner, b))
	}
	if footer != "" {
		lines = append(lines, dialogRuleLine(inner))
		lines = append(lines, panelFillLine(inner, footer))
	}
	content := joinLines(lines)
	return styleDialogView().
		Padding(0, 2).
		Width(outerWidth).
		Render(content)
}

// helpSectionBlock is a titled group of shortcut rows (no trailing blank).
type helpSectionBlock struct {
	title string
	rows  [][2]string // key, desc
}

func helpSectionsLeft(colW int) []helpSectionBlock {
	_ = colW
	// Balanced with right (~19 rows compact).
	return []helpSectionBlock{
		{
			title: "Tabs",
			rows: [][2]string{
				{KeyCtrlShift("T"), "New tab"},
				{KeyCtrlShift("N"), "New window"},
				{KeyCtrl("W"), "Close tab"},
				{KeyCtrl("Tab"), "Next / prev"},
				{KeyCtrl("1-9"), "Jump"},
				{"Rename tab", "Palette · double-click"},
			},
		},
		{
			title: "Command line",
			rows: [][2]string{
				{"Enter", "Run command"},
				{KeyShift("Enter"), "New line"},
				{KeyUpDown(), "Line / history"},
				{"→ / Tab", "Accept suggest · complete"},
				{"⌘⌫ · Esc", "Clear line / to start"},
				{KeyCtrl("C"), "Clear / interrupt"},
				{KeyCtrl("V"), "Paste into bar"},
			},
		},
		{
			title: "Terminal",
			rows: [][2]string{
				{KeyCtrlShift("C"), "Copy selection"},
				{"Wheel", "Scrollback"},
				{"⌘/Ctrl-click", "Open URL"},
				{"Hover link", "Highlight"},
			},
		},
	}
}

func helpSectionsRight(colW int) []helpSectionBlock {
	_ = colW
	return []helpSectionBlock{
		{
			title: "Panes",
			rows: [][2]string{
				{KeyCtrlShift("D"), "Split right"},
				{KeyCtrlShift("E"), "Split down"},
				{"⌘⌥+arrows", "Focus pane (macOS)"},
				{"Alt+arrows", "Focus pane (Windows)"},
				{"⌥/Ctrl+←→", "Word jump"},
				{"⌘←→ · Home/End", "Line ends (macOS)"},
				{"Hold ←→ / ⌫", "Key repeat"},
				{"F2", "Rename pane"},
				{"Double-click", "Rename tab / pane"},
				{KeyCtrlShift("W"), "Close pane"},
			},
		},
		{
			title: "Chrome",
			rows: [][2]string{
				{KeyCtrl("K"), "Palette"},
				{KeyCtrl(","), "Settings"},
				{KeyCtrl("/"), "Help (this window)"},
				{KeyCtrlShift("M"), "Notes"},
				{"⌘+/⌘- · Ctrl++/−", "Zoom in / out"},
				{"⌘0 · Ctrl+0", "Reset zoom"},
				{"Esc", "Dismiss overlay"},
			},
		},
	}
}

func helpAllSections(colW int) []helpSectionBlock {
	return append(helpSectionsLeft(colW), helpSectionsRight(colW)...)
}

// helpJoinSections flattens section blocks into dialog body lines.
// compact: no blank rows between sections (two-column layout).
func helpJoinSections(colW int, sections ...helpSectionBlock) []string {
	return helpJoinSectionsEx(colW, false, sections...)
}

func helpJoinSectionsEx(colW int, compact bool, sections ...helpSectionBlock) []string {
	var body []string
	for i, sec := range sections {
		body = append(body, helpSection(colW, sec.title))
		for _, r := range sec.rows {
			body = append(body, helpRow(colW, r[0], r[1]))
		}
		if !compact && i < len(sections)-1 {
			body = append(body, panelFillLine(colW, ""))
		}
	}
	return body
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
	// Two-column help is narrower — keep key column proportional.
	if inner < 30 {
		kw = 9
		if !keyFancyOn() {
			kw = 12
		}
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
