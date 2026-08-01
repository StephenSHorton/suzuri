// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// tab strip, hairline rule, and command palette. Shell content stays VT.
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
	Height int

	Tabs   []Tab
	Active int

	Status string // shown only when non-empty and not "ready"

	PaletteOpen bool
	palette     list.Model
}

type (
	SyncTabsMsg struct {
		Tabs   []Tab
		Active int
	}
	StatusMsg       string
	OpenPaletteMsg  struct{}
	ClosePaletteMsg struct{}
)

type HostAction int

const (
	ActionNone HostAction = iota
	ActionNewTab
	ActionCloseTab
	ActionSelectTab
	ActionNextTab
	ActionPrevTab
	ActionQuit
)

type Result struct {
	Model  Model
	Action HostAction
	Index  int
}

type KeyMap struct {
	NewTab   key.Binding
	CloseTab key.Binding
	NextTab  key.Binding
	PrevTab  key.Binding
	Palette  key.Binding
	Quit     key.Binding
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

	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colSoft).
		Padding(0, 1)
	delegate.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colMute).
		Padding(0, 1)
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colText).
		Background(colSel).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colAccent)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colAccent).
		Background(colSel).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colAccent)
	delegate.Styles.DimmedTitle = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)
	delegate.Styles.DimmedDesc = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)
	delegate.Styles.FilterMatch = lipgloss.NewStyle().Foreground(colMatch).Underline(true)

	l := list.New(items, delegate, width, 8)
	l.Title = "Command palette"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(colAccent).
		Bold(true).
		MarginBottom(0).
		Padding(0, 1)
	l.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(colAccent)
	l.Styles.FilterCursor = lipgloss.NewStyle().Foreground(colText)
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(colDim).Padding(1, 1)
	l.FilterInput.Prompt = "› "
	l.FilterInput.Placeholder = "Type a command…"
	l.FilterInput.PromptStyle = lipgloss.NewStyle().Foreground(colAccent)
	l.FilterInput.TextStyle = lipgloss.NewStyle().Foreground(colText)
	l.FilterInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(colMute)

	return Model{
		Width:   width,
		Height:  2,
		Status:  "",
		palette: l,
	}
}

func (m Model) Init() tea.Cmd { return nil }

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
		m.palette.SetWidth(min(56, msg.Width-4))
		m.palette.SetHeight(min(10, msg.Height-2))
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

func tabLabel(t Tab, i int) string {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		title = fmt.Sprintf("Shell %d", i+1)
	}
	// Strip common PowerShell noise for a calmer tab title.
	title = strings.TrimPrefix(title, "Administrator: ")
	rs := []rune(title)
	if len(rs) > 20 {
		title = string(rs[:18]) + "…"
	}
	return title
}

// View: tab strip + accent hairline (+ optional status / palette).
func (m Model) View() string {
	w := m.Width
	if w < 20 {
		w = 20
	}

	tabs, bounds := m.layoutTabs(w)
	rule := m.renderRule(w, bounds)

	out := tabs + "\n" + rule
	if m.showStatus() {
		out += "\n" + m.renderStatus(w)
	}
	if !m.PaletteOpen {
		return out
	}

	pw := min(52, max(28, w-8))
	m.palette.SetWidth(pw)
	m.palette.SetHeight(9)
	card := stylePaletteBorder().Width(pw).Render(m.palette.View())
	// Dim the rest of the row so the card reads as a modal, not a full-width dump.
	card = lipgloss.PlaceHorizontal(w, lipgloss.Center, card,
		lipgloss.WithWhitespaceBackground(colShell),
		lipgloss.WithWhitespaceForeground(colMute))
	return out + "\n" + card
}

// layoutTabs builds the tab row and returns per-tab [start,end) columns.
func (m Model) layoutTabs(w int) (string, [][2]int) {
	bounds := make([][2]int, len(m.Tabs))
	var parts []string
	col := 0

	// Leading pad.
	lead := styleGap().Render(" ")
	parts = append(parts, lead)
	col += lipgloss.Width(lead)

	if len(m.Tabs) == 0 {
		parts = append(parts, styleInactiveTab().Render("no tabs"))
	} else {
		for i, t := range m.Tabs {
			label := tabLabel(t, i)
			var seg string
			if i == m.Active {
				seg = styleActiveTab().Render(label)
			} else {
				seg = styleInactiveTab().Render(label)
			}
			sw := lipgloss.Width(seg)
			bounds[i] = [2]int{col, col + sw}
			parts = append(parts, seg)
			col += sw
			// Gap between tabs (not after last).
			if i < len(m.Tabs)-1 {
				g := styleGap().Render(" ")
				parts = append(parts, g)
				col += lipgloss.Width(g)
			}
		}
	}

	// New-tab affordance.
	gap := styleGap().Render("  ")
	parts = append(parts, gap)
	col += lipgloss.Width(gap)
	parts = append(parts, stylePlus().Render("+"))

	left := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	lw := lipgloss.Width(left)
	if lw >= w {
		return styleBar().Width(w).MaxWidth(w).Render(left), bounds
	}
	fill := styleGap().Render(strings.Repeat(" ", w-lw))
	return left + fill, bounds
}

// renderRule draws a hairline under the strip; accent under the active tab
// (Windows Terminal “selected tab attaches to content” cue).
func (m Model) renderRule(w int, bounds [][2]int) string {
	if w < 1 {
		return ""
	}
	cells := make([]string, w)
	for i := 0; i < w; i++ {
		cells[i] = styleRule().Render("─")
	}
	if m.Active >= 0 && m.Active < len(bounds) {
		b := bounds[m.Active]
		for x := b[0]; x < b[1] && x < w; x++ {
			if x >= 0 {
				// Active segment: solid accent on shell bg (opens into viewport).
				cells[x] = styleRuleActive().Render("▀")
			}
		}
	}
	return strings.Join(cells, "")
}

func (m Model) showStatus() bool {
	s := strings.TrimSpace(m.Status)
	return s != "" && s != "ready"
}

func (m Model) renderStatus(w int) string {
	return styleStatus().Width(w).Render(m.Status)
}

// TabBounds matches layoutTabs (for mouse hit-testing).
func (m Model) TabBounds() [][2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, bounds := m.layoutTabs(w)
	return bounds
}

// RowCount is how many terminal rows the chrome View occupies.
func (m Model) RowCount() int {
	n := 2 // tabs + rule
	if m.showStatus() {
		n++
	}
	if m.PaletteOpen {
		n += 11
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
