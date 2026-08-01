// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// neon rounded tab cards, hairline, and command palette. Shell content stays VT.
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
	// Selected: neon left edge + soft pink wash (card-list look).
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colText).
		Background(colSel).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colNeon)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colViolet).
		Background(colSel).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colNeon)
	delegate.Styles.DimmedTitle = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)
	delegate.Styles.DimmedDesc = lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)
	delegate.Styles.FilterMatch = lipgloss.NewStyle().Foreground(colMatch).Underline(true)

	l := list.New(items, delegate, width, 8)
	l.Title = "Commands"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(colNeon).
		Bold(true).
		Padding(0, 1)
	l.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(colNeon)
	l.Styles.FilterCursor = lipgloss.NewStyle().Foreground(colCyan)
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(colDim).Padding(1, 1)
	l.FilterInput.Prompt = "› "
	l.FilterInput.Placeholder = "filter…"
	l.FilterInput.PromptStyle = lipgloss.NewStyle().Foreground(colNeon)
	l.FilterInput.TextStyle = lipgloss.NewStyle().Foreground(colText)
	l.FilterInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(colMute)

	return Model{
		Width:   width,
		Height:  TabStripRows(),
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
	title = strings.TrimPrefix(title, "Administrator: ")
	rs := []rune(title)
	if len(rs) > 18 {
		title = string(rs[:16]) + "…"
	}
	return title
}

// TabStripRows is the height of the neon tab-card strip (rounded border = 3 rows).
func TabStripRows() int { return 3 }

// View: neon rounded tab cards (+ optional status / palette card).
func (m Model) View() string {
	w := m.Width
	if w < 20 {
		w = 20
	}

	tabs, _ := m.layoutTabCards(w)
	out := tabs
	if m.showStatus() {
		out += "\n" + m.renderStatus(w)
	}
	if !m.PaletteOpen {
		return out
	}

	pw := min(48, max(30, w-10))
	m.palette.SetWidth(pw - 4)
	m.palette.SetHeight(8)
	// Nested rounded neon card — the Charm “floating panel” look.
	inner := m.palette.View()
	card := stylePaletteBorder().Width(pw).Render(inner)
	// Dim void around the card so it reads as floating over the shell.
	card = lipgloss.PlaceHorizontal(w, lipgloss.Center, card,
		lipgloss.WithWhitespaceBackground(colVoid),
		lipgloss.WithWhitespaceForeground(colMute))
	return out + "\n" + card
}

// layoutTabCards builds Charm-style rounded neon tab cards in a horizontal row.
// Returns the multi-line strip and [startCol,endCol) bounds for hit-testing.
func (m Model) layoutTabCards(w int) (string, [][2]int) {
	bounds := make([][2]int, len(m.Tabs))
	var parts []string

	// Brand as a small rounded violet chip so it aligns with 3-row tab cards.
	brand := lipgloss.NewStyle().
		Foreground(colViolet).
		Background(colBar).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colViolet).
		BorderBackground(colBar).
		Render("硯")
	parts = append(parts, brand)
	col := lipgloss.Width(brand)

	gap := styleGap().Render(" ")
	gapW := lipgloss.Width(gap)

	if len(m.Tabs) == 0 {
		parts = append(parts, gap, styleInactiveTab().Render("no tabs"))
	} else {
		for i, t := range m.Tabs {
			parts = append(parts, gap)
			col += gapW
			label := tabLabel(t, i)
			var card string
			if i == m.Active {
				card = styleActiveTab().Render(label)
			} else {
				card = styleInactiveTab().Render(label)
			}
			cw := lipgloss.Width(card)
			bounds[i] = [2]int{col, col + cw}
			parts = append(parts, card)
			col += cw
		}
	}

	// Neon-adjacent new-tab chip.
	parts = append(parts, gap, stylePlus().Render("+"))

	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	// Place on full-width bar so rounded cards sit on a continuous void strip.
	strip := lipgloss.NewStyle().
		Background(colBar).
		Width(w).
		Padding(0, 0).
		Render(row)

	// If Join produced multi-line cards, ensure every line is full width with bar bg.
	lines := strings.Split(strip, "\n")
	for i, ln := range lines {
		lw := lipgloss.Width(ln)
		if lw < w {
			lines[i] = ln + styleGap().Render(strings.Repeat(" ", w-lw))
		} else if lw > w {
			lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(ln)
		}
	}
	return strings.Join(lines, "\n"), bounds
}

func (m Model) showStatus() bool {
	s := strings.TrimSpace(m.Status)
	return s != "" && s != "ready"
}

func (m Model) renderStatus(w int) string {
	return styleStatus().Width(w).Render(m.Status)
}

// TabBounds matches layoutTabCards (column range of each card, including borders).
func (m Model) TabBounds() [][2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, bounds := m.layoutTabCards(w)
	return bounds
}

// RowCount is how many terminal rows the chrome View occupies.
func (m Model) RowCount() int {
	n := TabStripRows()
	if m.showStatus() {
		n++
	}
	if m.PaletteOpen {
		// rounded border card ~10 rows
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
