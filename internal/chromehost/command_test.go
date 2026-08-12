package chromehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidCommand(t *testing.T) {
	for _, cmd := range []string{
		CmdQuit,
		CmdOpenNotes,
		CmdOpenWorkspace,
		CmdOpenPalette,
		CmdOpenSettings,
		CmdOpenTransferSend,
		CmdOpenTransferReceive,
		CmdOpenHelp,
		CmdNewTab,
		CmdNewWindow,
		CmdToggleCaffeine,
		CmdRefreshWorkspace,
	} {
		if !ValidCommand(cmd) {
			t.Fatalf("expected valid: %s", cmd)
		}
	}
	if ValidCommand("nope") {
		t.Fatal("unknown should be invalid")
	}
	if ValidCommand("") {
		t.Fatal("empty should be invalid")
	}
	if !ValidCommand("  quit  ") {
		t.Fatal("trim should accept quit")
	}
	if ValidCommand("OpenSettings") {
		t.Fatal("camelCase should be invalid (snake_case wire only)")
	}
}

func TestSendCommandWritesMailbox(t *testing.T) {
	dir := t.TempDir()
	// Point product config dir at the temp tree via LOCALAPPDATA (config.Dir
	// prefers it on all platforms when set — see internal/config.Dir).
	t.Setenv("LOCALAPPDATA", dir)
	// Clear XDG / home side paths are unused when LOCALAPPDATA is set.

	wantPath := filepath.Join(dir, "suzuri", CmdFile)
	if err := SendCommand(CmdOpenNotes); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	got := strings.TrimSpace(string(body))
	if got != CmdOpenNotes {
		t.Fatalf("mailbox body %q want %q", got, CmdOpenNotes)
	}

	// Overwrite with quit.
	if err := SendCommand(CmdQuit); err != nil {
		t.Fatalf("SendCommand quit: %v", err)
	}
	body, err = os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read mailbox: %v", err)
	}
	if strings.TrimSpace(string(body)) != CmdQuit {
		t.Fatalf("after overwrite got %q", body)
	}
}

func TestSendCommandRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if err := SendCommand("explode"); err == nil {
		t.Fatal("expected error for unknown command")
	}
	// Must not create mailbox for rejected commands.
	path := filepath.Join(dir, "suzuri", CmdFile)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("mailbox should not exist after rejected command")
	}
}

func TestSendCommandEmpty(t *testing.T) {
	if err := SendCommand("   "); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestCmdPathUsesConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	got := CmdPath()
	want := filepath.Join(dir, "suzuri", CmdFile)
	if got != want {
		t.Fatalf("CmdPath = %q want %q", got, want)
	}
}
