package chrome

import (
	"fmt"
	"os/user"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
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
	// Fixed chrome rows inside the card (title, tabs, presence, gaps, compose, status, footer).
	wsChromeRows = 12
	wsMsgRowsMin = 8
	wsMsgRowsMax = 56
)

// wsInputMode is what the compose line is for.
type wsInputMode int

const (
	wsModeCompose wsInputMode = iota
	wsModeNewChannel
	wsModeAttach
	wsModeDeleteConfirm // second Ctrl+D confirms delete of current channel
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
	m.wsStickBtm = true
	if m.wsChannel == "" {
		m.wsChannel = workspace.DefaultChannel
	}
	if m.wsHumanName == "" {
		m.wsHumanName = localHumanName()
	}
	m.ensureWorkspaceViewport()
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

func (m *Model) ensureWorkspaceViewport() {
	if m.wsVPInit {
		return
	}
	m.wsVP = viewport.New(40, wsMsgRowsMin)
	m.wsVP.MouseWheelEnabled = false
	m.wsVPInit = true
	m.wsStickBtm = true
}

func (m *Model) reloadWorkspaceFromDisk() {
	m.ensureWorkspaceViewport()
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
	// Refresh viewport content; pin to bottom unless user scrolled up.
	m.syncWorkspaceViewport(true)
}

// syncWorkspaceViewport rebuilds message content. If refreshContent is true,
// content is re-set; stick-to-bottom applies when wsStickBtm is set.
func (m *Model) syncWorkspaceViewport(refreshContent bool) {
	m.ensureWorkspaceViewport()
	innerW := dialogInnerWidth(workspaceDialogWidth(m.Width))
	if innerW < 24 {
		innerW = 24
	}
	msgRows := m.workspaceMsgRows()
	m.wsVP.Width = innerW
	m.wsVP.Height = msgRows
	if !refreshContent {
		return
	}
	content := m.workspaceMessageContent(innerW)
	atBottom := m.wsStickBtm || m.wsVP.AtBottom()
	m.wsVP.SetContent(content)
	if atBottom {
		m.wsVP.GotoBottom()
		m.wsStickBtm = true
	}
}

func (m Model) workspaceMessageContent(innerW int) string {
	me := m.humanName()
	if len(m.wsMessages) == 0 {
		return workspaceEmptyState(innerW, m.wsChannel)
	}
	var allLines []string
	for _, msg := range m.wsMessages {
		allLines = append(allLines, formatChatBubble(msg, innerW, me)...)
		allLines = append(allLines, "") // gap between bubbles
	}
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}
	return strings.Join(allLines, "\n")
}

func workspaceEmptyState(width int, channel string) string {
	ch := channel
	if ch == "" {
		ch = workspace.DefaultChannel
	}
	title := lipgloss.NewStyle().Foreground(colText).Bold(true).Render("No messages yet")
	hint1 := lipgloss.NewStyle().Foreground(colSoft).Render(
		fmt.Sprintf("Say hello in #%s — humans type below; agents use workspace_post.", ch))
	hint2 := lipgloss.NewStyle().Foreground(colMute).Render(
		"Agents: workspace_join → workspace_set_status → workspace_history → workspace_post")
	block := title + "\n\n" + hint1 + "\n" + hint2
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Center).Render(block)
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
	m.wsStickBtm = true
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
	m.wsStickBtm = true
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
	m.wsStickBtm = true
	name := path
	if msg.File != nil {
		name = msg.File.Name
	}
	m.wsStatus = "attached " + name
	m.reloadWorkspaceFromDisk()
}

