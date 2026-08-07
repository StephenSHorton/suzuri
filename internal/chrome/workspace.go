package chrome

import (
	"fmt"
	"os/user"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/StephenSHorton/suzuri/internal/workspace"
)

const (
	wsDialogWant   = 64
	wsMsgRows      = 14
	wsComposeMax   = 2000
	wsHistoryLimit = 80
)

// wsInputMode is what the compose line is for.
type wsInputMode int

const (
	wsModeCompose wsInputMode = iota
	wsModeNewChannel
	wsModeAttach
)

// OpenWorkspaceMsg opens the shared workspace panel.
type OpenWorkspaceMsg struct{}

// ToggleWorkspaceMsg toggles the workspace panel.
type ToggleWorkspaceMsg struct{}

// RefreshWorkspaceMsg reloads channels/messages from disk (MCP posts).
type RefreshWorkspaceMsg struct{}

func (m *Model) openWorkspace() {
	m.closeModalsExcept("workspace")
	m.WorkspaceOpen = true
	m.wsCompose = ""
	m.wsScroll = 0
	m.wsStatus = ""
	m.wsMode = wsModeCompose
	if m.wsChannel == "" {
		m.wsChannel = workspace.DefaultChannel
	}
	if m.wsHumanName == "" {
		m.wsHumanName = localHumanName()
	}
	m.reloadWorkspaceFromDisk()
	_, _ = workspace.Default.Join(m.wsHumanName, workspace.KindHuman, "")
	m.reloadWorkspaceFromDisk()
}

func (m *Model) closeWorkspace() {
	m.WorkspaceOpen = false
	m.wsCompose = ""
	m.wsStatus = ""
	m.wsMode = wsModeCompose
}

func (m *Model) reloadWorkspaceFromDisk() {
	chs, err := workspace.Default.ListChannels()
	if err != nil {
		m.wsStatus = "load error: " + err.Error()
		return
	}
	m.wsChannels = chs
	if m.wsChannel == "" {
		m.wsChannel = workspace.DefaultChannel
	}
	found := false
	for _, ch := range chs {
		if ch.ID == m.wsChannel {
			found = true
			break
		}
	}
	if !found && len(chs) > 0 {
		m.wsChannel = chs[0].ID
	}
	msgs, err := workspace.Default.History(m.wsChannel, wsHistoryLimit)
	if err != nil {
		m.wsStatus = "history error: " + err.Error()
		return
	}
	m.wsMessages = msgs
	members, _ := workspace.Default.Members()
	m.wsMembers = members
}

func (m *Model) humanName() string {
	if m.wsHumanName == "" {
		m.wsHumanName = localHumanName()
	}
	return m.wsHumanName
}

func (m *Model) workspacePostCompose() {
	body := strings.TrimSpace(m.wsCompose)
	if body == "" {
		return
	}
	if utf8.RuneCountInString(body) > wsComposeMax {
		m.wsStatus = "message too long"
		return
	}
	_, err := workspace.Default.Post(m.wsChannel, body, "", m.humanName(), workspace.KindHuman, "")
	if err != nil {
		m.wsStatus = err.Error()
		return
	}
	m.wsCompose = ""
	m.wsStatus = ""
	m.wsScroll = 0
	m.reloadWorkspaceFromDisk()
}

func (m *Model) workspaceCreateChannel() {
	name := strings.TrimSpace(m.wsCompose)
	if name == "" {
		m.wsStatus = "channel name required"
		return
	}
	ch, err := workspace.Default.CreateChannel(name, "")
	if err != nil {
		m.wsStatus = err.Error()
		return
	}
	// Announce in the new channel.
	_, _ = workspace.Default.Post(ch.ID, "channel created", "", m.humanName(), workspace.KindHuman, "")
	m.wsChannel = ch.ID
	m.wsCompose = ""
	m.wsMode = wsModeCompose
	m.wsScroll = 0
	m.wsStatus = "created #" + ch.ID
	m.reloadWorkspaceFromDisk()
}

