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
	wsComposeMax   = 2000
	wsHistoryLimit = 120
	// Fraction of host terminal used by the workspace modal (width and height).
	wsModalFrac = 0.80
	// Fixed chrome rows inside the card (title, header, gaps, compose, status, footer).
	wsChromeRows = 10
	wsMsgRowsMin = 8
	wsMsgRowsMax = 56
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

// workspaceDialogWidth is ~80% of the host width, with margin from the edges.
func workspaceDialogWidth(windowCols int) int {
	if windowCols < 24 {
		windowCols = 24
	}
	// Leave ~10% margin total (~5% each side) → 80% content.
	w := int(float64(windowCols) * wsModalFrac)
	// Hard margins so it still reads as a floating modal.
	maxW := windowCols - 4
	if maxW < 28 {
		maxW = 28
	}
	if w > maxW {
		w = maxW
	}
	if w < 36 && windowCols >= 40 {
		w = 36
	}
	if w < 28 {
		w = 28
	}
	return w
}

// workspaceMsgRows is how many message *lines* fit at ~80% of host height.
func (m Model) workspaceMsgRows() int {
	h := m.Height
	if h < 12 {
		h = 24
	}
	// Use 80% of host rows for the whole card, then subtract chrome.
	card := int(float64(h) * wsModalFrac)
	rows := card - wsChromeRows
	if rows < wsMsgRowsMin {
		rows = wsMsgRowsMin
	}
	if rows > wsMsgRowsMax {
		rows = wsMsgRowsMax
	}
	return rows
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
	msgRows := m.workspaceMsgRows()
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
		m.wsScroll += msgRows
		if m.wsScroll > max(0, len(m.wsMessages)-1) {
			m.wsScroll = max(0, len(m.wsMessages)-1)
		}
	case "pgdown":
		m.wsScroll -= msgRows
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
	outer := workspaceDialogWidth(w)
	// Bypass clampDialogWidth's 68-col global max for this modal only.
	innerW := dialogInnerWidth(outer)
	if innerW < 24 {
		innerW = 24
	}
	msgRows := m.workspaceMsgRows()

	// Channel strip: plain text for width safety, then style active separately.
	var chPlain []string
	activeIdx := -1
	for i, ch := range m.wsChannels {
		chPlain = append(chPlain, "#"+ch.ID)
		if ch.ID == m.wsChannel {
			activeIdx = i
		}
	}
	if len(chPlain) == 0 {
		chPlain = []string{"#" + workspace.DefaultChannel}
		activeIdx = 0
	}
	// Build styled channel line without truncating mid-ANSI incorrectly.
	var chParts []string
	for i, label := range chPlain {
		if i == activeIdx {
			chParts = append(chParts, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render(label))
		} else {
			chParts = append(chParts, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(label))
		}
	}
	chLine := strings.Join(chParts, "  ")
	// Truncate plain version for measure, then re-style if needed.
	plainJoin := strings.Join(chPlain, "  ")
	memN := len(m.wsMembers)
	memPlain := fmt.Sprintf(" · %d members", memN)
	if lipgloss.Width(plainJoin)+lipgloss.Width(memPlain) > innerW {
		// Prefer keeping active channel + member count.
		budget := innerW - lipgloss.Width(memPlain) - 1
		if budget < 8 {
			budget = 8
		}
		chLine = ansi.Truncate(chLine, budget, "…")
	}
	header := chLine + lipgloss.NewStyle().Faint(true).Render(memPlain)

	// Messages: build wrapped lines, then take a window of msgRows lines.
	var allLines []string
	if len(m.wsMessages) == 0 {
		allLines = append(allLines, lipgloss.NewStyle().Faint(true).Render("(no messages yet — say hello)"))
	} else {
		for _, msg := range m.wsMessages {
			allLines = append(allLines, formatWorkspaceMsgWrapped(msg, innerW)...)
		}
	}
	// Scroll is in *message* units historically; map to line scroll from the end.
	// Keep last msgRows lines, offset by approximate lines-per-message * wsScroll.
	end := len(allLines)
	// Use wsScroll as line-scroll from bottom when wrapping (smoother UX).
	lineScroll := m.wsScroll
	// If user scrolled by messages, multiply — treat wsScroll as line offset from bottom.
	if lineScroll > end {
		lineScroll = end
	}
	end -= lineScroll
	if end < 0 {
		end = 0
	}
	if end > len(allLines) {
		end = len(allLines)
	}
	start := end - msgRows
	if start < 0 {
		start = 0
	}
	var lines []string
	if start == 0 && end == 0 {
		lines = append(lines, lipgloss.NewStyle().Faint(true).Render("(no messages yet — say hello)"))
	} else {
		lines = append(lines, allLines[start:end]...)
	}
	for len(lines) < msgRows {
		lines = append([]string{""}, lines...)
	}
	// Cap if wrap overshot (shouldn't).
	if len(lines) > msgRows {
		lines = lines[len(lines)-msgRows:]
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
	// Wrap compose on display if long (single-line input still, show tail).
	compVisible := compose + caret
	if lipgloss.Width(compVisible) > innerW-4 {
		compVisible = ansi.Truncate(compVisible, innerW-4, "…")
	}
	compLine := prompt + compVisible

	status := m.wsStatus
	if status == "" {
		status = workspace.Dir()
	}
	status = ansi.Truncate(status, innerW, "…")

	parts := []string{
		header,
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
	// renderDialogCardEx clamps outer via clampDialogWidth(outer, outer+8) which
	// re-applies the 68-col max — pass a synthetic high window width by using
	// a dedicated render path that respects our outer width.
	return renderWorkspaceCard(outer, "Workspace", parts, footer)
}

// renderWorkspaceCard is like renderDialogCard but does not re-clamp to 68 cols.
func renderWorkspaceCard(outerWidth int, title string, body []string, footer string) string {
	if outerWidth < 28 {
		outerWidth = 28
	}
	inner := dialogInnerWidth(outerWidth)
	var lines []string
	if title != "" {
		lines = append(lines, styleDialogTitle().
			Background(colPanel).
			Width(inner).
			MaxHeight(1).
			Render(title))
	}
	if title != "" && len(body) > 0 {
		lines = append(lines, dialogRuleLine(inner))
	}
	for _, b := range body {
		if b == "" {
			lines = append(lines, panelFillLine(inner, ""))
			continue
		}
		if strings.Contains(b, "\n") {
			for _, line := range strings.Split(b, "\n") {
				lines = append(lines, panelFillLine(inner, line))
			}
			continue
		}
		lines = append(lines, panelFillLine(inner, b))
	}
	if footer != "" {
		lines = append(lines, dialogRuleLine(inner))
		lines = append(lines, panelFillLine(inner, footer))
	}
	content := joinLines(lines)
	return styleDialogView().Width(outerWidth).Render(content)
}

// formatWorkspaceMsgWrapped returns one or more display lines for a message
// (word-wrap, no mid-word hard cut when possible).
func formatWorkspaceMsgWrapped(msg workspace.Message, width int) []string {
	if width < 16 {
		width = 16
	}
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
		// Indent wrap under timestamp.
		prefix := ts + "  "
		body := strings.ReplaceAll(msg.Body, "\n", " ")
		wrapped := wrapWords(body, width-lipgloss.Width(prefix))
		if len(wrapped) == 0 {
			return []string{lipgloss.NewStyle().Faint(true).Italic(true).Render(prefix)}
		}
		var out []string
		for i, w := range wrapped {
			if i == 0 {
				out = append(out, lipgloss.NewStyle().Faint(true).Italic(true).Render(prefix+w))
			} else {
				pad := strings.Repeat(" ", lipgloss.Width(prefix))
				out = append(out, lipgloss.NewStyle().Faint(true).Italic(true).Render(pad+w))
			}
		}
		return out
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
	prefixPlain := ts + "  " + name + kindMark + ": "
	prefixW := lipgloss.Width(prefixPlain)
	bodyW := width - prefixW
	if bodyW < 12 {
		// Narrow: stack body under name.
		first := lipgloss.NewStyle().Faint(true).Render(ts+"  ") +
			nameStyle.Render(name+kindMark) + ":"
		wrapped := wrapWords(bodyText, width-2)
		out := []string{first}
		for _, w := range wrapped {
			out = append(out, "  "+w)
		}
		return out
	}
	wrapped := wrapWords(bodyText, bodyW)
	var out []string
	for i, w := range wrapped {
		if i == 0 {
			out = append(out, lipgloss.NewStyle().Faint(true).Render(ts+"  ")+
				nameStyle.Render(name+kindMark)+
				": "+w)
		} else {
			pad := strings.Repeat(" ", prefixW)
			out = append(out, pad+w)
		}
	}
	if len(out) == 0 {
		out = []string{lipgloss.NewStyle().Faint(true).Render(ts+"  ") + nameStyle.Render(name+kindMark) + ":"}
	}
	return out
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

// WorkspaceOverlayRows estimates paint rows for the large workspace modal.
func (m Model) WorkspaceOverlayRows() int {
	return m.workspaceMsgRows() + wsChromeRows + 4
}
