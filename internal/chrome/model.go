// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// neon rounded tab cards, command palette, and settings dialog.
// Shell content stays a VT cell grid in the host.
package chrome

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// Tab is a chrome-level tab descriptor (no PTY — host owns sessions).
type Tab struct {
	ID    int
	Title string
	// Session state for the strip glyph (host fills these on SyncTabs).
	Alive     bool // PTY still live
	AltScreen bool // fullscreen TUI (Grok, vim, less, …)
	Busy      bool // activity spinner (PTY I/O and/or OSC title spinner)
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
	HelpOpen     bool
	SplashOpen   bool
	RenameOpen   bool
	NotesOpen    bool
	// Sync palette (no bubbles list / tea.Cmd — host key path must stay non-blocking).
	palAll    []paletteItem
	palView   []paletteItem
	palFilter string
	palIndex  int
	settings  settingsState
	confirm   confirmState
	// Rename dialog (sync, same key path as palette).
	renameTarget RenameTarget
	renameBuf    string
	// Scratch notes (in-memory only; survives hide/show until process exit).
	notesRunes  []rune
	notesCursor int
	notesScroll int
	notesWrapW  int
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
	// OpenConfirmUpdateMsg asks before installing a GitHub release.
	OpenConfirmUpdateMsg struct {
		Version string // without leading v
	}
	// OpenHelpMsg shows keyboard shortcuts.
	OpenHelpMsg struct{}
	// OpenSplashMsg shows first-run welcome (once).
	OpenSplashMsg struct{}
	// DismissOverlayMsg closes palette/settings/confirm without side effects
	// (except settings cancel restores snap via ActionSettingsCancel).
	DismissOverlayMsg struct{}
)

// HostAction is returned to the Win32 host after UpdateChrome.
type HostAction int

const (
	ActionNone HostAction = iota
	ActionNewTab
	ActionNewTabProfile // Result.ProfileName set
	ActionNewWindow     // spawn a second OS window (new process)
	ActionCloseTab
	ActionSelectTab
	ActionNextTab
	ActionPrevTab
	ActionQuit
	ActionOpenSettings
	ActionOpenHelp
	// ActionSettingsPreview: live-apply Settings config (do not persist).
	ActionSettingsPreview
	// ActionSettingsApply: persist Settings and close dialog.
	ActionSettingsApply
	// ActionSettingsCancel: restore Settings.snap and close dialog.
	ActionSettingsCancel
	// ActionSplashDone: mark first-run complete and persist.
	ActionSplashDone
	// ActionReplayIntro re-runs the configured startup curtain (matrix/ripple/none).
	ActionReplayIntro
	// ActionCheckUpdates queries GitHub Releases (host may open confirm).
	ActionCheckUpdates
	// ActionInstallUpdate downloads and applies a pending update (after confirm).
	ActionInstallUpdate
	// ActionUpdateLater: user dismissed the update confirm (do not re-offer this session).
	ActionUpdateLater
	// Split panes (host implements layout).
	ActionSplitRight
	ActionSplitDown
	ActionClosePane
	ActionFocusPaneLeft
	ActionFocusPaneRight
	ActionFocusPaneUp
	ActionFocusPaneDown
	// ActionOpenRenamePane / Tab: host opens OpenRenameMsg with a seed name.
	ActionOpenRenamePane
	ActionOpenRenameTab
	// ActionApplyRename: Result.Name is the new title (empty clears custom name).
	// Result.RenameTarget is pane vs strip tab.
	ActionApplyRename
	// ActionOpenNotes opens the scratch notes surface (host may also toggle).
	ActionOpenNotes
)

// Result pairs the new model with an optional host action.
type Result struct {
	Model        Model
	Action       HostAction
	Index        int
	Settings     config.Config // set for preview/apply/cancel
	ProfileName  string        // ActionNewTabProfile
	Name         string        // ActionApplyRename
	RenameTarget RenameTarget  // ActionApplyRename
}

