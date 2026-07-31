// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// tab strip, status line, and (later) command palette. The shell viewport
// stays a VT cell grid; everything around it is this model’s View().
package chrome

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Tab is a chrome-level tab descriptor (no PTY — host owns sessions).
type Tab struct {
	ID    int
	Title string
}

// Model is the Bubble Tea model for host chrome.
type Model struct {
	Width  int
	Height int // total chrome rows when palette closed (usually 2: tabs+status)

	Tabs   []Tab
	Active int

	Status string

	// Command palette
	PaletteOpen bool
	palette     list.Model
}

// Messages the host dispatches into the model (no keyboard steal from PTY
// unless palette is open — host decides).
type (
	// SyncTabs replaces the tab list and active index from the host.
	SyncTabsMsg struct {
		Tabs   []Tab
		Active int
	}
	// StatusMsg sets the status line.
	StatusMsg string
	// OpenPaletteMsg opens the command palette.
	OpenPaletteMsg struct{}
	// ClosePaletteMsg closes it.
	ClosePaletteMsg struct{}
)

// HostAction is returned to the Win32 host after an Update that should affect sessions.
type HostAction int

const (
	ActionNone HostAction = iota
	ActionNewTab
	ActionCloseTab
	ActionSelectTab // use SelectIndex
	ActionNextTab
	ActionPrevTab
	ActionQuit
)

// Result pairs the new model with an optional host action.
type Result struct {
	Model  Model
	Action HostAction
	Index  int // for ActionSelectTab
}

// KeyMap for palette / chrome shortcuts when palette owns input.
type KeyMap struct {
	NewTab    key.Binding
	CloseTab  key.Binding
	NextTab   key.Binding
	PrevTab   key.Binding
	Palette   key.Binding
	Quit      key.Binding
}

var DefaultKeys = KeyMap{
	NewTab:   key.NewBinding(key.WithKeys("ctrl+shift+t"), key.WithHelp("ctrl+shift+t", "new tab")),
	CloseTab: key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("ctrl+w", "close tab")),
	NextTab:  key.NewBinding(key.WithKeys("ctrl+tab", "tab"), key.WithHelp("ctrl+tab", "next tab")),
	PrevTab:  key.NewBinding(key.WithKeys("ctrl+shift+tab", "shift+tab"), key.WithHelp("ctrl+shift+tab", "prev tab")),
	Palette:  key.NewBinding(key.WithKeys("ctrl+k", "ctrl+p"), key.WithHelp("ctrl+k", "palette")),
	Quit:     key.NewBinding(key.WithKeys("ctrl+shift+q"), key.WithHelp("ctrl+shift+q", "quit")),
}

type paletteItem struct {
	title, desc string
	action      HostAction
}

func (i paletteItem) Title() string       { return i.title }
func (i paletteItem) Description() string { return i.desc }
func (i paletteItem) FilterValue() string { return i.title + " " + i.desc }

func New(width int) Model {
	items := []list.Item{
		paletteItem{title: "New tab", desc: "Ctrl+Shift+T", action: ActionNewTab},
		paletteItem{title: "Close tab", desc: "Ctrl+W", action: ActionCloseTab},
		paletteItem{title: "Next tab", desc: "Ctrl+Tab", action: ActionNextTab},
		paletteItem{title: "Previous tab", desc: "Ctrl+Shift+Tab", action: ActionPrevTab},
	}
	l := list.New(items, list.NewDefaultDelegate(), width, 10)
	l.Title = "Commands"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()

	return Model{
		Width:   width,
		Height:  2,
		Status:  "ctrl+shift+t new tab · ctrl+k palette · ctrl+w close",
		palette: l,
	}
}

func (m Model) Init() tea.Cmd { return nil }

