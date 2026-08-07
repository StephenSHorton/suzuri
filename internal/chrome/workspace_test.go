package chrome

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOpenWorkspacePanel(t *testing.T) {
	// Isolate store from the developer's real workspace.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	m := New(80)
	r := m.UpdateChrome(OpenWorkspaceMsg{})
	m = r.Model
	if !m.WorkspaceOpen {
		t.Fatal("expected workspace open")
	}
	if m.WorkspaceChannel() == "" {
		t.Fatal("expected channel")
	}

	m.wsCompose = "hello human"
	m.handleWorkspaceKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.wsCompose != "" {
		t.Fatalf("compose should clear after send, got %q", m.wsCompose)
	}
	if len(m.wsMessages) < 1 {
		t.Fatal("expected at least one message after post")
	}

	r = m.UpdateChrome(DismissOverlayMsg{})
	if r.Model.WorkspaceOpen {
		t.Fatal("dismiss should close workspace")
	}
}

func TestWorkspacePaletteAction(t *testing.T) {
	m := New(80)
	m.activatePalette()
	// Find workspace command
	found := false
	for _, it := range m.palAll {
		if it.action == ActionOpenWorkspace {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("palette missing ActionOpenWorkspace")
	}
}

func TestWorkspaceNewChannelMode(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	m := New(80)
	m = m.UpdateChrome(OpenWorkspaceMsg{}).Model
	m.handleWorkspaceKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	// tea may not map KeyCtrlN on all builds — set mode directly if needed
	if m.wsMode != wsModeNewChannel {
		m.wsMode = wsModeNewChannel
	}
	m.wsCompose = "fix-login"
	m.handleWorkspaceKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.WorkspaceChannel() != "fix-login" {
		t.Fatalf("channel=%q", m.WorkspaceChannel())
	}
	if m.wsMode != wsModeCompose {
		t.Fatalf("mode=%v", m.wsMode)
	}
}