// KeyMap for chrome shortcuts the host may forward.
type KeyMap struct {
	NewTab    key.Binding
	NewWindow key.Binding
	CloseTab  key.Binding
	NextTab   key.Binding
	PrevTab   key.Binding
	Palette   key.Binding
	Settings  key.Binding
	Help      key.Binding
	Quit      key.Binding
	Notes     key.Binding
}

// DefaultKeys documents bindings (host also handles most of these).
var DefaultKeys = KeyMap{
	NewTab:    key.NewBinding(key.WithKeys("ctrl+shift+t"), key.WithHelp("ctrl+shift+t", "new tab")),
	NewWindow: key.NewBinding(key.WithKeys("ctrl+shift+n"), key.WithHelp("ctrl+shift+n", "new window")),
	CloseTab:  key.NewBinding(key.WithKeys("ctrl+w"), key.WithHelp("ctrl+w", "close tab")),
	NextTab:   key.NewBinding(key.WithKeys("ctrl+tab", "tab"), key.WithHelp("ctrl+tab", "next tab")),
	PrevTab:   key.NewBinding(key.WithKeys("ctrl+shift+tab", "shift+tab"), key.WithHelp("ctrl+shift+tab", "prev tab")),
	Palette:   key.NewBinding(key.WithKeys("ctrl+k", "ctrl+p"), key.WithHelp("ctrl+k", "palette")),
	Settings:  key.NewBinding(key.WithKeys("ctrl+,"), key.WithHelp("ctrl+,", "settings")),
	Help:      key.NewBinding(key.WithKeys("ctrl+/"), key.WithHelp("ctrl+/", "help")),
	Quit:      key.NewBinding(key.WithKeys("ctrl+shift+q"), key.WithHelp("ctrl+shift+q", "quit")),
	Notes:     key.NewBinding(key.WithKeys("ctrl+shift+m"), key.WithHelp("ctrl+shift+m", "notes")),
}

type paletteItem struct {
	title, desc, profile string
	action               HostAction
}

func (i paletteItem) Title() string       { return i.title }
func (i paletteItem) Description() string { return i.desc }
func (i paletteItem) FilterValue() string { return i.title + " " + i.desc }

