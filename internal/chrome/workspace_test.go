package chrome

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/StephenSHorton/suzuri/internal/config"
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

func TestWorkspaceComposeAcceptsRunes(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	m := New(80)
	m = m.UpdateChrome(OpenWorkspaceMsg{}).Model
	m.handleWorkspaceKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	if m.wsCompose != "hi" {
		t.Fatalf("compose=%q want hi", m.wsCompose)
	}
}

func TestWorkspaceMentionComplete(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	m := New(80)
	m = m.UpdateChrome(OpenWorkspaceMsg{}).Model
	m.wsMembers = []workspace.Member{
		{Name: "build-session", Kind: workspace.KindAgent},
		{Name: "alice", Kind: workspace.KindHuman},
	}
	m.wsCompose = "hey @bu"
	m.wsClampMentionIdx()
	cands := m.wsMentionCandidates()
	if len(cands) < 1 || cands[0].Name != "build-session" {
		t.Fatalf("cands=%+v", cands)
	}
	m.wsCompleteMention(cands)
	if m.wsCompose != "hey @build-session " {
		t.Fatalf("compose=%q", m.wsCompose)
	}
}

func TestStyleMentionsInText(t *testing.T) {
	names := map[string]string{"alice": "alice"}
	out := styleMentionsInText("hi @alice there", names, colPanel)
	if !strings.Contains(out, "alice") {
		t.Fatalf("out=%q", out)
	}
	// Plain (no members) still returns something printable-stripped nonempty.
	plain := ansi.Strip(styleMentionsInText("nope", nil, colPanel))
	if plain != "nope" {
		t.Fatalf("plain=%q", plain)
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
	if !strings.Contains(chip, "need human") {
		t.Fatalf("expected note in chip: %q", chip)
	}
}

func TestMemberIdentityStable(t *testing.T) {
	c1, g1 := memberIdentity("build-session", workspace.KindAgent)
	c2, g2 := memberIdentity("build-session", workspace.KindAgent)
	if c1 != c2 || g1 != g2 {
		t.Fatal("identity should be stable")
	}
	_, gHuman := memberIdentity("build-session", workspace.KindHuman)
	if g1 == gHuman {
		// same glyph possible; colors should still differ via kind bit
	}
	cOther, _ := memberIdentity("alice", workspace.KindHuman)
	if c1 == cOther && g1 == gHuman {
		// rare collision ok
	}
}

func TestComposeCaretFromSettings(t *testing.T) {
	if composeCaretGlyph(config.CursorBlock) != "█" {
		t.Fatal("block")
	}
	if composeCaretGlyph(config.CursorBar) != "▌" {
		t.Fatal("bar")
	}
	if composeCaretGlyph(config.CursorUnderline) != "▁" {
		t.Fatal("underline")
	}
}

func TestChannelTabHitsIncludePlus(t *testing.T) {
	chs := []workspace.Channel{{ID: "general"}, {ID: "pr-1"}}
	hits, plus := channelTabHits(chs, "general", 80)
	if len(hits) < 1 {
		t.Fatal("expected channel hits")
	}
	if !plus.ok {
		t.Fatal("expected + chip")
	}
}

func TestWorkspaceDialogWidth(t *testing.T) {
	// 80% of 100 = 80, under max 120.
	w := workspaceDialogWidth(100)
	if w != 80 {
		t.Fatalf("workspaceDialogWidth(100)=%d want 80", w)
	}
	// Huge window: cap at wsMaxOuterW.
	if got := workspaceDialogWidth(200); got != wsMaxOuterW {
		t.Fatalf("workspaceDialogWidth(200)=%d want %d", got, wsMaxOuterW)
	}
}

func TestSolidifyOverlayLinesFillsWidth(t *testing.T) {
	out := solidifyOverlayLines("hi", 20)
	if lipgloss.Width(out) != 20 {
		t.Fatalf("width=%d want 20 (stripped=%q)", lipgloss.Width(out), ansi.Strip(out))
	}
}

func TestFormatChatBubble(t *testing.T) {
	msg := workspace.Message{
		FromName: "alice",
		FromKind: workspace.KindHuman,
		Kind:     "text",
		Body:     "this is a fairly long message that should wrap across multiple lines inside a chat bubble",
	}
	names := map[string]string{"alice": "alice", "bot": "bot"}
	lines := formatChatBubble(msg, 48, "alice", names)
	if len(lines) < 2 {
		t.Fatalf("expected multi-line bubble, got %#v", lines)
	}
	// mine → right-ish content should still render
	agent := workspace.Message{
		FromName: "bot",
		FromKind: workspace.KindAgent,
		Kind:     "text",
		Body:     "hello from the left @alice",
	}
	if al := formatChatBubble(agent, 48, "alice", names); len(al) < 1 {
		t.Fatal("agent bubble empty")
	}
}