func (m *Model) workspaceDeleteCurrentChannel() {
	ch := m.wsChannel
	if ch == "" || ch == workspace.DefaultChannel {
		m.wsStatus = "cannot delete #general"
		m.wsMode = wsModeCompose
		return
	}
	if m.wsMode != wsModeDeleteConfirm {
		m.wsMode = wsModeDeleteConfirm
		m.wsStatus = fmt.Sprintf("delete #%s? press Ctrl+D again to confirm (esc cancel)", ch)
		return
	}
	slug, err := workspace.Default.DeleteChannel(ch)
	if err != nil {
		m.wsStatus = err.Error()
		m.wsMode = wsModeCompose
		return
	}
	m.wsChannel = workspace.DefaultChannel
	m.wsMode = wsModeCompose
	m.wsScroll = 0
	m.wsStickBtm = true
	m.wsStatus = "deleted #" + slug + " (history + files removed)"
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
	m.wsStickBtm = true
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
	case "ctrl+d":
		m.workspaceDeleteCurrentChannel()
	case "ctrl+r":
		m.wsStickBtm = true
		m.reloadWorkspaceFromDisk()
		m.wsStatus = "refreshed"
		m.wsMode = wsModeCompose
	case "up":
		m.syncWorkspaceViewport(true)
		m.wsVP.ScrollUp(1)
		m.wsStickBtm = m.wsVP.AtBottom()
		m.wsScroll++
	case "down":
		m.syncWorkspaceViewport(true)
		m.wsVP.ScrollDown(1)
		m.wsStickBtm = m.wsVP.AtBottom()
		if m.wsScroll > 0 {
			m.wsScroll--
		}
	case "pgup":
		m.syncWorkspaceViewport(true)
		m.wsVP.PageUp()
		m.wsStickBtm = m.wsVP.AtBottom()
		m.wsScroll += msgRows
	case "pgdown":
		m.syncWorkspaceViewport(true)
		m.wsVP.PageDown()
		m.wsStickBtm = m.wsVP.AtBottom()
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

	// Mutating viewport size/content on a value receiver is intentional for
	// paint: host re-renders from the same model each frame; we sync a copy.
	// Prefer sticking to bottom when content is shorter than the viewport.
	vp := m.wsVP
	if !m.wsVPInit {
		vp = viewport.New(innerW, msgRows)
		vp.MouseWheelEnabled = false
	}
	vp.Width = innerW
	vp.Height = msgRows
	content := m.workspaceMessageContent(innerW)
	atBottom := m.wsStickBtm || vp.AtBottom() || vp.TotalLineCount() <= msgRows
	vp.SetContent(content)
	if atBottom {
		vp.GotoBottom()
	}
	body := vp.View()

	tabs := renderChannelTabs(m.wsChannels, m.wsChannel, innerW)
	presence := renderPresenceStrip(m.wsMembers, innerW)

	compose := m.wsCompose
	caret := "▌"
	var prompt string
	switch m.wsMode {
	case wsModeNewChannel:
		prompt = lipgloss.NewStyle().Foreground(colSecondary).Bold(true).Render("# ")
	case wsModeAttach:
		prompt = lipgloss.NewStyle().Foreground(colCyan).Bold(true).Render("📎 ")
	default:
		prompt = lipgloss.NewStyle().Foreground(colPrimary).Bold(true).Render("› ")
	}
	// Wrap compose on display if long (single-line input still, show tail).
	compVisible := compose + caret
	if lipgloss.Width(compVisible) > innerW-4 {
		compVisible = ansi.Truncate(compVisible, innerW-4, "…")
	}
	compLine := prompt + lipgloss.NewStyle().Foreground(colText).Render(compVisible)

	// Status: ephemeral action feedback only — path demoted to footer when idle.
	statusLine := ""
	if m.wsStatus != "" {
		statusLine = lipgloss.NewStyle().Foreground(colSoft).Render(ansi.Truncate(m.wsStatus, innerW, "…"))
	}

	parts := []string{tabs, presence, "", body, "", compLine}
	if statusLine != "" {
		parts = append(parts, statusLine)
	}

	footer := styleDialogHintKey().Render("enter") + styleDialogHint().Render(" send  ") +
		styleDialogHintKey().Render("tab") + styleDialogHint().Render(" ch  ") +
		styleDialogHintKey().Render("⌃n") + styleDialogHint().Render(" new  ") +
		styleDialogHintKey().Render("⌃d") + styleDialogHint().Render(" del  ") +
		styleDialogHintKey().Render("esc")
	return renderWorkspaceCard(outer, "Workspace", parts, footer)
}

