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

	"github.com/StephenSHorton/suzuri/internal/textedit"
	"github.com/StephenSHorton/suzuri/internal/workspace"
)

const (
	wsComposeMax   = 2000
	wsHistoryLimit = 120
	// Horizontal margin (cols each side). Modal content fills the rest.
	wsMarginCols = 1
	// Vertical: leave tab strip + small gap; modal uses nearly all remaining rows.
	wsMarginRows = 1
	// Fixed chrome rows inside the card (title, tabs, presence, gaps, compose, status, footer).
	wsChromeRows = 12
	wsMsgRowsMin = 8
	wsMsgRowsMax = 72
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
	m.wsMentionIdx = 0
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
	m.wsMentionIdx = 0
}

// WorkspacePaste inserts clipboard text into the compose line (single-line).
func (m *Model) WorkspacePaste(s string) {
	if !m.WorkspaceOpen || s == "" {
		return
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if s == "" {
		return
	}
	m.wsPushUndo()
	for _, r := range s {
		if r < 32 {
			continue
		}
		if utf8.RuneCountInString(m.wsCompose) >= wsComposeMax {
			break
		}
		m.wsCompose += string(r)
	}
	m.wsClampMentionIdx()
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
	names := m.wsMemberNameSet()
	if len(m.wsMessages) == 0 {
		return workspaceEmptyState(innerW, m.wsChannel)
	}
	var allLines []string
	for _, msg := range m.wsMessages {
		allLines = append(allLines, formatChatBubble(msg, innerW, me, names)...)
		allLines = append(allLines, "") // gap between bubbles
	}
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}
	return strings.Join(allLines, "\n")
}

// wsMemberNameSet lowercased member names for @mention highlighting.
func (m Model) wsMemberNameSet() map[string]string {
	// map lower → display name
	out := make(map[string]string, len(m.wsMembers))
	for _, mem := range m.wsMembers {
		n := strings.TrimSpace(mem.Name)
		if n == "" {
			continue
		}
		out[strings.ToLower(n)] = n
	}
	return out
}

func workspaceEmptyState(width int, channel string) string {
	ch := channel
	if ch == "" {
		ch = workspace.DefaultChannel
	}
	// Every span sets Background(colPanel) — host skips default-bg cells.
	title := lipgloss.NewStyle().Foreground(colText).Background(colPanel).Bold(true).Render("No messages yet")
	hint1 := lipgloss.NewStyle().Foreground(colSoft).Background(colPanel).Render(
		fmt.Sprintf("Say hello in #%s — humans type below; agents use workspace_post.", ch))
	hint2 := lipgloss.NewStyle().Foreground(colMute).Background(colPanel).Render(
		"Agents: workspace_join → workspace_set_status → workspace_history → workspace_post")
	blank := lipgloss.NewStyle().Background(colPanel).Width(width).MaxHeight(1).Render("")
	block := title + "\n" + blank + "\n" + hint1 + "\n" + hint2
	// placeOpaque so centered empty-state doesn't leave transparent side gutters.
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		lines = append(lines, placeOpaque(width, lipgloss.Center, line, colPanel))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) humanName() string {
	if m.wsHumanName == "" {
		m.wsHumanName = localHumanName()
	}
	return m.wsHumanName
}

// workspaceDialogWidth fills the host width minus a thin margin.
// Earlier 80%-centered cards left a huge empty gutter the dim matte could not fix.
func workspaceDialogWidth(windowCols int) int {
	if windowCols < 24 {
		windowCols = 24
	}
	w := windowCols - 2*wsMarginCols
	if w < 28 {
		w = 28
	}
	if w > windowCols {
		w = windowCols
	}
	return w
}

