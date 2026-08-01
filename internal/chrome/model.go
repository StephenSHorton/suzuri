// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// tab strip, status line, and command palette. The shell viewport stays a
// VT cell grid; everything around it is this model’s View().
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
	delegate.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colText).
		Padding(0, 0, 0, 1)
	delegate.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colDim).
		Padding(0, 0, 0, 1)
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colText).
		Background(colSel).
		Bold(true).
		Padding(0, 0, 0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colAccent)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colSoft).
		Background(colSel).
		Padding(0, 0, 0, 1).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colAccent)
	delegate.Styles.DimmedTitle = lipgloss.NewStyle().
		Foreground(colDim).
		Padding(0, 0, 0, 1)
	delegate.Styles.DimmedDesc = lipgloss.NewStyle().
		Foreground(colMute).
		Padding(0, 0, 0, 1)
	delegate.Styles.FilterMatch = lipgloss.NewStyle().
		Foreground(colMatch).
		Underline(true)

	l := list.New(items, delegate, width, 10)
	l.Title = "Commands"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(colAccent).
		Bold(true).
		Padding(0, 1)
	l.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(colAccent)
	l.Styles.FilterCursor = lipgloss.NewStyle().Foreground(colText)
	l.Styles.NoItems = lipgloss.NewStyle().Foreground(colDim).Padding(0, 1)
	l.FilterInput.Prompt = "› "
	l.FilterInput.Placeholder = "filter…"
	l.FilterInput.PromptStyle = lipgloss.NewStyle().Foreground(colAccent)
	l.FilterInput.TextStyle = lipgloss.NewStyle().Foreground(colText)
	l.FilterInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(colDim)

	return Model{
		Width:  width,
		Height: 2,
		Status: "ready",
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

// tabLabel is the visible title for a tab (no index prefix).
func tabLabel(t Tab, i int) string {
	title := t.Title
	if title == "" {
		title = fmt.Sprintf("shell %d", i+1)
	}
	rs := []rune(title)
	if len(rs) > 18 {
		title = string(rs[:16]) + "…"
	}
	return title
}

// View renders tab strip + status (+ palette) with Lip Gloss.
func (m Model) View() string {
	w := m.Width
	if w < 20 {
		w = 20
	}

	tabLine := m.renderTabLine(w)
	statusLine := m.renderStatusLine(w)

	if !m.PaletteOpen {
		return tabLine + "\n" + statusLine
	}

	// Palette as a floating rounded card under the chrome header.
	m.palette.SetWidth(max(24, w-4))
	m.palette.SetHeight(10)
	body := stylePaletteBorder().
		Width(max(22, w-4)).
		Render(m.palette.View())
	// Center the card if the window is wider than the body.
	body = lipgloss.PlaceHorizontal(w, lipgloss.Center, body,
		lipgloss.WithWhitespaceBackground(colInk))
	return tabLine + "\n" + statusLine + "\n" + body
}

func (m Model) renderTabLine(w int) string {
	brand := styleBrand().Render("硯")
	sep := styleSep().Render("│")

	var parts []string
	parts = append(parts, brand)

	if len(m.Tabs) == 0 {
		parts = append(parts, sep, styleInactiveTab().Render("no tabs"))
	} else {
		for i, t := range m.Tabs {
			parts = append(parts, sep)
			label := tabLabel(t, i)
			if i == m.Active {
				// Accent bar + title for the active tab.
				mark := lipgloss.NewStyle().
					Foreground(colAccent).
					Background(colActive).
					Bold(true).
					Render("▌")
				title := lipgloss.NewStyle().
					Foreground(colText).
					Background(colActive).
					Bold(true).
					Padding(0, 1, 0, 0).
					Render(label)
				parts = append(parts, mark+title)
			} else {
				parts = append(parts, styleInactiveTab().Render(label))
			}
		}
	}
	parts = append(parts, sep, stylePlus().Render("+"))

	left := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	// Right-side brand wordmark (subtle).
	right := lipgloss.NewStyle().
		Foreground(colDim).
		Background(colPaper).
		Render("suzuri")

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		// Truncate: keep left, drop wordmark if needed.
		line := left
		if lipgloss.Width(line) > w {
			line = lipgloss.NewStyle().MaxWidth(w).Render(line)
		}
		return styleTabBar().Width(w).Render(line)
	}
	mid := lipgloss.NewStyle().
		Background(colPaper).
		Render(strings.Repeat(" ", gap))
	return styleTabBar().Width(w).Render(left + mid + right)
}

func (m Model) renderStatusLine(w int) string {
	leftText := m.Status
	if leftText == "" {
		leftText = " "
	}
	// Show tab count when quiet.
	if leftText == "ready" && len(m.Tabs) > 0 {
		leftText = fmt.Sprintf("%d tab%s", len(m.Tabs), plural(len(m.Tabs)))
	}

	left := styleStatus().Render(leftText)

	// Hint cluster: key in soft, rest dim — reads as chrome, not shell noise.
	key := styleStatusKey().Render("ctrl+k")
	hint := styleStatusHint().Render(" palette  ·  ")
	key2 := styleStatusKey().Render("⌃⇧t")
	hint2 := styleStatusHint().Render(" new tab")
	right := key + hint + key2 + hint2

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return styleStatus().Width(w).Render(leftText)
	}
	mid := lipgloss.NewStyle().
		Background(colStone).
		Render(strings.Repeat(" ", gap))
	return lipgloss.NewStyle().
		Background(colStone).
		Width(w).
		Render(left + mid + right)
}

// TabBounds returns [startCol, endCol) for each tab label, matching View layout
// (after brand + separators). Used for mouse hit-testing in the host.
func (m Model) TabBounds() [][2]int {
	// brand " 硯 " ≈ styleBrand padding
	col := lipgloss.Width(styleBrand().Render("硯"))
	sepW := lipgloss.Width(styleSep().Render("│"))

	out := make([][2]int, len(m.Tabs))
	for i, t := range m.Tabs {
		col += sepW
		label := tabLabel(t, i)
		var w int
		if i == m.Active {
			// Matches renderTabLine: mark "▌" + title with Padding(0,1,0,0).
			mark := lipgloss.NewStyle().
				Foreground(colAccent).
				Background(colActive).
				Bold(true).
				Render("▌")
			title := lipgloss.NewStyle().
				Foreground(colText).
				Background(colActive).
				Bold(true).
				Padding(0, 1, 0, 0).
				Render(label)
			w = lipgloss.Width(mark + title)
		} else {
			w = lipgloss.Width(styleInactiveTab().Render(label))
		}
		out[i] = [2]int{col, col + w}
		col += w
	}
	return out
}

// RowCount is how many terminal rows the chrome View occupies (approx).
func (m Model) RowCount() int {
	if m.PaletteOpen {
		return 2 + 12 // header + palette room
	}
	return 2
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
