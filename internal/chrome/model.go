// Package chrome is the Charm (Bubble Tea + Lip Gloss) UI layer for suzuri:
// neon rounded tab cards, command palette, and settings dialog.
// Shell content stays a VT cell grid in the host.
package chrome

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/StephenSHorton/suzuri/internal/textedit"
	"github.com/StephenSHorton/suzuri/internal/workspace"
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
	NotesOpen     bool
	WorkspaceOpen bool
	// Transfer: path/ticket prompt, then progress panel while engine runs.
	TransferPromptOpen bool
	TransferPanelOpen  bool
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
	// Transfer prompt + panel state.
	transferMode      TransferMode
	transferBuf       string
	transferPhase     string
	transferTicket    string
	transferDone      uint64
	transferTotal     uint64
	transferMsg       string
	transferDropHover bool   // OS file drag over window (send prompt)
	transferDropHint  string // short feedback under drop zone
	// Notes bank (persisted to notes.json; editor mirrors active note).
	notesBank   []NoteDoc
	notesActive int // index into notesBank
	notesFocus  notesFocus
	notesTitle  string // buffer while renaming title
	notesRunes  []rune
	notesCursor int
	notesSel    int // selection anchor; -1 = none (cursor is the other end)
	notesScroll int
	notesWrapW  int
	notesDirty  bool
	// notesHist is undo/redo for the body editor (shared textedit package).
	notesHist *textedit.History
	// notesLayout is filled by renderNotes for host click hit-testing.
	notesLayout notesLayout
	// Shared workspace panel (channels + messages; disk under …/workspace/).
	wsChannel   string
	wsChannels  []workspace.Channel
	wsMessages  []workspace.Message
	wsMembers   []workspace.Member
	wsCompose   string
	wsScroll    int // line offset from bottom (kept for simple scroll; viewport preferred)
	wsStatus    string
	wsHumanName string
	wsMode      wsInputMode
	// wsHist is undo/redo for the compose line (shared with notes).
	wsHist *textedit.History
	// wsVP is a Charm viewport used sync-only (SetContent/View/Scroll*; no tea.Cmd).
	wsVP       viewport.Model
	wsVPInit   bool
	wsStickBtm bool // pin scroll to latest messages after post/reload
	// lastCfg is the host's applied config (for reopening settings).
	lastCfg config.Config

	// Caffeine strip chip (host owns the power assertion; chrome only paints).
	CaffeineOn   bool
	CaffeineHint string // "" off, "∞" indefinite, or short remaining ("15m")
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
	// SyncCaffeineMsg updates the top-right coffee chip (host → chrome).
	SyncCaffeineMsg struct {
		Active bool
		Hint   string // "∞", "15m", or ""
	}
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
	// ActionDeleteNote is the confirm "yes" for interactive note delete.
	// Handled inside chrome (notesDeleteActive); host only needs to persist dirty bank.
	ActionDeleteNote
	// Zoom adjusts UI font size (host applies FontSizePx + persists).
	ActionZoomIn
	ActionZoomOut
	ActionZoomReset
	// Caffeine stay-awake (host owns IOPM / SetThreadExecutionState).
	// ActionCaffeineToggle flips indefinite on/off.
	// ActionCaffeineFor activates for Result.Minutes (0 = indefinite).
	// ActionCaffeineOff forces off.
	ActionCaffeineToggle
	ActionCaffeineFor
	ActionCaffeineOff
	// ActionOpenTransferSend / Receive: host opens the transfer path/ticket prompt.
	ActionOpenTransferSend
	ActionOpenTransferReceive
	// ActionTransferStart: Result.Name is path (send) or ticket (receive);
	// Result.TransferMode selects which.
	ActionTransferStart
	// ActionTransferCancel stops an in-flight engine process.
	ActionTransferCancel
	// ActionTransferCopyTicket copies the active ticket to the clipboard.
	ActionTransferCopyTicket
	// ActionOpenWorkspace opens the shared workspace panel (channels / chat).
	ActionOpenWorkspace
)

