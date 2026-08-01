// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// neon rounded tab cards, command palette, and settings dialog.
// Shell content stays a VT cell grid in the host.
package chrome

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
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

	Status string

	PaletteOpen  bool
	SettingsOpen bool
	ConfirmOpen  bool
	palette      list.Model
	settings     settingsState
	confirm      confirmState
	// lastCfg is the host's applied config (for reopening settings).
	lastCfg config.Config
}

type (
	SyncTabsMsg struct {
		Tabs   []Tab
		Active int
	}
	StatusMsg       string
	OpenPaletteMsg  struct{}
	ClosePaletteMsg struct{}
	// OpenSettingsMsg opens the settings dialog with a snapshot of host config.
	OpenSettingsMsg struct {
		Config config.Config
	}
	CloseSettingsMsg struct{}
	// SyncConfigMsg updates lastCfg without opening UI (host applied settings).
	SyncConfigMsg struct {
		Config config.Config
	}
	// OpenConfirmQuitMsg asks to quit the app (last tab closed).
	OpenConfirmQuitMsg struct{}
	// DismissOverlayMsg closes palette/settings/confirm without side effects
	// (except settings cancel restores snap via ActionSettingsCancel).
	DismissOverlayMsg struct{}
)

// HostAction is returned to the Win32 host after UpdateChrome.
type HostAction int

const (
	ActionNone HostAction = iota
	ActionNewTab
	ActionCloseTab
	ActionSelectTab
	ActionNextTab
	ActionPrevTab
	ActionQuit
	ActionOpenSettings
	// ActionSettingsPreview: live-apply Settings config (do not persist).
	ActionSettingsPreview
	// ActionSettingsApply: persist Settings and close dialog.
	ActionSettingsApply
	// ActionSettingsCancel: restore Settings.snap and close dialog.
	ActionSettingsCancel
)

// Result pairs the new model with an optional host action.
type Result struct {
	Model    Model
	Action   HostAction
	Index    int
	Settings config.Config // set for preview/apply/cancel
}

// KeyMap for chrome shortcuts the host may forward.
type KeyMap struct {
	NewTab   key.Binding
	CloseTab key.Binding
	NextTab  key.Binding
	PrevTab  key.Binding
	Palette  key.Binding
	Settings key.Binding
	Quit     key.Binding
}

// DefaultKeys documents bindings (host also handles most of these).
var DefaultKeys = KeyMap{
	NewTab:   key.NewBinding(key.WithKeys("ctrl+shift+t"), key.WithHelp("ctrl+shift+t", "new tab")),
	CloseTab: key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("ctrl+w", "close tab")),
	NextTab:  key.NewBinding(key.WithKeys("ctrl+tab", "tab"), key.WithHelp("ctrl+tab", "next tab")),
	PrevTab:  key.NewBinding(key.WithKeys("ctrl+shift+tab", "shift+tab"), key.WithHelp("ctrl+shift+tab", "prev tab")),
	Palette:  key.NewBinding(key.WithKeys("ctrl+k", "ctrl+p"), key.WithHelp("ctrl+k", "palette")),
	Settings: key.NewBinding(key.WithKeys("ctrl+,"), key.WithHelp("ctrl+,", "settings")),
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
	cmds := DefaultCommands()
	items := make([]list.Item, len(cmds))
	for i, c := range cmds {
		items[i] = paletteItem{title: c.Title, desc: c.Desc, action: c.Action}
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

	cfg := config.Default()
	return Model{
		Width:   width,
		Height:  TabStripRows(),
		Status:  "",
		palette: l,
		lastCfg: cfg,
	}
}

func (m Model) Init() tea.Cmd { return nil }

// OverlayOpen is true when palette, settings, or confirm owns keyboard focus.
func (m Model) OverlayOpen() bool {
	return m.PaletteOpen || m.SettingsOpen || m.ConfirmOpen
}

// UpdateChrome applies a message and returns host actions.
func (m Model) UpdateChrome(msg tea.Msg) Result {
	var act HostAction
	var idx int
	var settings config.Config

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
	case SyncConfigMsg:
		m.lastCfg = config.Normalize(msg.Config)
	case OpenPaletteMsg:
		m.SettingsOpen = false
		m.ConfirmOpen = false
		m.PaletteOpen = true
		m.palette.ResetFilter()
		m.palette.ResetSelected()
	case ClosePaletteMsg:
		m.PaletteOpen = false
	case OpenSettingsMsg:
		m.PaletteOpen = false
		m.ConfirmOpen = false
		m.SettingsOpen = true
		m.settings = newSettingsState(msg.Config)
		m.lastCfg = m.settings.snap
		settings = m.settings.edit
		act = ActionSettingsPreview
	case CloseSettingsMsg:
		if m.SettingsOpen {
			settings = m.settings.snap
			act = ActionSettingsCancel
		}
		m.SettingsOpen = false
	case OpenConfirmQuitMsg:
		m.PaletteOpen = false
		m.SettingsOpen = false
		m.ConfirmOpen = true
		m.confirm = confirmState{
			title:     "Quit suzuri?",
			body:      "This is the last tab. Close the window?",
			yesLabel:  "Quit",
			noLabel:   "Cancel",
			yesAction: ActionQuit,
		}
	case DismissOverlayMsg:
		if m.SettingsOpen {
			settings = m.settings.snap
			act = ActionSettingsCancel
		}
		m.PaletteOpen = false
		m.SettingsOpen = false
		m.ConfirmOpen = false
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.palette.SetWidth(min(56, msg.Width-4))
		m.palette.SetHeight(min(10, msg.Height-2))
	case tea.KeyMsg:
		if m.ConfirmOpen {
			switch msg.String() {
			case "enter", "y":
				act = m.confirm.yesAction
				m.ConfirmOpen = false
			case "esc", "n", "ctrl+c":
				m.ConfirmOpen = false
			}
			return Result{Model: m, Action: act, Index: idx, Settings: settings}
		}
		if m.SettingsOpen {
			return m.updateSettingsKey(msg)
		}
		if m.PaletteOpen {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.PaletteOpen = false
			case "enter":
				if it, ok := m.palette.SelectedItem().(paletteItem); ok {
					act = it.action
					m.PaletteOpen = false
					if act == ActionOpenSettings {
						m.SettingsOpen = true
						m.settings = newSettingsState(m.lastCfg)
						settings = m.settings.edit
						act = ActionSettingsPreview
					}
				}
			default:
				var cmd tea.Cmd
				m.palette, cmd = m.palette.Update(msg)
				_ = cmd
			}
			return Result{Model: m, Action: act, Index: idx, Settings: settings}
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
		case key.Matches(msg, DefaultKeys.Settings):
			m.SettingsOpen = true
			m.settings = newSettingsState(m.lastCfg)
			settings = m.settings.edit
			act = ActionSettingsPreview
		case key.Matches(msg, DefaultKeys.Quit):
			act = ActionQuit
		}
	}

	return Result{Model: m, Action: act, Index: idx, Settings: settings}
}