func (m *Model) workspaceAttachFile() {
	path := strings.TrimSpace(m.wsCompose)
	if path == "" {
		m.wsStatus = "file path required"
		return
	}
	// Strip surrounding quotes if user pasted a quoted path.
	path = strings.Trim(path, `"'`)
	msg, err := workspace.Default.Upload(m.wsChannel, path, "", m.humanName(), workspace.KindHuman, "")
	if err != nil {
		m.wsStatus = err.Error()
		return
	}
	m.wsCompose = ""
	m.wsMode = wsModeCompose
	m.wsScroll = 0
	name := path
	if msg.File != nil {
		name = msg.File.Name
	}
	m.wsStatus = "attached " + name
	m.reloadWorkspaceFromDisk()
}

func (m *Model) workspaceCycleChannel(delta int) {
	if len(m.wsChannels) == 0 {
		return
	}
	idx := 0
	for i, ch := range m.wsChannels {
		if ch.ID == m.wsChannel {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(m.wsChannels)) % len(m.wsChannels)
	m.wsChannel = m.wsChannels[idx].ID
	m.wsScroll = 0
	m.wsMode = wsModeCompose
	m.reloadWorkspaceFromDisk()
}

func (m *Model) handleWorkspaceKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		if m.wsMode != wsModeCompose {
			m.wsMode = wsModeCompose
			m.wsCompose = ""
			m.wsStatus = ""
			return
		}
		m.closeWorkspace()
	case "ctrl+c":
		m.closeWorkspace()
	case "enter":
		switch m.wsMode {
		case wsModeNewChannel:
			m.workspaceCreateChannel()
		case wsModeAttach:
			m.workspaceAttachFile()
		default:
			m.workspacePostCompose()
		}
	case "tab":
		if m.wsMode == wsModeCompose {
			m.workspaceCycleChannel(1)
		}
	case "shift+tab":
		if m.wsMode == wsModeCompose {
			m.workspaceCycleChannel(-1)
		}
	case "ctrl+n":
		m.wsMode = wsModeNewChannel
		m.wsCompose = ""
		m.wsStatus = "new channel name"
	case "ctrl+f":
		m.wsMode = wsModeAttach
		m.wsCompose = ""
		m.wsStatus = "path to attach"
	case "ctrl+r":
		m.reloadWorkspaceFromDisk()
		m.wsStatus = "refreshed"
	case "up":
		if m.wsScroll < max(0, len(m.wsMessages)-1) {
			m.wsScroll++
		}
	case "down":
		if m.wsScroll > 0 {
			m.wsScroll--
		}
	case "pgup":
		m.wsScroll += wsMsgRows
		if m.wsScroll > max(0, len(m.wsMessages)-1) {
			m.wsScroll = max(0, len(m.wsMessages)-1)
		}
	case "pgdown":
		m.wsScroll -= wsMsgRows
		if m.wsScroll < 0 {
			m.wsScroll = 0
		}
	case "backspace":
		if m.wsCompose != "" {
			rs := []rune(m.wsCompose)
			m.wsCompose = string(rs[:len(rs)-1])
		}
	case "ctrl+u":
		m.wsCompose = ""
	default:
		if msg.Type == tea.KeyRunes {
			for _, r := range msg.Runes {
				if r == '\n' || r == '\r' {
					continue
				}
				if utf8.RuneCountInString(m.wsCompose) >= wsComposeMax {
					break
				}
				m.wsCompose += string(r)
			}
		}
	}
}