// renderChannelTabs paints a Lip Gloss tab strip (active filled, inactive soft).
// Overflow collapses inactive tabs into "…N more".
func renderChannelTabs(channels []workspace.Channel, active string, width int) string {
	if width < 12 {
		width = 12
	}
	if len(channels) == 0 {
		channels = []workspace.Channel{{ID: workspace.DefaultChannel, Name: workspace.DefaultChannel}}
		active = workspace.DefaultChannel
	}

	activeStyle := lipgloss.NewStyle().
		Background(colPrimary).
		Foreground(colOnPrimary).
		Bold(true).
		Padding(0, 1)
	idleStyle := lipgloss.NewStyle().
		Foreground(colSoft).
		Padding(0, 1)
	moreStyle := lipgloss.NewStyle().Foreground(colMute).Padding(0, 1)

	// Prefer showing the active channel; pack as many others as fit.
	type tabItem struct {
		id     string
		label  string
		active bool
	}
	var items []tabItem
	activeIdx := 0
	for i, ch := range channels {
		id := ch.ID
		if id == "" {
			id = ch.Name
		}
		items = append(items, tabItem{id: id, label: "#" + id, active: id == active})
		if id == active {
			activeIdx = i
		}
	}

	// Build from active outward until width is exhausted.
	renderOne := func(it tabItem) string {
		if it.active {
			return activeStyle.Render(it.label)
		}
		return idleStyle.Render(it.label)
	}
	shown := make([]bool, len(items))
	shown[activeIdx] = true
	parts := []string{renderOne(items[activeIdx])}
	used := lipgloss.Width(parts[0])

	left, right := activeIdx-1, activeIdx+1
	for left >= 0 || right < len(items) {
		added := false
		if right < len(items) {
			p := renderOne(items[right])
			if used+1+lipgloss.Width(p) <= width {
				parts = append(parts, p)
				used += 1 + lipgloss.Width(p)
				shown[right] = true
				right++
				added = true
			}
		}
		if left >= 0 {
			p := renderOne(items[left])
			if used+1+lipgloss.Width(p) <= width {
				parts = append([]string{p}, parts...)
				used += 1 + lipgloss.Width(p)
				shown[left] = true
				left--
				added = true
			}
		}
		if !added {
			break
		}
	}

	hidden := 0
	for _, s := range shown {
		if !s {
			hidden++
		}
	}
	if hidden > 0 {
		more := moreStyle.Render(fmt.Sprintf("…%d more", hidden))
		if used+1+lipgloss.Width(more) <= width {
			parts = append(parts, more)
		}
	}
	return strings.Join(parts, " ")
}

