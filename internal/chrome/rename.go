package chrome

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// RenameTarget selects what a rename dialog renames.
type RenameTarget int

const (
	// RenameTargetPane renames the focused shell pane (leaf).
	RenameTargetPane RenameTarget = iota
	// RenameTargetTab renames the chrome strip tab (page).
	RenameTargetTab
)

// OpenRenameMsg opens the rename dialog with an optional seed name.
type OpenRenameMsg struct {
	Target RenameTarget
	Seed   string
}

func (m *Model) openRename(target RenameTarget, seed string) {
	m.closeModalsExcept("rename")
	m.RenameOpen = true
	m.renameTarget = target
	m.renameBuf = strings.TrimSpace(seed)
}

func (m *Model) handleRenameKey(msg tea.KeyMsg) (act HostAction, name string) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.RenameOpen = false
		m.renameBuf = ""
		return ActionNone, ""
	case "enter":
		name = strings.TrimSpace(m.renameBuf)
		m.RenameOpen = false
		m.renameBuf = ""
		return ActionApplyRename, name
	case "backspace":
		if m.renameBuf != "" {
			rs := []rune(m.renameBuf)
			m.renameBuf = string(rs[:len(rs)-1])
		}
		return ActionNone, ""
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			for _, r := range msg.Runes {
				if r >= 32 {
					m.renameBuf += string(r)
				}
			}
		}
		return ActionNone, ""
	}
}

func (m Model) renderRename(windowCols int) string {
	outer := clampDialogWidth(40, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 16 {
		inner = 16
	}
	title := "Rename pane"
	if m.renameTarget == RenameTargetTab {
		title = "Rename tab"
	}
	prompt := "> "
	body := m.renameBuf
	placeholder := false
	if body == "" {
		body = "name…"
		placeholder = true
	}
	var line string
	if placeholder {
		p := styleDialogHintKey().Render(prompt)
		rest := styleDialogHint().Render(padFit(body, inner-lipgloss.Width(prompt)))
		line = panelFillLine(inner, p+rest)
	} else {
		plain := padFit(prompt+body+"▌", inner)
		line = styleDialogHintKey().Width(inner).MaxHeight(1).Render(plain)
	}
	footer := styleDialogHintKey().Render("enter") +
		styleDialogHint().Render(" save  ") +
		styleDialogHintKey().Render("esc") +
		styleDialogHint().Render(" cancel · empty clears custom name")
	return renderDialogCard(outer, title, []string{line}, footer)
}