func (m Model) updateSettingsKey(msg tea.KeyMsg) Result {
	var act HostAction
	var settings config.Config
	switch msg.String() {
	case "esc", "ctrl+c":
		settings = m.settings.snap
		m.SettingsOpen = false
		act = ActionSettingsCancel
	case "enter":
		settings = config.Normalize(m.settings.edit)
		m.lastCfg = settings
		m.SettingsOpen = false
		act = ActionSettingsApply
	case "up", "k":
		m.settings.moveField(-1)
	case "down", "j":
		m.settings.moveField(1)
	case "left", "h":
		m.settings.nudge(-1)
		settings = config.Normalize(m.settings.edit)
		act = ActionSettingsPreview
	case "right", "l":
		m.settings.nudge(1)
		settings = config.Normalize(m.settings.edit)
		act = ActionSettingsPreview
	}
	return Result{Model: m, Action: act, Settings: settings}
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

// TabStripRows is the height of the neon tab-card strip only.
func TabStripRows() int { return 3 }

// StripView is tab strip (+ optional status) — always used for chrome strip paint.
func (m Model) StripView() string {
	w := m.Width
	if w < 20 {
		w = 20
	}
	tabs, _, _ := m.layoutTabCards(w)
	if m.showStatus() {
		return tabs + "\n" + m.renderStatus(w)
	}
	return tabs
}

// OverlayView is the floating palette/settings/confirm card (empty if closed).
func (m Model) OverlayView() string {
	w := m.Width
	if w < 20 {
		w = 20
	}
	var card string
	switch {
	case m.ConfirmOpen:
		pw := min(40, max(30, w-10))
		card = m.confirm.render(pw)
	case m.SettingsOpen:
		pw := min(44, max(32, w-8))
		card = m.settings.render(pw)
	case m.PaletteOpen:
		pw := min(48, max(30, w-10))
		m.palette.SetWidth(pw - 4)
		m.palette.SetHeight(8)
		inner := m.palette.View()
		card = stylePaletteBorder().Width(pw).Render(inner)
	default:
		return ""
	}
	return lipgloss.PlaceHorizontal(w, lipgloss.Center, card,
		lipgloss.WithWhitespaceBackground(colVoid),
		lipgloss.WithWhitespaceForeground(colMute))
}

// View is strip only (overlay is composited separately by the host).
func (m Model) View() string {
	return m.StripView()
}

func (m Model) layoutTabCards(w int) (string, [][2]int, [2]int) {
	bounds := make([][2]int, len(m.Tabs))
	var parts []string
	var plusB [2]int

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

	parts = append(parts, gap)
	col += gapW
	plus := stylePlus().Render("+")
	pw := lipgloss.Width(plus)
	plusB = [2]int{col, col + pw}
	parts = append(parts, plus)

	row := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	strip := lipgloss.NewStyle().
		Background(colBar).
		Width(w).
		Padding(0, 0).
		Render(row)

	lines := strings.Split(strip, "\n")
	for i, ln := range lines {
		lw := lipgloss.Width(ln)
		if lw < w {
			lines[i] = ln + styleGap().Render(strings.Repeat(" ", w-lw))
		} else if lw > w {
			lines[i] = lipgloss.NewStyle().MaxWidth(w).Render(ln)
		}
	}
	return strings.Join(lines, "\n"), bounds, plusB
}

func (m Model) showStatus() bool {
	s := strings.TrimSpace(m.Status)
	return s != "" && s != "ready"
}

func (m Model) renderStatus(w int) string {
	return styleStatus().Width(w).Render(m.Status)
}

// TabBounds matches layoutTabCards.
func (m Model) TabBounds() [][2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, bounds, _ := m.layoutTabCards(w)
	return bounds
}

// PlusBounds is [startCol,endCol) of the "+" new-tab chip.
func (m Model) PlusBounds() [2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, _, plus := m.layoutTabCards(w)
	return plus
}

// RowCount is strip rows only (overlay floats over the shell).
func (m Model) RowCount() int {
	n := TabStripRows()
	if m.showStatus() {
		n++
	}
	return n
}

// OverlayRowCount estimates rows for the floating card paint.
func (m Model) OverlayRowCount() int {
	if m.ConfirmOpen {
		return 10
	}
	if m.SettingsOpen {
		return 14
	}
	if m.PaletteOpen {
		return 12
	}
	return 0
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