func New(width int) Model {
	cfg := config.Default()
	m := Model{
		Width:  width,
		Height: TabStripRows(),
		Status: "",
		lastCfg: cfg,
	}
	m.rebuildPalette()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// OverlayOpen is true when any modal owns keyboard focus.
func (m Model) OverlayOpen() bool {
	return m.PaletteOpen || m.SettingsOpen || m.ConfirmOpen || m.HelpOpen ||
		m.SplashOpen || m.RenameOpen || m.NotesOpen
}

// UpdateChrome applies a message and returns host actions.
func (m Model) UpdateChrome(msg tea.Msg) Result {
	var act HostAction
	var idx int
	var settings config.Config
	var profileName string
	var renameName string
	var renameTarget RenameTarget

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
		// Avoid rebuilding the palette list while settings is open — live
		// preview sends SyncConfig on every left/right and was a source of
		// freezes/crashes (list model thrash + GDI font churn mid-dialog).
		if !m.SettingsOpen {
			m.rebuildPalette()
		}
	case OpenPaletteMsg:
		m.closeModalsExcept("")
		m.activatePalette()
	case ClosePaletteMsg:
		m.PaletteOpen = false
	case OpenSettingsMsg:
		m.closeModalsExcept("settings")
		m.SettingsOpen = true
		m.settings = newSettingsState(msg.Config)
		m.lastCfg = m.settings.snap
		// Do not fire SettingsPreview on open — config is already live.
		// Preview only when the user nudges a value (left/right).
	case CloseSettingsMsg:
		if m.SettingsOpen {
			settings = m.settings.snap
			act = ActionSettingsCancel
		}
		m.SettingsOpen = false
	case OpenConfirmQuitMsg:
		m.closeModalsExcept("confirm")
		m.ConfirmOpen = true
		m.confirm = confirmState{
			title:     "Quit suzuri?",
			body:      "This is the last tab. Close the window?",
			yesLabel:  "Quit",
			noLabel:   "Cancel",
			yesAction: ActionQuit,
		}
	case OpenConfirmUpdateMsg:
		m.closeModalsExcept("confirm")
		m.ConfirmOpen = true
		ver := strings.TrimPrefix(strings.TrimSpace(msg.Version), "v")
		if ver == "" {
			ver = "?"
		}
		m.confirm = confirmState{
			title:     "Update suzuri?",
			body:      fmt.Sprintf("v%s is available. Install and restart?", ver),
			yesLabel:  "Update",
			noLabel:   "Later",
			yesAction: ActionInstallUpdate,
		}
	case OpenHelpMsg:
		m.closeModalsExcept("help")
		m.HelpOpen = true
	case OpenSplashMsg:
		m.closeModalsExcept("splash")
		m.SplashOpen = true
	case OpenRenameMsg:
		m.openRename(msg.Target, msg.Seed)
	case OpenNotesMsg:
		m.openNotes()
	case ToggleNotesMsg:
		m.toggleNotes()
	case DismissOverlayMsg:
		if m.SettingsOpen {
			settings = m.settings.snap
			act = ActionSettingsCancel
		}
		if m.SplashOpen {
			act = ActionSplashDone
		}
		// Click-outside on update confirm = Later (don't re-pop this session).
		if m.ConfirmOpen && m.confirm.yesAction == ActionInstallUpdate {
			act = ActionUpdateLater
		}
		m.PaletteOpen = false
		m.SettingsOpen = false
		m.ConfirmOpen = false
		m.HelpOpen = false
		m.SplashOpen = false
		m.RenameOpen = false
		m.renameBuf = ""
		// Notes: put away only — keep notesRunes.
		m.NotesOpen = false
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	case tea.KeyMsg:
		if m.SplashOpen {
			if keyDismiss(msg) || msg.String() == " " {
				m.SplashOpen = false
				act = ActionSplashDone
			}
			return Result{Model: m, Action: act, Settings: settings}
		}
		if m.HelpOpen {
			if keyDismiss(msg) || msg.String() == "q" {
				m.HelpOpen = false
			}
			return Result{Model: m, Action: act, Settings: settings}
		}
		if m.ConfirmOpen {
			s := msg.String()
			if s == "enter" || s == "y" || msg.Type == tea.KeyEnter {
				act = m.confirm.yesAction
				m.ConfirmOpen = false
			} else if s == "esc" || s == "n" || s == "ctrl+c" || msg.Type == tea.KeyEsc {
				// Signal host that user deferred an update offer.
				if m.confirm.yesAction == ActionInstallUpdate {
					act = ActionUpdateLater
				}
				m.ConfirmOpen = false
			}
			return Result{Model: m, Action: act, Index: idx, Settings: settings}
		}
		if m.SettingsOpen {
			return m.updateSettingsKey(msg)
		}
		if m.RenameOpen {
			renameTarget = m.renameTarget
			act, renameName = m.handleRenameKey(msg)
			return Result{Model: m, Action: act, Name: renameName, RenameTarget: renameTarget}
		}
		if m.NotesOpen {
			// Update wrap width for vertical motion from current model width.
			inner := dialogInnerWidth(clampDialogWidth(56, m.Width))
			m.notesWrapW = inner - 2
			if m.notesWrapW < 8 {
				m.notesWrapW = 8
			}
			m.handleNotesKey(msg)
			return Result{Model: m}
		}
		if m.PaletteOpen {
			// Sync filter/nav — never tea.Cmd (Win32 key path must not block).
			act, profileName = m.handlePaletteKey(msg)
			if act == ActionOpenSettings && m.SettingsOpen {
				settings = m.settings.edit
			}
			if act == ActionOpenNotes {
				m.openNotes()
				act = ActionNone
			}
			return Result{Model: m, Action: act, Index: idx, Settings: settings, ProfileName: profileName}
		}
		switch {
		case key.Matches(msg, DefaultKeys.NewTab):
			act = ActionNewTab
		case key.Matches(msg, DefaultKeys.NewWindow):
			act = ActionNewWindow
		case key.Matches(msg, DefaultKeys.CloseTab):
			act = ActionCloseTab
		case key.Matches(msg, DefaultKeys.NextTab):
			act = ActionNextTab
		case key.Matches(msg, DefaultKeys.PrevTab):
			act = ActionPrevTab
		case key.Matches(msg, DefaultKeys.Palette):
			m.activatePalette()
		case key.Matches(msg, DefaultKeys.Settings):
			m.SettingsOpen = true
			m.settings = newSettingsState(m.lastCfg)
			settings = m.settings.edit
			act = ActionSettingsPreview
		case key.Matches(msg, DefaultKeys.Help):
			m.HelpOpen = true
		case key.Matches(msg, DefaultKeys.Notes):
			m.toggleNotes()
		case key.Matches(msg, DefaultKeys.Quit):
			act = ActionQuit
		}
	}

	return Result{Model: m, Action: act, Index: idx, Settings: settings, ProfileName: profileName}
}