// renderPresenceStrip shows members with availability glyphs (right-aligned-ish).
func renderPresenceStrip(members []workspace.Member, width int) string {
	if width < 12 {
		width = 12
	}
	if len(members) == 0 {
		return lipgloss.NewStyle().Foreground(colMute).Render("no members yet")
	}

	// Sort humans first, then agents; keep stable order otherwise.
	var humans, agents []workspace.Member
	for _, mem := range members {
		if mem.Kind == workspace.KindHuman {
			humans = append(humans, mem)
		} else {
			agents = append(agents, mem)
		}
	}
	ordered := append(humans, agents...)

	var chips []string
	for _, mem := range ordered {
		chips = append(chips, formatMemberChip(mem))
	}

	// Fit as many as possible; show overflow count.
	var shown []string
	used := 0
	hidden := 0
	for i, chip := range chips {
		w := lipgloss.Width(chip)
		sep := 0
		if len(shown) > 0 {
			sep = 2
		}
		if used+sep+w > width-8 && len(shown) > 0 {
			hidden = len(chips) - i
			break
		}
		if len(shown) > 0 {
			used += 2
		}
		shown = append(shown, chip)
		used += w
	}
	line := strings.Join(shown, "  ")
	if hidden > 0 {
		extra := lipgloss.NewStyle().Foreground(colMute).Render(fmt.Sprintf("+%d", hidden))
		if lipgloss.Width(line)+2+lipgloss.Width(extra) <= width {
			line += "  " + extra
		}
	}
	if lipgloss.Width(line) > width {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

func formatMemberChip(m workspace.Member) string {
	st := m.Status
	if st == "" {
		st = workspace.AvailIdle
	}
	glyph, color := availabilityStyle(st)
	name := m.Name
	if name == "" {
		name = "?"
	}
	// Cap name length so many members still fit.
	if utf8.RuneCountInString(name) > 14 {
		name = string([]rune(name)[:13]) + "…"
	}
	label := name
	if m.Kind == workspace.KindAgent {
		label = name
	}
	chip := glyph + " " + label
	if m.StatusNote != "" && (st == workspace.AvailWaiting || st == workspace.AvailBlocked || st == workspace.AvailWorking) {
		note := m.StatusNote
		if utf8.RuneCountInString(note) > 18 {
			note = string([]rune(note)[:17]) + "…"
		}
		chip += " · " + note
	}
	return lipgloss.NewStyle().Foreground(color).Render(chip)
}

func availabilityStyle(st workspace.Availability) (glyph string, color lipgloss.Color) {
	switch workspace.NormalizeAvailability(string(st)) {
	case workspace.AvailWorking:
		return "●", colPrimary
	case workspace.AvailWaiting:
		return "◐", colSecondary
	case workspace.AvailBlocked:
		return "✖", lipgloss.Color("203") // soft red; not a theme role but alert-y
	case workspace.AvailAway:
		return "○", colMute
	default: // idle
		return "●", colCyan
	}
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

// formatChatBubble renders a message as a chat-style bubble.
// Human messages (matching me) sit on the right; agents/others on the left.
func formatChatBubble(msg workspace.Message, width int, me string) []string {
	if width < 20 {
		width = 20
	}
	ts := msg.TS.Local().Format("15:04")
	name := msg.FromName
	if name == "" {
		name = "?"
	}

	if msg.Kind == "system" {
		body := strings.ReplaceAll(msg.Body, "\n", " ")
		line := fmt.Sprintf("— %s · %s —", body, ts)
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		return []string{lipgloss.NewStyle().
			Foreground(colMute).
			Italic(true).
			Width(width).
			Align(lipgloss.Center).
			Render(line)}
	}

	bodyText := strings.ReplaceAll(msg.Body, "\n", " ")
	if msg.Kind == "file" && msg.File != nil {
		bodyText = fmt.Sprintf("📎 %s (%s)", msg.File.Name, humanBytes(uint64(msg.File.Bytes)))
		if msg.Body != "" && msg.Body != msg.File.Name {
			bodyText += " — " + msg.Body
		}
	}

	mine := msg.FromKind == workspace.KindHuman &&
		strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(me))

	// Bubble max ~72% of row so it reads as a bubble, not full width.
	bubbleW := width * 72 / 100
	if bubbleW < 18 {
		bubbleW = min(width-2, 28)
	}
	if bubbleW > width-2 {
		bubbleW = width - 2
	}
	// Inner text width after padding (1 each side) and border (2).
	textW := bubbleW - 4
	if textW < 10 {
		textW = 10
	}

	label := name
	if msg.FromKind == workspace.KindAgent {
		label = name + " · ai"
	}
	header := lipgloss.NewStyle().Foreground(colSoft).Render(fmt.Sprintf("%s  %s", label, ts))

	var bodyLines []string
	if wl := wrapWords(bodyText, textW); len(wl) > 0 {
		bodyLines = wl
	} else {
		bodyLines = []string{""}
	}
	// Body uses theme text; join with unstyled newlines.
	bodyJoined := strings.Join(bodyLines, "\n")
	inner := header + "\n" + bodyJoined

	var borderFg lipgloss.Color
	var fg lipgloss.Color
	if mine {
		borderFg = colPrimary
		fg = colText
	} else if msg.FromKind == workspace.KindAgent {
		borderFg = colCyan
		fg = colText
	} else {
		borderFg = colDim
		fg = colText
	}

	bubble := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderFg).
		Foreground(fg).
		Padding(0, 1).
		Width(bubbleW).
		Render(inner)

	align := lipgloss.Left
	if mine {
		align = lipgloss.Right
	}
	placed := lipgloss.PlaceHorizontal(width, align, bubble)
	return strings.Split(placed, "\n")
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