// workspaceMsgRows is how many message lines fit after card chrome, using nearly
// the full host height (not a short 80% card floating in empty space).
func (m Model) workspaceMsgRows() int {
	h := m.Height
	if h < 12 {
		h = 24
	}
	// Host height minus margin; subtract card chrome (title/tabs/compose/footer).
	card := h - 2*wsMarginRows
	if card < 12 {
		card = 12
	}
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

func (m *Model) wsComposeSnapshot() textedit.Snapshot {
	rs := []rune(m.wsCompose)
	return textedit.Snapshot{Text: rs, Cursor: len(rs), Sel: -1}
}

func (m *Model) wsPushUndo() {
	if m.wsHist == nil {
		m.wsHist = textedit.NewHistory(100)
	}
	m.wsHist.Push(m.wsComposeSnapshot())
}

func (m *Model) wsApplyCompose(s textedit.Snapshot) {
	m.wsCompose = string(s.Text)
}

func (m *Model) handleWorkspaceKey(msg tea.KeyMsg) {
	msgRows := m.workspaceMsgRows()
	// Live @mention picker takes Tab / arrows when active.
	mentionCands := m.wsMentionCandidates()
	mentionActive := m.wsMode == wsModeCompose && len(mentionCands) > 0

	switch msg.String() {
	case "esc":
		if mentionActive {
			// Dismiss mention by completing nothing — drop trailing partial @query? keep text.
			m.wsMentionIdx = 0
			m.wsStatus = ""
			return
		}
		if m.wsMode != wsModeCompose {
			m.wsMode = wsModeCompose
			m.wsCompose = ""
			m.wsStatus = ""
			if m.wsHist != nil {
				m.wsHist.Clear()
			}
			return
		}
		m.closeWorkspace()
	case "ctrl+c":
		m.closeWorkspace()
	case "ctrl+z":
		if m.wsHist != nil {
			if prev, ok := m.wsHist.Undo(m.wsComposeSnapshot()); ok {
				m.wsApplyCompose(prev)
				m.wsClampMentionIdx()
			}
		}
	case "ctrl+y", "ctrl+shift+z":
		if m.wsHist != nil {
			if next, ok := m.wsHist.Redo(m.wsComposeSnapshot()); ok {
				m.wsApplyCompose(next)
				m.wsClampMentionIdx()
			}
		}
	case "enter":
		if mentionActive {
			m.wsCompleteMention(mentionCands)
			return
		}
		switch m.wsMode {
		case wsModeNewChannel:
			m.workspaceCreateChannel()
		case wsModeAttach:
			m.workspaceAttachFile()
		default:
			m.workspacePostCompose()
		}
	case "tab":
		if mentionActive {
			m.wsCompleteMention(mentionCands)
			return
		}
		if m.wsMode == wsModeCompose {
			m.workspaceCycleChannel(1)
		}
	case "shift+tab":
		if mentionActive {
			// Cycle mention selection backwards.
			if m.wsMentionIdx <= 0 {
				m.wsMentionIdx = len(mentionCands) - 1
			} else {
				m.wsMentionIdx--
			}
			return
		}
		if m.wsMode == wsModeCompose {
			m.workspaceCycleChannel(-1)
		}
	case "ctrl+n":
		m.wsMode = wsModeNewChannel
		m.wsCompose = ""
		m.wsStatus = "new channel name"
		if m.wsHist != nil {
			m.wsHist.Clear()
		}
	case "ctrl+f":
		m.wsMode = wsModeAttach
		m.wsCompose = ""
		m.wsStatus = "path to attach"
		if m.wsHist != nil {
			m.wsHist.Clear()
		}
	case "ctrl+d":
		m.workspaceDeleteCurrentChannel()
	case "ctrl+r":
		m.wsStickBtm = true
		m.reloadWorkspaceFromDisk()
		m.wsStatus = "refreshed"
		m.wsMode = wsModeCompose
	case "up":
		if mentionActive {
			if m.wsMentionIdx <= 0 {
				m.wsMentionIdx = len(mentionCands) - 1
			} else {
				m.wsMentionIdx--
			}
			return
		}
		m.syncWorkspaceViewport(true)
		m.wsVP.ScrollUp(1)
		m.wsStickBtm = m.wsVP.AtBottom()
		m.wsScroll++
	case "down":
		if mentionActive {
			m.wsMentionIdx = (m.wsMentionIdx + 1) % len(mentionCands)
			return
		}
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
			m.wsPushUndo()
			rs := []rune(m.wsCompose)
			m.wsCompose = string(rs[:len(rs)-1])
			m.wsClampMentionIdx()
		}
	case "ctrl+u":
		if m.wsCompose != "" {
			m.wsPushUndo()
			m.wsCompose = ""
			m.wsMentionIdx = 0
		}
	default:
		if msg.Type == tea.KeyRunes {
			var added bool
			for _, r := range msg.Runes {
				if r == '\n' || r == '\r' {
					continue
				}
				if utf8.RuneCountInString(m.wsCompose) >= wsComposeMax {
					break
				}
				if !added {
					m.wsPushUndo()
					added = true
				}
				m.wsCompose += string(r)
			}
			if added {
				m.wsClampMentionIdx()
			}
		}
	}
}