func (m *Model) closeModalsExcept(keep string) {
	if keep != "palette" {
		m.PaletteOpen = false
	}
	if keep != "settings" {
		m.SettingsOpen = false
	}
	if keep != "confirm" {
		m.ConfirmOpen = false
	}
	if keep != "rename" {
		m.RenameOpen = false
		m.renameBuf = ""
	}
	if keep != "notes" {
		m.NotesOpen = false // buffer kept
	}
	if keep != "help" {
		m.HelpOpen = false
	}
	if keep != "splash" {
		m.SplashOpen = false
	}
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

// tabStateGlyph is a compact activity / mode marker before the title.
// Glyphs are chosen at runtime from the active font (see SetTabStateGlyphs):
// braille preferred, else geometric, else ASCII.
// Busy / alt-busy use an animated Spin cycle when the pack provides one.
func tabStateGlyph(t Tab) string {
	g := tabGlyphs()
	if !t.Alive {
		return g.Dead
	}
	if t.AltScreen {
		if t.Busy {
			return busyGlyph(g, true)
		}
		return g.AltIdle
	}
	if t.Busy {
		return busyGlyph(g, false)
	}
	return ""
}

// titleBudget is max title runes per tab given strip width and tab count.
// One tab may use most of the row; many tabs share the space.
func titleBudget(stripW, nTabs int) int {
	if nTabs < 1 {
		nTabs = 1
	}
	// Approximate fixed strip chrome in cells: brand 硯 (2) + plus chip + gaps.
	const brandW = 2
	const plusW = 3
	const stateW = 2 // glyph + space (worst case)
	const padW = 4   // lipgloss Padding(0, 2) each side
	gaps := nTabs + 1
	fixed := brandW + plusW + gaps + 2
	remain := stripW - fixed
	if remain < nTabs*(stateW+padW+6) {
		remain = nTabs * (stateW + padW + 6)
	}
	per := remain/nTabs - stateW - padW
	if nTabs == 1 {
		// Single tab: allow a long title instead of the old hard 18-char clip.
		per = stripW - brandW - plusW - gaps - stateW - padW - 2
	}
	if per < 8 {
		per = 8
	}
	if per > 72 {
		per = 72
	}
	return per
}

func tabLabel(t Tab, i int, maxRunes int) string {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		if t.AltScreen {
			title = fmt.Sprintf("app %d", i+1)
		} else {
			title = fmt.Sprintf("shell %d", i+1)
		}
	}
	title = strings.TrimPrefix(title, "Administrator: ")
	if maxRunes < 4 {
		maxRunes = 4
	}
	rs := []rune(title)
	if len(rs) > maxRunes {
		if maxRunes <= 1 {
			title = "…"
		} else {
			title = string(rs[:maxRunes-1]) + "…"
		}
	}
	return tabStateGlyph(t) + title
}

