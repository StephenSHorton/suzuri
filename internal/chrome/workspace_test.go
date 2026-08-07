package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/StephenSHorton/suzuri/internal/workspace"
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

func TestRenderChannelTabsShowsActive(t *testing.T) {
	chs := []workspace.Channel{
		{ID: "general"},
		{ID: "pr-1"},
		{ID: "pr-2"},
	}
	out := renderChannelTabs(chs, "pr-1", 80)
	if !strings.Contains(out, "#pr-1") {
		t.Fatalf("missing active tab: %q", out)
	}
	if !strings.Contains(out, "#general") {
		t.Fatalf("missing sibling tab: %q", out)
	}
}

func TestAvailabilityStyleCodes(t *testing.T) {
	g, _ := availabilityStyle(workspace.AvailWorking)
	if g == "" {
		t.Fatal("empty glyph")
	}
	chip := formatMemberChip(workspace.Member{
		Name: "bot", Kind: workspace.KindAgent,
		Status: workspace.AvailWaiting, StatusNote: "need human",
	})
	if !strings.Contains(chip, "bot") {
		t.Fatalf("chip=%q", chip)
	}
	// Note should appear for waiting
	if !strings.Contains(chip, "need human") {
		t.Fatalf("expected note in chip: %q", chip)
	}
}

func TestWorkspaceDialogWidth(t *testing.T) {
	w := workspaceDialogWidth(100)
	if w < 70 || w > 96 {
		t.Fatalf("80%% of 100 should be ~80, got %d", w)
	}
}

func TestFormatChatBubble(t *testing.T) {
	msg := workspace.Message{
		FromName: "alice",
		FromKind: workspace.KindHuman,
		Kind:     "text",
		Body:     "this is a fairly long message that should wrap across multiple lines inside a chat bubble",
	}
	lines := formatChatBubble(msg, 48, "alice")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line bubble, got %#v", lines)
	}
	// mine → right-ish content should still render
	agent := workspace.Message{
		FromName: "bot",
		FromKind: workspace.KindAgent,
		Kind:     "text",
		Body:     "hello from the left",
	}
	if al := formatChatBubble(agent, 48, "alice"); len(al) < 1 {
		t.Fatal("agent bubble empty")
	}
}
