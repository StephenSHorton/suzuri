package chrome

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Sync command palette — no bubbles list.Update / tea.Cmd filter pipeline.
// The Win32 host is not a Bubble Tea runtime; any cmd-based filter either
// blocks keystrokes or feels laggy. Filtering here is a plain substring match
// on the key path (O(n) over ~20 commands).

const paletteMaxRows = 12

func loadPaletteItems(cfg config.Config) []paletteItem {
	cmds := DefaultCommands(cfg.ActiveProfile, config.ProfileNames(cfg))
	out := make([]paletteItem, 0, len(cmds))
	for _, c := range cmds {
		title := c.Title
		if c.Desc != "" {
			title = c.Title + "  ·  " + c.Desc
		}
		out = append(out, paletteItem{
			title:   title,
			desc:    c.Desc,
			action:  c.Action,
			profile: c.ProfileName,
			minutes: c.Minutes,
		})
	}
	return out
}

func (m *Model) rebuildPalette() {
	cfg := config.Normalize(m.lastCfg)
	m.lastCfg = cfg
	m.palAll = loadPaletteItems(cfg)
	m.refilterPalette()
}

func (m *Model) activatePalette() {
	m.rebuildPalette()
	m.palFilter = ""
	m.palIndex = 0
	m.refilterPalette()
	m.PaletteOpen = true
}

func (m *Model) refilterPalette() {
	q := strings.ToLower(strings.TrimSpace(m.palFilter))
	if q == "" {
		m.palView = append([]paletteItem(nil), m.palAll...)
	} else {
		m.palView = m.palView[:0]
		if m.palView == nil {
			m.palView = make([]paletteItem, 0, len(m.palAll))
		}
		for _, it := range m.palAll {
			hay := strings.ToLower(it.title + " " + it.desc)
			if strings.Contains(hay, q) {
				m.palView = append(m.palView, it)
			}
		}
	}
	if len(m.palView) == 0 {
		m.palIndex = 0
		return
	}
	if m.palIndex >= len(m.palView) {
		m.palIndex = len(m.palView) - 1
	}
	if m.palIndex < 0 {
		m.palIndex = 0
	}
}

func (m *Model) paletteSelected() (paletteItem, bool) {
	if m.palIndex < 0 || m.palIndex >= len(m.palView) {
		return paletteItem{}, false
	}
	return m.palView[m.palIndex], true
}

// handlePaletteKey processes keys for the sync palette. Never calls tea.Cmd.
func (m *Model) handlePaletteKey(msg tea.KeyMsg) (act HostAction, profile string, minutes int) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.PaletteOpen = false
		return ActionNone, "", 0
	case "enter":
		it, ok := m.paletteSelected()
		if !ok {
			return ActionNone, "", 0
		}
		m.PaletteOpen = false
		act = it.action
		profile = it.profile
		minutes = it.minutes
		switch act {
		case ActionOpenSettings:
			m.SettingsOpen = true
			m.settings = newSettingsState(m.lastCfg)
		case ActionOpenHelp:
			m.HelpOpen = true
			act = ActionNone
		case ActionOpenNotes:
			m.openNotes()
			act = ActionNone
		case ActionOpenWorkspace:
			m.openWorkspace()
			act = ActionNone
		case ActionOpenTransferSend:
			m.openTransferPrompt(TransferModeSend, "")
			act = ActionNone
		case ActionOpenTransferReceive:
			m.openTransferPrompt(TransferModeReceive, "")
			act = ActionNone
		}
		return act, profile, minutes
	case "up":
		if m.palIndex > 0 {
			m.palIndex--
		}
		return ActionNone, "", 0
	case "down":
		if m.palIndex+1 < len(m.palView) {
			m.palIndex++
		}
		return ActionNone, "", 0
	case "backspace":
		if m.palFilter != "" {
			rs := []rune(m.palFilter)
			m.palFilter = string(rs[:len(rs)-1])
			m.refilterPalette()
		}
		return ActionNone, "", 0
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			// Ignore control runes.
			for _, r := range msg.Runes {
				if r >= 32 {
					m.palFilter += string(r)
				}
			}
			m.refilterPalette()
			// Keep selection on first match after refilter for predictable Enter.
			m.palIndex = 0
		}
		return ActionNone, "", 0
	}
}

// renderPalette draws filter + list with full-width panel rows (no bubbles list).
func (m Model) renderPalette(innerW int) string {
	if innerW < 16 {
		innerW = 16
	}
	var lines []string

	// Filter line: "> query" or "> filter..."
	prompt := "> "
	body := m.palFilter
	placeholder := false
	if body == "" {
		body = "filter..."
		placeholder = true
	}
	filterPlain := padFit(prompt+body, innerW)
	if placeholder {
		// Dim placeholder after prompt.
		p := styleDialogHintKey().Render(prompt)
		rest := styleDialogHint().Render(padFit(body, innerW-lipgloss.Width(prompt)))
		lines = append(lines, panelFillLine(innerW, p+rest))
	} else {
		lines = append(lines, styleDialogHintKey().Width(innerW).MaxHeight(1).Render(filterPlain))
	}

	// Always emit paletteMaxRows item slots so the card height is stable.
	// The Win32 host re-paints filter updates in-place on the backbuffer; a
	// shrinking card would leave ghost rows from the previous frame.
	itemRows := 0
	if len(m.palView) == 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(colMute).Background(colPanel).
			Width(innerW).MaxHeight(1).Padding(0, 1).
			Render("No matches"))
		itemRows = 1
	} else {
		max := paletteMaxRows
		if max > len(m.palView) {
			max = len(m.palView)
		}
		// Window around selection if many matches.
		start := 0
		if m.palIndex >= max {
			start = m.palIndex - max + 1
		}
		end := start + max
		if end > len(m.palView) {
			end = len(m.palView)
		}
		for i := start; i < end; i++ {
			it := m.palView[i]
			title := padFit(it.title, innerW-2)
			var row string
			if i == m.palIndex {
				row = styleDialogActive().Width(innerW).MaxHeight(1).Render(title)
			} else {
				row = styleDialogNormalItem().Width(innerW).MaxHeight(1).Render(title)
			}
			if lipgloss.Width(row) < innerW {
				row += lipgloss.NewStyle().Background(colPanel).
					Render(strings.Repeat(" ", innerW-lipgloss.Width(row)))
			}
			lines = append(lines, row)
			itemRows++
		}
	}
	blank := lipgloss.NewStyle().Background(colPanel).Width(innerW).MaxHeight(1).
		Render(strings.Repeat(" ", innerW))
	for itemRows < paletteMaxRows {
		lines = append(lines, blank)
		itemRows++
	}

	return strings.Join(lines, "\n")
}
