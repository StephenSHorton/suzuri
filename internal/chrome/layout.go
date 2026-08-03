package chrome

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Layout constants from Crush (dialog/common.go, commands.go, quickstyle.go).
const (
	// Crush defaultDialogMaxWidth = 70; host windows are often narrower.
	crushDialogMaxWidth = 70
	// Wide enough for notes list+editor split; other dialogs still pass smaller want.
	suzuriDialogMaxWidth = 68

	// Title / input content line heights in Crush sizeDialogList.
	titleContentHeight = 1
	// Help footer is one line after margin.
	helpContentHeight = 1

	// Crush: secondary info column hides above this % of row width.
	commandInfoMaxPercent = 25
)

// dialogFrameSize returns horizontal and vertical cells consumed by the
// dialog chrome (border + padding), matching Crush's Get*FrameSize usage.
func dialogFrameSize() (h, v int) {
	s := styleDialogView()
	// lipgloss: GetHorizontalFrameSize = padding+border left/right
	return s.GetHorizontalFrameSize(), s.GetVerticalFrameSize()
}

// clampDialogWidth is Crush min(max, windowWidth - border) for outer box width.
func clampDialogWidth(want, windowCols int) int {
	if windowCols < 24 {
		windowCols = 24
	}
	maxW := suzuriDialogMaxWidth
	if crushDialogMaxWidth < maxW {
		maxW = crushDialogMaxWidth
	}
	// Leave a little margin from the host edge (Crush subtracts border via area).
	avail := windowCols - 4
	if avail < maxW {
		maxW = avail
	}
	if maxW < 28 {
		maxW = 28
	}
	if want < 28 {
		want = 28
	}
	if want > maxW {
		return maxW
	}
	return want
}

// dialogInnerWidth is content width inside the bordered dialog box.
func dialogInnerWidth(outerWidth int) int {
	h, _ := dialogFrameSize()
	inner := outerWidth - h
	if inner < 16 {
		return 16
	}
	return inner
}

// renderDialogCard builds a Crush-style card: rounded primary border,
// title, optional rule, body lines, optional help footer.
//
// Every content line is forced to full inner width with panel background so
// short titles/footers don't leave dark void gutters (VT clear color) to the
// right of the text.
func renderDialogCard(outerWidth int, title string, body []string, footer string) string {
	return renderDialogCardEx(outerWidth, title, body, footer, false)
}

// renderDialogCardEx is renderDialogCard with optional active (selection-style) title,
// used when the dialog chrome title is an editable field (notes rename).
func renderDialogCardEx(outerWidth int, title string, body []string, footer string, titleActive bool) string {
	outerWidth = clampDialogWidth(outerWidth, outerWidth+8)
	inner := dialogInnerWidth(outerWidth)

	var lines []string
	if title != "" {
		tStyle := styleDialogTitle()
		if titleActive {
			tStyle = styleDialogActive()
		}
		lines = append(lines, tStyle.
			Background(colPanel).
			Width(inner).
			MaxHeight(1).
			Render(title))
	}
	// Subtle separator under title (separator role).
	if title != "" && len(body) > 0 {
		lines = append(lines, dialogRuleLine(inner))
	}
	for _, b := range body {
		if b == "" {
			lines = append(lines, panelFillLine(inner, ""))
			continue
		}
		// Multi-line blocks (e.g. bubbles list View) must not pass through
		// panelFillLine as one blob — MaxHeight(1) used to crush the whole
		// command list into a single dark empty row.
		if strings.Contains(b, "\n") {
			for _, line := range strings.Split(b, "\n") {
				lines = append(lines, panelFillLine(inner, line))
			}
			continue
		}
		// Body rows may already be styled; re-pad with panel so short lines fill.
		lines = append(lines, panelFillLine(inner, b))
	}
	if footer != "" {
		lines = append(lines, dialogRuleLine(inner))
		lines = append(lines, panelFillLine(inner, footer))
	}

	content := joinLines(lines)
	return styleDialogView().Width(outerWidth).Render(content)
}

// panelFillLine ensures a content row spans inner columns on colPanel, so
// unstyled trailing cells never show the dark shell/void behind the modal.
func panelFillLine(inner int, content string) string {
	if inner < 1 {
		inner = 1
	}
	// Outer width + panel bg pads with spaces using panel background.
	return lipgloss.NewStyle().
		Background(colPanel).
		Width(inner).
		MaxHeight(1).
		Render(content)
}

func dialogRuleLine(inner int) string {
	ruleW := inner
	if ruleW < 8 {
		ruleW = 8
	}
	return styleDialogRule().
		Background(colPanel).
		Width(inner).
		MaxHeight(1).
		Render(stringsRepeat("─", ruleW))
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for i := 1; i < len(lines); i++ {
		out += "\n" + lines[i]
	}
	return out
}

func stringsRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}

// hideInfoIfCrowded mirrors Crush applyInfoColumnVisibility for a single
// secondary string (e.g. keybinding). Returns "" if it would exceed maxPercent.
func hideInfoIfCrowded(info string, rowWidth, maxPercent int) string {
	if info == "" || rowWidth <= 0 {
		return info
	}
	w := lipgloss.Width(" " + info + " ")
	if w*100 > rowWidth*maxPercent {
		return ""
	}
	return info
}