func (m Model) renderWorkspace(w int) string {
	outer := clampDialogWidth(wsDialogWant, w)
	innerW := dialogInnerWidth(outer)
	if innerW < 24 {
		innerW = 24
	}

	var chParts []string
	for _, ch := range m.wsChannels {
		label := "#" + ch.ID
		if ch.ID == m.wsChannel {
			chParts = append(chParts, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render(label))
		} else {
			chParts = append(chParts, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(label))
		}
	}
	chLine := strings.Join(chParts, "  ")
	if chLine == "" {
		chLine = "#" + workspace.DefaultChannel
	}
	chLine = ansi.Truncate(chLine, innerW, "…")

	memN := len(m.wsMembers)
	header := fmt.Sprintf("%s · %d members", chLine, memN)

	msgs := m.wsMessages
	end := len(msgs) - m.wsScroll
	if end < 0 {
		end = 0
	}
	if end > len(msgs) {
		end = len(msgs)
	}
	start := end - wsMsgRows
	if start < 0 {
		start = 0
	}
	var lines []string
	if start == 0 && end == 0 {
		lines = append(lines, lipgloss.NewStyle().Faint(true).Render("(no messages yet — say hello)"))
	}
	for _, msg := range msgs[start:end] {
		lines = append(lines, formatWorkspaceMsg(msg, innerW))
	}
	for len(lines) < wsMsgRows {
		lines = append([]string{""}, lines...)
	}
	body := strings.Join(lines, "\n")

	compose := m.wsCompose
	caret := "▌"
	var prompt string
	switch m.wsMode {
	case wsModeNewChannel:
		prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("# ")
	case wsModeAttach:
		prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("📎 ")
	default:
		prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("› ")
	}
	compLine := prompt + ansi.Truncate(compose+caret, innerW-4, "…")

	status := m.wsStatus
	if status == "" {
		status = workspace.Dir()
	}
	status = ansi.Truncate(status, innerW, "…")

	parts := []string{
		lipgloss.NewStyle().Faint(true).Render(header),
		"",
		body,
		"",
		compLine,
		lipgloss.NewStyle().Faint(true).Render(status),
	}
	footer := styleDialogHintKey().Render("enter") + styleDialogHint().Render(" send  ") +
		styleDialogHintKey().Render("tab") + styleDialogHint().Render(" ch  ") +
		styleDialogHintKey().Render("⌃n") + styleDialogHint().Render(" new  ") +
		styleDialogHintKey().Render("⌃f") + styleDialogHint().Render(" file  ") +
		styleDialogHintKey().Render("⌃r") + styleDialogHint().Render("  ") +
		styleDialogHintKey().Render("esc")
	return renderDialogCard(outer, "Workspace", parts, footer)
}

func formatWorkspaceMsg(msg workspace.Message, width int) string {
	ts := msg.TS.Local().Format("15:04")
	name := msg.FromName
	if name == "" {
		name = "?"
	}
	kindMark := ""
	if msg.FromKind == workspace.KindAgent {
		kindMark = "·ai"
	}
	if msg.Kind == "system" {
		line := fmt.Sprintf("%s  %s", ts, msg.Body)
		return lipgloss.NewStyle().Faint(true).Italic(true).Render(ansi.Truncate(line, width, "…"))
	}
	bodyText := strings.ReplaceAll(msg.Body, "\n", " ")
	if msg.Kind == "file" && msg.File != nil {
		bodyText = fmt.Sprintf("📎 %s (%s)", msg.File.Name, humanBytes(uint64(msg.File.Bytes)))
		if msg.Body != "" && msg.Body != msg.File.Name {
			bodyText += " — " + msg.Body
		}
	}
	nameStyle := lipgloss.NewStyle().Bold(true)
	if msg.FromKind == workspace.KindAgent {
		nameStyle = nameStyle.Foreground(lipgloss.Color("80"))
	} else {
		nameStyle = nameStyle.Foreground(lipgloss.Color("229"))
	}
	prefix := ts + "  " + name + kindMark + ": "
	prefixW := lipgloss.Width(prefix)
	bodyW := width - prefixW
	if bodyW < 8 {
		bodyW = 8
	}
	return lipgloss.NewStyle().Faint(true).Render(ts+"  ") +
		nameStyle.Render(name+kindMark) +
		": " + ansi.Truncate(bodyText, bodyW, "…")
}

func localHumanName() string {
	if u, err := user.Current(); err == nil {
		if u.Username != "" {
			return u.Username
		}
		if u.Name != "" {
			return u.Name
		}
	}
	return "you"
}

// WorkspaceChannel returns the active channel id (for tests / host).
func (m Model) WorkspaceChannel() string { return m.wsChannel }
