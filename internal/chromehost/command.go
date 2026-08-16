package chromehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/config"
)

// CmdFile is the light control mailbox filename under the product config dir.
// Chrome polls this file every ~250ms, executes one-line commands, and truncates.
const CmdFile = "chrome_cmd"

// Known control commands (one line each, snake_case). Keep in sync with chrome
// `control_mailbox::ControlCommand`.
const (
	CmdQuit                = "quit"
	CmdOpenNotes           = "open_notes"
	CmdOpenWorkspace       = "open_workspace"
	CmdOpenGuest           = "open_guest"
	CmdOpenGuests          = "open_guests"
	CmdOpenPalette         = "open_palette"
	CmdOpenSettings        = "open_settings"
	CmdOpenTransferSend    = "open_transfer_send"
	CmdOpenTransferReceive = "open_transfer_receive"
	CmdOpenHelp            = "open_help"
	CmdNewTab              = "new_tab"
	CmdNewWindow           = "new_window"
	CmdToggleCaffeine      = "toggle_caffeine"
	CmdRefreshWorkspace    = "refresh_workspace"
)

// CmdPath is the absolute path of the chrome control mailbox
// (`{config.Dir()}/chrome_cmd`). When the host spawns chrome it sets
// SUZURI_CONFIG_DIR to the same directory so both sides share the path.
func CmdPath() string {
	return filepath.Join(config.Dir(), CmdFile)
}

// SendCommand writes a single control command for a running (or soon-to-start)
// suzuri-chrome process. The mailbox is overwrite semantics: the latest
// command wins. Chrome reads and truncates on its next poll.
//
// Fail soft for callers that do not care if chrome is absent — writing the
// file always succeeds if the config dir is writable; chrome simply picks it
// up when it next polls. Unknown commands return an error without writing.
//
// Default `suzuri` / `suzuri chrome` spawn does not require IPC; this is an
// optional control plane (HOST.md Phase 2).
func SendCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("chromehost: empty command")
	}
	if !ValidCommand(cmd) {
		return fmt.Errorf("chromehost: unknown command %q (want quit|open_notes|open_workspace|open_guest|open_guests|open_palette|open_settings|open_transfer_send|open_transfer_receive|open_help|new_tab|new_window|toggle_caffeine|refresh_workspace)", cmd)
	}

	path := CmdPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("chromehost: mkdir config dir: %w", err)
	}

	body := cmd + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return fmt.Errorf("chromehost: write mailbox: %w", err)
	}
	// Atomic replace. On Windows, rename over existing may fail — remove first.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("chromehost: install mailbox: %w", err2)
		}
	}
	return nil
}

// ValidCommand reports whether cmd is a known chrome control verb.
func ValidCommand(cmd string) bool {
	switch strings.TrimSpace(cmd) {
	case CmdQuit,
		CmdOpenNotes,
		CmdOpenWorkspace,
		CmdOpenGuest,
		CmdOpenGuests,
		CmdOpenPalette,
		CmdOpenSettings,
		CmdOpenTransferSend,
		CmdOpenTransferReceive,
		CmdOpenHelp,
		CmdNewTab,
		CmdNewWindow,
		CmdToggleCaffeine,
		CmdRefreshWorkspace:
		return true
	default:
		return false
	}
}
