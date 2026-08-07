package chrome

// Command is a palette entry backed by a host action.
type Command struct {
	ID       string
	Title    string
	Desc     string
	Category string // Tabs · Appearance · Help
	Action   HostAction
	// ProfileName set for ActionNewTabProfile.
	ProfileName string
	// Minutes set for ActionCaffeineFor (0 = indefinite).
	Minutes int
}

// DefaultCommands is the command registry (categories shown in descriptions).
func DefaultCommands(activeProfile string, profileNames []string) []Command {
	cmds := []Command{
		{ID: "settings", Title: "Settings", Desc: "Ctrl+, · Appearance", Category: "Appearance", Action: ActionOpenSettings},
		{ID: "zoom_in", Title: "Zoom in", Desc: "⌘+ / Ctrl++ · larger UI font · Appearance", Category: "Appearance", Action: ActionZoomIn},
		{ID: "zoom_out", Title: "Zoom out", Desc: "⌘- / Ctrl+- · smaller UI font · Appearance", Category: "Appearance", Action: ActionZoomOut},
		{ID: "zoom_reset", Title: "Reset zoom", Desc: "⌘0 / Ctrl+0 · default font size · Appearance", Category: "Appearance", Action: ActionZoomReset},
		{ID: "replay_intro", Title: "Replay intro", Desc: "Play startup curtain again · Appearance", Category: "Appearance", Action: ActionReplayIntro},
		{ID: "check_updates", Title: "Check for updates", Desc: "GitHub Releases · System", Category: "System", Action: ActionCheckUpdates},
		{ID: "caffeine_toggle", Title: "Toggle caffeine", Desc: "☕ strip · prevent sleep · System", Category: "System", Action: ActionCaffeineToggle},
		{ID: "caffeine_15", Title: "Caffeine 15 minutes", Desc: "Stay awake 15m · System", Category: "System", Action: ActionCaffeineFor, Minutes: 15},
		{ID: "caffeine_1h", Title: "Caffeine 1 hour", Desc: "Stay awake 1h · System", Category: "System", Action: ActionCaffeineFor, Minutes: 60},
		{ID: "caffeine_off", Title: "Caffeine off", Desc: "Allow sleep · System", Category: "System", Action: ActionCaffeineOff},
		{ID: "help", Title: "Keyboard shortcuts", Desc: "Ctrl+/ · Help", Category: "Help", Action: ActionOpenHelp},
		{ID: "notes", Title: "Notes", Desc: "Ctrl+Shift+M · List + editor (saved)", Category: "Notes", Action: ActionOpenNotes},
		{ID: "workspace", Title: "Workspace", Desc: "Shared channels · humans + AIs", Category: "Workspace", Action: ActionOpenWorkspace},
		{ID: "transfer_send", Title: "Send file (ticket)…", Desc: "P2P · Transfer", Category: "Transfer", Action: ActionOpenTransferSend},
		{ID: "transfer_receive", Title: "Receive ticket…", Desc: "P2P · Transfer", Category: "Transfer", Action: ActionOpenTransferReceive},
		{ID: "new_tab", Title: "New tab", Desc: "Ctrl+Shift+T · Tabs", Category: "Tabs", Action: ActionNewTab},
		{ID: "new_window", Title: "New window", Desc: "Ctrl+Shift+N · Window", Category: "Window", Action: ActionNewWindow},
	}
	for _, name := range profileNames {
		if name == "" {
			continue
		}
		label := "New tab: " + name
		if activeProfile != "" && equalFold(name, activeProfile) {
			label += " ★"
		}
		cmds = append(cmds, Command{
			ID:          "profile:" + name,
			Title:       label,
			Desc:        "Tabs · profile",
			Category:    "Tabs",
			Action:      ActionNewTabProfile,
			ProfileName: name,
		})
	}
	cmds = append(cmds,
		Command{ID: "close_tab", Title: "Close tab", Desc: "Ctrl+W · Tabs", Category: "Tabs", Action: ActionCloseTab},
		Command{ID: "rename_tab", Title: "Rename tab", Desc: "Double-click tab · custom strip name · Tabs", Category: "Tabs", Action: ActionOpenRenameTab},
		Command{ID: "next_tab", Title: "Next tab", Desc: "Ctrl+Tab · Tabs", Category: "Tabs", Action: ActionNextTab},
		Command{ID: "prev_tab", Title: "Previous tab", Desc: "Ctrl+Shift+Tab · Tabs", Category: "Tabs", Action: ActionPrevTab},
		Command{ID: "split_right", Title: "Split right", Desc: "Alt+Shift+= · Ctrl+Shift+D · Panes", Category: "Panes", Action: ActionSplitRight},
		Command{ID: "split_down", Title: "Split down", Desc: "Alt+Shift+- · Ctrl+Shift+E · Panes", Category: "Panes", Action: ActionSplitDown},
		Command{ID: "rename_pane", Title: "Rename pane", Desc: "F2 · double-click title · Panes", Category: "Panes", Action: ActionOpenRenamePane},
		Command{ID: "close_pane", Title: "Close pane", Desc: "Ctrl+Shift+W · Panes", Category: "Panes", Action: ActionClosePane},
		Command{ID: "focus_left", Title: "Focus pane left", Desc: "⌘⌥+← · Alt+← · Panes", Category: "Panes", Action: ActionFocusPaneLeft},
		Command{ID: "focus_right", Title: "Focus pane right", Desc: "⌘⌥+→ · Alt+→ · Panes", Category: "Panes", Action: ActionFocusPaneRight},
		Command{ID: "focus_up", Title: "Focus pane up", Desc: "⌘⌥+↑ · Alt+↑ · Panes", Category: "Panes", Action: ActionFocusPaneUp},
		Command{ID: "focus_down", Title: "Focus pane down", Desc: "⌘⌥+↓ · Alt+↓ · Panes", Category: "Panes", Action: ActionFocusPaneDown},
	)
	return cmds
}

func equalFold(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