// Result pairs the new model with an optional host action.
type Result struct {
	Model        Model
	Action       HostAction
	Index        int
	Settings     config.Config // set for preview/apply/cancel
	ProfileName  string        // ActionNewTabProfile
	Name         string        // ActionApplyRename / ActionTransferStart
	RenameTarget RenameTarget  // ActionApplyRename
	TransferMode TransferMode  // ActionTransferStart
	// Minutes is set for ActionCaffeineFor (0 = indefinite).
	Minutes int
	// StartNotesDrag: NotesClickMsg hit the editor body — host should capture
	// the mouse and send NotesDragMsg while the button is held.
	StartNotesDrag bool
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
	minutes              int // ActionCaffeineFor
}

func (i paletteItem) Title() string       { return i.title }
func (i paletteItem) Description() string { return i.desc }
func (i paletteItem) FilterValue() string { return i.title + " " + i.desc }

func New(width int) Model {
	cfg := config.Default()
	m := Model{
		Width:     width,
		Height:    TabStripRows(),
		Status:    "",
		lastCfg:   cfg,
		notesSel:  -1,
		notesHist: textedit.NewHistory(200),
		wsHist:    textedit.NewHistory(100),
	}
	m.initNotesBank(defaultNotesBank())
	m.rebuildPalette()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// OverlayOpen is true when any modal owns keyboard focus.
func (m Model) OverlayOpen() bool {
	return m.PaletteOpen || m.SettingsOpen || m.ConfirmOpen || m.HelpOpen ||
		m.SplashOpen || m.RenameOpen || m.NotesOpen || m.WorkspaceOpen ||
		m.TransferPromptOpen || m.TransferPanelOpen
}

// TransferTicket returns the ticket shown on the progress panel (for copy).
func (m Model) TransferTicket() string { return m.transferTicket }

// UpdateChrome applies a message and returns host actions.
func (m Model) UpdateChrome(msg tea.Msg) Result {
	var act HostAction
	var idx int
	var settings config.Config
	var profileName string
	var renameName string
	var renameTarget RenameTarget
	var startNotesDrag bool
	var minutes int

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
	case SyncCaffeineMsg:
		m.CaffeineOn = msg.Active
		m.CaffeineHint = strings.TrimSpace(msg.Hint)
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
	case OpenTransferPromptMsg:
		m.openTransferPrompt(msg.Mode, msg.Seed)
	case TransferStatusMsg:
		m.applyTransferStatus(msg)
	case TransferDropHoverMsg:
		if m.AcceptsFileDrop() {
			m.transferDropHover = msg.Hover
			if !msg.Hover && m.transferDropHint == "drop target active" {
				m.transferDropHint = ""
			}
		} else {
			m.transferDropHover = false
		}
	case TransferDropPathsMsg:
		var dropPath string
		act, dropPath = m.applyTransferDropPaths(msg.Paths)
		if act == ActionTransferStart {
			return Result{Model: m, Action: act, Name: dropPath, TransferMode: TransferModeSend}
		}
	case CloseTransferMsg:
		m.TransferPromptOpen = false
		m.TransferPanelOpen = false
		m.transferBuf = ""
		m.transferDropHover = false
		m.transferDropHint = ""
	case OpenNotesMsg:
		m.openNotes()
	case ToggleNotesMsg:
		m.toggleNotes()
	case OpenWorkspaceMsg:
		m.openWorkspace()
	case ToggleWorkspaceMsg:
		if m.WorkspaceOpen {
			m.closeWorkspace()
		} else {
			m.openWorkspace()
		}
	case RefreshWorkspaceMsg:
		if m.WorkspaceOpen {
			m.reloadWorkspaceFromDisk()
		}
	case LoadNotesMsg:
		// Prefer disk bank; keep dirty flag clear after load.
		if len(msg.Bank.Notes) > 0 || msg.Bank.ActiveID != "" {
			m.initNotesBank(msg.Bank)
			m.notesDirty = false
		}
	case NotesDeleteMsg:
		// Force (MCP/agents): delete immediately. Interactive host: confirm first.
		if msg.Force || !m.NotesOpen {
			m.notesDeleteActive()
		} else {
			m.openConfirmDeleteNote()
		}
	case NotesClickMsg:
		if m.NotesOpen {
			cols := msg.Cols
			if cols < 20 {
				cols = m.Width
			}
			m.computeNotesLayout(cols)
			n := msg.ClickCount
			if n < 1 {
				n = 1
			}
			startNotesDrag = m.handleNotesClick(msg.CellX, msg.CellY, cols, n)
		}
	case NotesDragMsg:
		if m.NotesOpen {
			cols := msg.Cols
			if cols < 20 {
				cols = m.Width
			}
			m.computeNotesLayout(cols)
			m.handleNotesDrag(msg.CellX, msg.CellY, cols)
		}
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
		// Transfer: dismiss UI; host should cancel via ActionTransferCancel if active.
		wasTransfer := m.TransferPanelOpen
		m.TransferPromptOpen = false
		m.TransferPanelOpen = false
		m.transferBuf = ""
		m.transferDropHover = false
		m.transferDropHint = ""
		if wasTransfer {
			act = ActionTransferCancel
		}
		// Notes: put away only — flush active body into bank (host persists).
		m.putAwayNotes()
		m.WorkspaceOpen = false
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		if msg.Height > 0 {
			m.Height = msg.Height
		}
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
				// Note delete is handled in-model so notes stay open; host only persists.
				if m.confirm.yesAction == ActionDeleteNote {
					m.notesDeleteActive()
					act = ActionNone
				}
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
		if m.TransferPromptOpen {
			var val string
			act, val = m.handleTransferPromptKey(msg)
			return Result{Model: m, Action: act, Name: val, TransferMode: m.transferMode}
		}
		if m.TransferPanelOpen {
			act = m.handleTransferPanelKey(msg)
			return Result{Model: m, Action: act, Name: m.transferTicket}
		}
		if m.NotesOpen {
			m.computeNotesLayout(m.Width)
			m.handleNotesKey(msg)
			return Result{Model: m}
		}
		if m.WorkspaceOpen {
			m.handleWorkspaceKey(msg)
			return Result{Model: m}
		}
		if m.PaletteOpen {
			// Sync filter/nav — never tea.Cmd (Win32 key path must not block).
			act, profileName, minutes = m.handlePaletteKey(msg)
			if act == ActionOpenSettings && m.SettingsOpen {
				settings = m.settings.edit
			}
			if act == ActionOpenNotes {
				m.openNotes()
				act = ActionNone
			}
			if act == ActionOpenWorkspace {
				m.openWorkspace()
				act = ActionNone
			}
			if act == ActionOpenTransferSend {
				m.openTransferPrompt(TransferModeSend, "")
				act = ActionNone
			}
			if act == ActionOpenTransferReceive {
				m.openTransferPrompt(TransferModeReceive, "")
				act = ActionNone
			}
			return Result{Model: m, Action: act, Index: idx, Settings: settings, ProfileName: profileName, Minutes: minutes}
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

	return Result{
		Model: m, Action: act, Index: idx, Settings: settings,
		ProfileName: profileName, StartNotesDrag: startNotesDrag, Minutes: minutes,
	}
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
	if keep != "transfer_prompt" {
		m.TransferPromptOpen = false
		m.transferBuf = ""
		m.transferDropHover = false
		m.transferDropHint = ""
	}
	// Transfer progress panel is long-lived: leave open when other modals open
	// so an in-flight send keeps showing the ticket.
	if keep != "notes" {
		if m.NotesOpen {
			m.flushActiveNote()
		}
		m.NotesOpen = false // bank kept
	}
	if keep != "workspace" {
		m.WorkspaceOpen = false
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
	// Approximate fixed strip chrome in cells: brand 硯 (2) + plus chip +
	// caffeine cup on the right + gaps.
	const brandW = 2
	const plusW = 3
	const cafeW = 6 // "☕" + optional hint + padding
	const stateW = 2 // glyph + space (worst case)
	const padW = 4   // lipgloss Padding(0, 2) each side
	gaps := nTabs + 1
	fixed := brandW + plusW + cafeW + gaps + 2
	remain := stripW - fixed
	if remain < nTabs*(stateW+padW+6) {
		remain = nTabs * (stateW + padW + 6)
	}
	per := remain/nTabs - stateW - padW
	if nTabs == 1 {
		// Single tab: allow a long title instead of the old hard 18-char clip.
		per = stripW - brandW - plusW - cafeW - gaps - stateW - padW - 2
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
	tabs, _, _, _ := m.layoutTabCards(w)
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
	case m.TransferPromptOpen:
		card = m.renderTransferPrompt(w)
	case m.TransferPanelOpen:
		card = m.renderTransferPanel(w)
	case m.NotesOpen:
		// Interactive notes modal + borderless keys caption (gap via MarginTop).
		main := m.renderNotes(w)
		keys := m.renderNotesContextKeys(lipgloss.Width(main), w)
		card = lipgloss.JoinVertical(lipgloss.Center, main, keys)
	case m.WorkspaceOpen:
		card = m.renderWorkspace(w)
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

func (m Model) layoutTabCards(w int) (string, [][2]int, [2]int, [2]int) {
	bounds := make([][2]int, len(m.Tabs))
	var parts []string
	var plusB, cafeB [2]int

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
	col += pw

	// Left cluster (brand · tabs · +). Cup sits on the far right.
	left := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	leftW := lipgloss.Width(left)

	cup := m.renderCaffeineChip()
	cupW := lipgloss.Width(cup)
	if cupW < 1 {
		cupW = 2
	}

	// Spacer between + and the coffee chip; keep at least one gap cell when possible.
	spacerW := w - leftW - cupW
	if spacerW < 1 {
		// Collapse tabs if the strip is too tight for a right-aligned cup.
		maxLeft := w - cupW - 1
		if maxLeft < 8 {
			maxLeft = 8
		}
		if leftW > maxLeft {
			left = lipgloss.NewStyle().MaxWidth(maxLeft).Background(colBar).Render(left)
			leftW = lipgloss.Width(left)
		}
		spacerW = w - leftW - cupW
		if spacerW < 0 {
			spacerW = 0
		}
	}
	spacer := styleGap().Render(strings.Repeat(" ", spacerW))
	cafeB = [2]int{leftW + spacerW, leftW + spacerW + cupW}

	row := left + spacer + cup
	// Guarantee full-width bar surface (clip only if still over).
	lw := lipgloss.Width(row)
	if lw < w {
		row = row + styleGap().Render(strings.Repeat(" ", w-lw))
	} else if lw > w {
		row = lipgloss.NewStyle().MaxWidth(w).Background(colBar).Render(row)
		// If clipped, drop a usable hit target at the extreme right.
		if cafeB[1] > w {
			cafeB[0] = w - cupW
			if cafeB[0] < 0 {
				cafeB[0] = 0
			}
			cafeB[1] = w
		}
	}
	return styleBar().Width(w).Render(row), bounds, plusB, cafeB
}

// renderCaffeineChip is the top-right coffee control (empty dim / full bright).
func (m Model) renderCaffeineChip() string {
	// HOT BEVERAGE U+2615 — reads as a cup in mono Nerd Font faces.
	const cup = "☕"
	label := cup
	if m.CaffeineOn {
		hint := strings.TrimSpace(m.CaffeineHint)
		if hint != "" && hint != "∞" {
			label = cup + " " + hint
		}
		return styleCaffeineOn().Render(label)
	}
	return styleCaffeineOff().Render(cup)
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
	_, bounds, _, _ := m.layoutTabCards(w)
	return bounds
}

// PlusBounds is [startCol,endCol) of the "+" new-tab chip.
func (m Model) PlusBounds() [2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, _, plus, _ := m.layoutTabCards(w)
	return plus
}

// CaffeineBounds is [startCol,endCol) of the top-right coffee chip.
func (m Model) CaffeineBounds() [2]int {
	w := m.Width
	if w < 20 {
		w = 20
	}
	_, _, _, cafe := m.layoutTabCards(w)
	return cafe
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
		// Two-column compact card; actual height from lipgloss.
		return 26
	case m.ConfirmOpen:
		return 10
	case m.SettingsOpen:
		// Main settings card + gap + help card under it.
		return 26
	case m.RenameOpen:
		return 8
	case m.NotesOpen:
		// List/editor card + gap + contextual keys companion.
		return notesListRows + 18
	case m.WorkspaceOpen:
		return m.WorkspaceOverlayRows()
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