// wsMentionQuery returns the active @query at the end of compose (partial name).
// ok is false when not in a mention context.
func (m Model) wsMentionQuery() (query string, atStart int, ok bool) {
	if m.wsMode != wsModeCompose {
		return "", 0, false
	}
	rs := []rune(m.wsCompose)
	// Find last '@' that starts a mention (start of string or after whitespace).
	at := -1
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == '@' {
			if i == 0 || isMentionBoundary(rs[i-1]) {
				at = i
			}
			break
		}
		// Only allow name-ish chars after @.
		if !isMentionNameRune(rs[i]) {
			return "", 0, false
		}
	}
	if at < 0 {
		return "", 0, false
	}
	q := string(rs[at+1:])
	// If query contains a space we already returned above; bare "@" is active.
	return q, at, true
}

func isMentionBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '(' || r == '[' || r == '{' ||
		r == ',' || r == ':' || r == ';'
}

func isMentionNameRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return r == '_' || r == '-' || r == '.'
}

// wsMentionCandidates members matching the trailing @query (prefix, then contains).
func (m Model) wsMentionCandidates() []workspace.Member {
	q, _, ok := m.wsMentionQuery()
	if !ok {
		return nil
	}
	ql := strings.ToLower(q)
	var prefix, rest []workspace.Member
	seen := map[string]bool{}
	for _, mem := range m.wsMembers {
		name := strings.TrimSpace(mem.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		nl := strings.ToLower(name)
		if ql == "" || strings.HasPrefix(nl, ql) {
			prefix = append(prefix, mem)
			seen[nl] = true
		} else if strings.Contains(nl, ql) {
			rest = append(rest, mem)
			seen[nl] = true
		}
	}
	out := append(prefix, rest...)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func (m *Model) wsClampMentionIdx() {
	cands := m.wsMentionCandidates()
	if len(cands) == 0 {
		m.wsMentionIdx = 0
		return
	}
	if m.wsMentionIdx < 0 {
		m.wsMentionIdx = 0
	}
	if m.wsMentionIdx >= len(cands) {
		m.wsMentionIdx = len(cands) - 1
	}
}

// wsCompleteMention replaces the trailing @query with @Name .
func (m *Model) wsCompleteMention(cands []workspace.Member) {
	if len(cands) == 0 {
		return
	}
	idx := m.wsMentionIdx
	if idx < 0 || idx >= len(cands) {
		idx = 0
	}
	_, atStart, ok := m.wsMentionQuery()
	if !ok {
		return
	}
	name := strings.TrimSpace(cands[idx].Name)
	if name == "" {
		return
	}
	m.wsPushUndo()
	rs := []rune(m.wsCompose)
	if atStart < 0 || atStart > len(rs) {
		return
	}
	// Keep text before @, then @Name + space.
	m.wsCompose = string(rs[:atStart]) + "@" + name + " "
	m.wsMentionIdx = 0
	m.wsStatus = ""
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
	// Viewport lines often lack full-width panel bg (Charm leaves default-bg
	// cells → host paints them transparent = "holes"). Solidify every row.
	body := solidifyOverlayLines(vp.View(), innerW)

	tabs := solidifyOverlayLines(renderChannelTabs(m.wsChannels, m.wsChannel, innerW), innerW)
	presence := solidifyOverlayLines(renderPresenceStrip(m.wsMembers, innerW), innerW)

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
	compLine := panelFillLine(innerW, prompt+lipgloss.NewStyle().
		Foreground(colText).Background(colPanel).Render(compVisible))

	// @mention picker (when typing @query).
	mentionLine := m.renderMentionPicker(innerW)
	if mentionLine != "" {
		mentionLine = solidifyOverlayLines(mentionLine, innerW)
	}

	// Status: ephemeral action feedback only — path demoted to footer when idle.
	statusLine := ""
	if m.wsStatus != "" {
		statusLine = panelFillLine(innerW, lipgloss.NewStyle().Foreground(colSoft).Background(colPanel).
			Render(ansi.Truncate(m.wsStatus, innerW, "…")))
	}

	parts := []string{tabs, presence, panelFillLine(innerW, ""), body, panelFillLine(innerW, "")}
	if mentionLine != "" {
		parts = append(parts, mentionLine)
	}
	parts = append(parts, compLine)
	if statusLine != "" {
		parts = append(parts, statusLine)
	}

	footer := styleDialogHintKey().Render("enter") + styleDialogHint().Render(" send  ") +
		styleDialogHintKey().Render("@") + styleDialogHint().Render(" mention  ") +
		styleDialogHintKey().Render("tab") + styleDialogHint().Render(" ch  ") +
		styleDialogHintKey().Render("⌃n") + styleDialogHint().Render(" new  ") +
		styleDialogHintKey().Render("esc")
	return renderWorkspaceCard(outer, "Workspace", parts, footer)
}

// solidifyOverlayLines forces every line to full width with panel background so
// the host never treats default-bg cells as transparent holes inside the card.
func solidifyOverlayLines(s string, width int) string {
	if s == "" {
		return panelFillLine(width, "")
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = panelFillLine(width, line)
	}
	return strings.Join(lines, "\n")
}

// renderMentionPicker paints live @candidates above the compose line.
func (m Model) renderMentionPicker(width int) string {
	cands := m.wsMentionCandidates()
	if len(cands) == 0 {
		return ""
	}
	idx := m.wsMentionIdx
	if idx < 0 || idx >= len(cands) {
		idx = 0
	}
	var chips []string
	used := 0
	for i, mem := range cands {
		label := "@" + mem.Name
		var chip string
		if i == idx {
			chip = lipgloss.NewStyle().
				Background(colPrimary).
				Foreground(colOnPrimary).
				Bold(true).
				Padding(0, 1).
				Render(label)
		} else {
			chip = lipgloss.NewStyle().
				Foreground(colCyan).
				Background(colPanel).
				Padding(0, 1).
				Render(label)
		}
		w := lipgloss.Width(chip)
		if used > 0 && used+1+w > width {
			break
		}
		if used > 0 {
			used++
		}
		chips = append(chips, chip)
		used += w
	}
	hint := lipgloss.NewStyle().Foreground(colMute).Background(colPanel).
		Render("  tab/enter insert")
	line := strings.Join(chips, " ")
	if lipgloss.Width(line)+lipgloss.Width(hint) <= width {
		line += hint
	}
	return line
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
		Background(colPanel).
		Padding(0, 1)
	moreStyle := lipgloss.NewStyle().Foreground(colMute).Background(colPanel).Padding(0, 1)

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
		return lipgloss.NewStyle().Foreground(colMute).Background(colPanel).Render("no members yet")
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
	return lipgloss.NewStyle().Foreground(color).Background(colPanel).Render(chip)
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
//
// Fills are always opaque (colPanel): the host treats default-bg VT cells as
// transparent so rain/shell shows through — never leave bubble guts or side
// gutters without an explicit panel background.
// memberNames maps lowercased name → display name for @highlight.
func formatChatBubble(msg workspace.Message, width int, me string, memberNames map[string]string) []string {
	if width < 20 {
		width = 20
	}
	ts := msg.TS.Local().Format("15:04")
	name := msg.FromName
	if name == "" {
		name = "?"
	}
	// Solid fill for every bubble cell (same surface as the dialog body).
	fill := colPanel

	if msg.Kind == "system" {
		body := strings.ReplaceAll(msg.Body, "\n", " ")
		line := fmt.Sprintf("— %s · %s —", body, ts)
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		return []string{lipgloss.NewStyle().
			Foreground(colMute).
			Background(fill).
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

	// Bubbles use most of the (now full-width) row; leave a small indent.
	bubbleW := width * 88 / 100
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
	// Nested spans must also set Background — bare Foreground SGR clears fill
	// on the host paint path (default bg → transparent).
	header := lipgloss.NewStyle().
		Foreground(colSoft).
		Background(fill).
		Render(fmt.Sprintf("%s  %s", label, ts))

	// Highlight @mentions then wrap (wrap on plain; re-style per line is lossy for
	// multi-span — paint each wrap line with styled mentions on that slice).
	styledBody := styleMentionsInText(bodyText, memberNames, fill)
	// For wrapping we use plain text; then re-apply mention style per line.
	var bodyLines []string
	if wl := wrapWords(bodyText, textW); len(wl) > 0 {
		bodyLines = wl
	} else {
		bodyLines = []string{""}
	}
	// Paint body lines with fill; re-highlight mentions per visual line.
	_ = styledBody
	for i, bl := range bodyLines {
		bodyLines[i] = styleMentionsInText(bl, memberNames, fill)
		// Ensure line spans textW with opaque fill (styleMentions may be short).
		bodyLines[i] = lipgloss.NewStyle().
			Background(fill).
			Width(textW).
			MaxHeight(1).
			Render(bodyLines[i])
	}
	inner := header + "\n" + strings.Join(bodyLines, "\n")

	var borderFg lipgloss.Color
	if mine {
		borderFg = colPrimary
	} else if msg.FromKind == workspace.KindAgent {
		borderFg = colCyan
	} else {
		borderFg = colDim
	}

	bubble := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderFg).
		BorderBackground(fill).
		Background(fill).
		Foreground(colText).
		Padding(0, 1).
		Width(bubbleW).
		Render(inner)

	align := lipgloss.Left
	if mine {
		align = lipgloss.Right
	}
	// Place with opaque panel padding — lipgloss.PlaceHorizontal uses default-bg
	// spaces that paint as transparent holes in the modal.
	return strings.Split(placeOpaque(width, align, bubble, fill), "\n")
}

// styleMentionsInText highlights @Name tokens that match known members.
func styleMentionsInText(text string, memberNames map[string]string, fill lipgloss.Color) string {
	if text == "" {
		return lipgloss.NewStyle().Foreground(colText).Background(fill).Render("")
	}
	if len(memberNames) == 0 {
		return lipgloss.NewStyle().Foreground(colText).Background(fill).Render(text)
	}
	rs := []rune(text)
	var b strings.Builder
	i := 0
	plain := lipgloss.NewStyle().Foreground(colText).Background(fill)
	mention := lipgloss.NewStyle().Foreground(colCyan).Background(fill).Bold(true)
	for i < len(rs) {
		if rs[i] == '@' {
			// Parse @name
			j := i + 1
			for j < len(rs) && isMentionNameRune(rs[j]) {
				j++
			}
			if j > i+1 {
				cand := string(rs[i+1 : j])
				if disp, ok := memberNames[strings.ToLower(cand)]; ok {
					// Prefer stored display casing.
					b.WriteString(mention.Render("@" + disp))
					i = j
					continue
				}
			}
		}
		// Emit one plain rune (batch consecutive non-@ for fewer styles).
		start := i
		i++
		for i < len(rs) && rs[i] != '@' {
			i++
		}
		b.WriteString(plain.Render(string(rs[start:i])))
	}
	return b.String()
}

// placeOpaque left/right-aligns content within width using Background(fill) pads.
func placeOpaque(width int, align lipgloss.Position, content string, fill lipgloss.Color) string {
	cw := lipgloss.Width(content)
	if width < 1 {
		width = 1
	}
	if cw >= width {
		return content
	}
	pad := width - cw
	// Width-only render of empty string → pad spaces carrying fill.
	padStr := lipgloss.NewStyle().Background(fill).Width(pad).MaxHeight(1).Render("")
	if align == lipgloss.Right {
		return padStr + content
	}
	// Center: split pad; default left for everything else.
	if align == lipgloss.Center {
		left := pad / 2
		right := pad - left
		l := lipgloss.NewStyle().Background(fill).Width(left).MaxHeight(1).Render("")
		r := lipgloss.NewStyle().Background(fill).Width(right).MaxHeight(1).Render("")
		return l + content + r
	}
	return content + padStr
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