// UpdateChrome applies a message and returns host actions for session control.
// This is used from the Win32 host without running tea.Program.
func (m Model) UpdateChrome(msg tea.Msg) Result {
	var act HostAction
	var idx int

	switch msg := msg.(type) {
	case SyncTabsMsg:
		m.Tabs = msg.Tabs
		m.Active = msg.Active
		if m.Active < 0 {
			m.Active = 0
		}
		if m.Active >= len(m.Tabs) && len(m.Tabs) > 0 {
			m.Active = len(m.Tabs) - 1
		}
	case StatusMsg:
		m.Status = string(msg)
	case OpenPaletteMsg:
		m.PaletteOpen = true
		m.palette.ResetFilter()
		m.palette.ResetSelected()
	case ClosePaletteMsg:
		m.PaletteOpen = false
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.palette.SetWidth(msg.Width)
		m.palette.SetHeight(min(12, msg.Height-2))
	case tea.KeyMsg:
		if m.PaletteOpen {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.PaletteOpen = false
			case "enter":
				if it, ok := m.palette.SelectedItem().(paletteItem); ok {
					act = it.action
					m.PaletteOpen = false
				}
			default:
				var cmd tea.Cmd
				m.palette, cmd = m.palette.Update(msg)
				_ = cmd
			}
			return Result{Model: m, Action: act, Index: idx}
		}
		// Palette closed — host usually handles keys for the PTY; these are
		// chrome-level bindings the host may forward.
		switch {
		case key.Matches(msg, DefaultKeys.NewTab):
			act = ActionNewTab
		case key.Matches(msg, DefaultKeys.CloseTab):
			act = ActionCloseTab
		case key.Matches(msg, DefaultKeys.NextTab):
			act = ActionNextTab
		case key.Matches(msg, DefaultKeys.PrevTab):
			act = ActionPrevTab
		case key.Matches(msg, DefaultKeys.Palette):
			m.PaletteOpen = true
			m.palette.ResetFilter()
			m.palette.ResetSelected()
		case key.Matches(msg, DefaultKeys.Quit):
			act = ActionQuit
		}
	}

	return Result{Model: m, Action: act, Index: idx}
}

// View renders tab strip + status (+ palette) with Lip Gloss.
func (m Model) View() string {
	w := m.Width
	if w < 20 {
		w = 20
	}

	active := lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("63")).
		Bold(true).
		Padding(0, 1)
	inactive := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)
	bar := lipgloss.NewStyle().Background(lipgloss.Color("235")).Width(w)
	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Background(lipgloss.Color("235")).
		Width(w).
		Padding(0, 1)

	var parts []string
	if len(m.Tabs) == 0 {
		parts = append(parts, inactive.Render("no tabs"))
	} else {
		for i, t := range m.Tabs {
			title := t.Title
			if title == "" {
				title = fmt.Sprintf("shell %d", i+1)
			}
			// Truncate long titles
			if len([]rune(title)) > 16 {
				rs := []rune(title)
				title = string(rs[:14]) + "…"
			}
			label := fmt.Sprintf("%d:%s", i+1, title)
			if i == m.Active {
				parts = append(parts, active.Render(label))
			} else {
				parts = append(parts, inactive.Render(label))
			}
		}
	}
	tabLine := bar.Render(lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	// Ensure full width (Join may be shorter)
	if lipgloss.Width(tabLine) < w {
		tabLine = bar.Render(tabLine + strings.Repeat(" ", w-lipgloss.Width(tabLine)))
	}

	st := m.Status
	if st == "" {
		st = " "
	}
	statusLine := status.Render(st)

	if !m.PaletteOpen {
		return tabLine + "\n" + statusLine
	}

	// Palette overlays below chrome header.
	m.palette.SetWidth(w)
	body := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Width(w - 2).
		Render(m.palette.View())
	return tabLine + "\n" + statusLine + "\n" + body
}

// RowCount is how many terminal rows the chrome View occupies (approx).
func (m Model) RowCount() int {
	if m.PaletteOpen {
		return 2 + 12 // header + palette room
	}
	return 2
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
