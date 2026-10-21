package chrome

import "fmt"

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
	// TabID set for ActionMovePaneToTab / ActionMergeTabInto (target Tab.ID).
	TabID int
}

// DefaultCommands is the command registry (categories shown in descriptions).
// Shortcut labels use KeyCtrl / KeyCtrlShift so macOS shows Cmd and Windows Ctrl.
func DefaultCommands(activeProfile string, profileNames []string) []Command {
	focusHint := KeyCtrlAlt("←→↑↓") + " · Panes"
	moveHint := KeyMovePane("←→↑↓") + " · swap with the neighbour · or drag the title bar · Panes"
	splitRight := KeyCtrlShift("D") + " · Panes"
	splitDown := KeyCtrlShift("E") + " · Panes"
	if !isDarwin() {
		splitRight = "Alt+Shift+= · " + KeyCtrlShift("D") + " · Panes"
		splitDown = "Alt+Shift+- · " + KeyCtrlShift("E") + " · Panes"
	}
	cmds := []Command{
		{ID: "settings", Title: "Settings", Desc: KeyCtrl(",") + " · Appearance", Category: "Appearance", Action: ActionOpenSettings},
		{ID: "zoom_in", Title: "Zoom in", Desc: KeyCtrl("+") + " · larger UI font · Appearance", Category: "Appearance", Action: ActionZoomIn},
		{ID: "zoom_out", Title: "Zoom out", Desc: KeyCtrl("-") + " · smaller UI font · Appearance", Category: "Appearance", Action: ActionZoomOut},
		{ID: "zoom_reset", Title: "Reset zoom", Desc: KeyCtrl("0") + " · default font size · Appearance", Category: "Appearance", Action: ActionZoomReset},
		{ID: "replay_intro", Title: "Replay intro", Desc: "Play startup curtain again · Appearance", Category: "Appearance", Action: ActionReplayIntro},
		{ID: "check_updates", Title: "Check for updates", Desc: "GitHub Releases · System", Category: "System", Action: ActionCheckUpdates},
		{ID: "notifications", Title: "Notifications", Desc: "Session history · bell · System", Category: "System", Action: ActionOpenNotifications},
		{ID: "caffeine_toggle", Title: "Toggle caffeine", Desc: "☕ strip · prevent sleep · System", Category: "System", Action: ActionCaffeineToggle},
		{ID: "caffeine_15", Title: "Caffeine 15 minutes", Desc: "Stay awake 15m · System", Category: "System", Action: ActionCaffeineFor, Minutes: 15},
		{ID: "caffeine_1h", Title: "Caffeine 1 hour", Desc: "Stay awake 1h · System", Category: "System", Action: ActionCaffeineFor, Minutes: 60},
		{ID: "caffeine_off", Title: "Caffeine off", Desc: "Allow sleep · System", Category: "System", Action: ActionCaffeineOff},
		{ID: "help", Title: "Keyboard shortcuts", Desc: KeyCtrl("/") + " · Help", Category: "Help", Action: ActionOpenHelp},
		{ID: "notes", Title: "Notes", Desc: KeyCtrlShift("M") + " · List + editor (saved)", Category: "Notes", Action: ActionOpenNotes},
		{ID: "workspace", Title: "Workspace", Desc: "Shared channels · humans + AIs", Category: "Workspace", Action: ActionOpenWorkspace},
		{ID: "transfer_send", Title: "Send file (ticket)…", Desc: "P2P · Transfer", Category: "Transfer", Action: ActionOpenTransferSend},
		{ID: "transfer_receive", Title: "Receive ticket…", Desc: "P2P · Transfer", Category: "Transfer", Action: ActionOpenTransferReceive},
		{ID: "new_tab", Title: "New tab", Desc: KeyCtrlShift("T") + " · Tabs", Category: "Tabs", Action: ActionNewTab},
		{ID: "new_window", Title: "New window", Desc: KeyCtrlShift("N") + " · Window", Category: "Window", Action: ActionNewWindow},
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
		Command{ID: "close_tab", Title: "Close tab", Desc: "Strip × · palette · Tabs", Category: "Tabs", Action: ActionCloseTab},
		Command{ID: "rename_tab", Title: "Rename tab", Desc: "Double-click tab · custom strip name · Tabs", Category: "Tabs", Action: ActionOpenRenameTab},
		Command{ID: "next_tab", Title: "Next tab", Desc: KeyCtrlShift("→") + " · " + KeyCtrlShift("]") + " · " + KeyCtrl("Tab") + " · Tabs", Category: "Tabs", Action: ActionNextTab},
		Command{ID: "prev_tab", Title: "Previous tab", Desc: KeyCtrlShift("←") + " · " + KeyCtrlShift("[") + " · " + KeyCtrlShift("Tab") + " · Tabs", Category: "Tabs", Action: ActionPrevTab},
		Command{ID: "split_right", Title: "Split right", Desc: splitRight, Category: "Panes", Action: ActionSplitRight},
		Command{ID: "split_down", Title: "Split down", Desc: splitDown, Category: "Panes", Action: ActionSplitDown},
		Command{ID: "rename_pane", Title: "Rename pane", Desc: "F2 · double-click title · Panes", Category: "Panes", Action: ActionOpenRenamePane},
		Command{ID: "close_pane", Title: "Close pane", Desc: KeyCtrl("W") + " · Panes", Category: "Panes", Action: ActionClosePane},
		Command{ID: "focus_left", Title: "Focus pane left", Desc: focusHint, Category: "Panes", Action: ActionFocusPaneLeft},
		Command{ID: "focus_right", Title: "Focus pane right", Desc: focusHint, Category: "Panes", Action: ActionFocusPaneRight},
		Command{ID: "focus_up", Title: "Focus pane up", Desc: focusHint, Category: "Panes", Action: ActionFocusPaneUp},
		Command{ID: "focus_down", Title: "Focus pane down", Desc: focusHint, Category: "Panes", Action: ActionFocusPaneDown},
		Command{ID: "move_pane_left", Title: "Move pane left", Desc: moveHint, Category: "Panes", Action: ActionMovePaneLeft},
		Command{ID: "move_pane_right", Title: "Move pane right", Desc: moveHint, Category: "Panes", Action: ActionMovePaneRight},
		Command{ID: "move_pane_up", Title: "Move pane up", Desc: moveHint, Category: "Panes", Action: ActionMovePaneUp},
		Command{ID: "move_pane_down", Title: "Move pane down", Desc: moveHint, Category: "Panes", Action: ActionMovePaneDown},
		Command{ID: "zoom_pane", Title: "Maximize pane", Desc: KeyCtrlShift("Enter") + " · hide other panes · again to restore · Panes", Category: "Panes", Action: ActionTogglePaneZoom},
		Command{ID: "move_pane_prev_tab", Title: "Move pane to previous tab", Desc: KeyCtrlAltShift("←") + " · new tab if first · Panes", Category: "Panes", Action: ActionMovePanePrevTab},
		Command{ID: "move_pane_next_tab", Title: "Move pane to next tab", Desc: KeyCtrlAltShift("→") + " · new tab if last · Panes", Category: "Panes", Action: ActionMovePaneNextTab},
		Command{ID: "move_pane_new_tab", Title: "Move pane to new tab", Desc: "Break the focused pane out into its own tab · Panes", Category: "Panes", Action: ActionMovePaneToNewTab},
	)
	return cmds
}

// TabMoveCommands lists one "move pane to" and one "merge tab into" entry per
// other open tab, so typing "move" or "merge" plus a tab name finds them.
func TabMoveCommands(tabs []Tab, active int) []Command {
	if len(tabs) < 2 {
		return nil
	}
	var moves, merges []Command
	for i, t := range tabs {
		if i == active {
			continue
		}
		name := t.Title
		if name == "" {
			name = "shell"
		}
		label := fmt.Sprintf("%d %s", i+1, name)
		moves = append(moves, Command{
			ID: fmt.Sprintf("move_pane_to:%d", t.ID), Title: "Move pane to tab: " + label,
			Desc: "Focused pane joins that tab · Panes", Category: "Panes",
			Action: ActionMovePaneToTab, TabID: t.ID,
		})
		merges = append(merges, Command{
			ID: fmt.Sprintf("merge_tab_into:%d", t.ID), Title: "Merge tab into: " + label,
			Desc: "All panes of this tab join that tab · Tabs", Category: "Tabs",
			Action: ActionMergeTabInto, TabID: t.ID,
		})
	}
	return append(moves, merges...)
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