// TabStripRows is a single calm row (no 3-line rounded tab boxes).
func TabStripRows() int { return 1 }

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

// OverlayView is the floating card (empty if closed).
func (m Model) OverlayView() string {
	w := m.Width
	if w < 20 {
		w = 20
	}
	var card string
	switch {
	case m.SplashOpen:
		card = splashBody(w)
	case m.HelpOpen:
		card = helpBody(w)
	case m.ConfirmOpen:
		card = m.confirm.render(w)
	case m.SettingsOpen:
		card = m.settings.render(w)
	case m.RenameOpen:
		card = m.renderRename(w)
	case m.NotesOpen:
		card = m.renderNotes(w)
	case m.PaletteOpen:
		// Crush commands: outer min(70, area), inner = outer − frame.
		// Sync render — no bubbles list layout/filter on the key path.
		outer := clampDialogWidth(52, w)
		innerW := dialogInnerWidth(outer)
		if innerW < 16 {
			innerW = 16
		}
		body := []string{m.renderPalette(innerW)}
		footer := styleDialogHintKey().Render("up/down  ") +
			styleDialogHint().Render("enter run  ") +
			styleDialogHintKey().Render("esc")
		card = renderDialogCard(outer, "Commands", body, footer)
	default:
		return ""
	}
	// Center the card only — no whitespace background. Side gutters stay
	// transparent so the live shell (palette) or dim matte (settings/help)
	// shows left and right of the card.
	return lipgloss.PlaceHorizontal(w, lipgloss.Center, card)
}

// View is strip only (overlay is composited separately by the host).
func (m Model) View() string {
	return m.StripView()
}

func (m Model) layoutTabCards(w int) (string, [][2]int, [2]int) {
	bounds := make([][2]int, len(m.Tabs))
	var parts []string
	var plusB [2]int

	// Quiet brand — no bordered chip, no trailing gap before the first tab.
	brand := styleBrand().Render("硯")
	parts = append(parts, brand)
	col := lipgloss.Width(brand)

	gap := styleGap().Render(" ")
	gapW := lipgloss.Width(gap)

	if len(m.Tabs) == 0 {
		// Only pad when there is no real tab card to sit flush against 硯.
		parts = append(parts, gap, styleInactiveTab().Render("no tabs"))
	} else {
		budget := titleBudget(w, len(m.Tabs))
		for i, t := range m.Tabs {
			// Gaps between tabs only — first tab abuts the brand mark.
			if i > 0 {
				parts = append(parts, gap)
				col += gapW
			}
			label := tabLabel(t, i, budget)
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
	// Fill remaining width with bar bg so the strip is one continuous surface.
	lw := lipgloss.Width(row)
	if lw < w {
		row = row + styleGap().Render(strings.Repeat(" ", w-lw))
	} else if lw > w {
		row = lipgloss.NewStyle().MaxWidth(w).Background(colBar).Render(row)
	}
	return styleBar().Width(w).Render(row), bounds, plusB
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
	switch {
	case m.SplashOpen:
		return 14
	case m.HelpOpen:
		return 20
	case m.ConfirmOpen:
		return 10
	case m.SettingsOpen:
		// Main settings card + gap + help card under it.
		return 26
	case m.RenameOpen:
		return 8
	case m.NotesOpen:
		return notesVisRows + 6
	case m.PaletteOpen:
		return 14
	default:
		return 0
	}
}

func keyDismiss(msg tea.KeyMsg) bool {
	s := msg.String()
	return s == "esc" || s == "enter" || s == "ctrl+c" ||
		msg.Type == tea.KeyEsc || msg.Type == tea.KeyEnter
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
