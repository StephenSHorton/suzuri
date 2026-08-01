package chrome

// Command is a palette entry backed by a host action.
type Command struct {
	ID     string
	Title  string
	Desc   string
	Action HostAction
}

// DefaultCommands is the command registry for the palette.
func DefaultCommands() []Command {
	return []Command{
		{ID: "settings", Title: "Settings", Desc: "Ctrl+,", Action: ActionOpenSettings},
		{ID: "new_tab", Title: "New tab", Desc: "Ctrl+Shift+T", Action: ActionNewTab},
		{ID: "close_tab", Title: "Close tab", Desc: "Ctrl+W", Action: ActionCloseTab},
		{ID: "next_tab", Title: "Next tab", Desc: "Ctrl+Tab", Action: ActionNextTab},
		{ID: "prev_tab", Title: "Previous tab", Desc: "Ctrl+Shift+Tab", Action: ActionPrevTab},
	}
}
