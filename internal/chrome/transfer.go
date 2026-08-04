package chrome

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TransferMode is send (path → ticket) or receive (ticket → files).
type TransferMode int

const (
	TransferModeSend TransferMode = iota
	TransferModeReceive
)

// OpenTransferPromptMsg opens the path/ticket input dialog.
type OpenTransferPromptMsg struct {
	Mode TransferMode
	Seed string
}

// TransferStatusMsg updates the floating transfer progress panel (host → chrome).
type TransferStatusMsg struct {
	// Active false closes the panel.
	Active bool
	Phase  string // preparing | ready | receiving | progress | done | error | stopped
	Ticket string
	Done   uint64
	Total  uint64
	// Message is a short status / error line.
	Message string
}

// CloseTransferMsg dismisses prompt and/or panel without cancelling (host cancels separately).
type CloseTransferMsg struct{}

func (m *Model) openTransferPrompt(mode TransferMode, seed string) {
	m.closeModalsExcept("transfer_prompt")
	m.TransferPromptOpen = true
	m.TransferPanelOpen = false
	m.transferMode = mode
	m.transferBuf = strings.TrimSpace(seed)
}

func (m *Model) applyTransferStatus(msg TransferStatusMsg) {
	if !msg.Active {
		m.TransferPanelOpen = false
		m.transferPhase = ""
		m.transferTicket = ""
		m.transferDone = 0
		m.transferTotal = 0
		m.transferMsg = ""
		return
	}
	// Progress panel owns focus over the prompt once transfer starts.
	m.TransferPromptOpen = false
	m.transferBuf = ""
	m.TransferPanelOpen = true
	if msg.Phase != "" {
		m.transferPhase = msg.Phase
	}
	if msg.Ticket != "" {
		m.transferTicket = msg.Ticket
	}
	if msg.Total > 0 || msg.Done > 0 {
		m.transferDone = msg.Done
		m.transferTotal = msg.Total
	}
	if msg.Message != "" {
		m.transferMsg = msg.Message
	}
}

func (m *Model) handleTransferPromptKey(msg tea.KeyMsg) (act HostAction, value string) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.TransferPromptOpen = false
		m.transferBuf = ""
		return ActionNone, ""
	case "enter":
		value = strings.TrimSpace(m.transferBuf)
		if value == "" {
			return ActionNone, ""
		}
		m.TransferPromptOpen = false
		m.transferBuf = ""
		return ActionTransferStart, value
	case "backspace":
		if m.transferBuf != "" {
			rs := []rune(m.transferBuf)
			m.transferBuf = string(rs[:len(rs)-1])
		}
		return ActionNone, ""
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			for _, r := range msg.Runes {
				if r >= 32 {
					m.transferBuf += string(r)
				}
			}
		}
		return ActionNone, ""
	}
}

func (m *Model) handleTransferPanelKey(msg tea.KeyMsg) HostAction {
	switch msg.String() {
	case "esc", "ctrl+c", "q":
		// Close panel + ask host to cancel engine.
		m.TransferPanelOpen = false
		return ActionTransferCancel
	case "c":
		// Copy ticket — host handles clipboard if ticket present.
		if m.transferTicket != "" {
			return ActionTransferCopyTicket
		}
		return ActionNone
	case "enter":
		// Dismiss completed/error panel.
		if m.transferPhase == "done" || m.transferPhase == "error" || m.transferPhase == "stopped" {
			m.TransferPanelOpen = false
			return ActionNone
		}
		return ActionNone
	default:
		return ActionNone
	}
}

func (m Model) renderTransferPrompt(windowCols int) string {
	outer := clampDialogWidth(56, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}
	title := "Send file (ticket)"
	placeholder := "absolute path to file or folder…"
	if m.transferMode == TransferModeReceive {
		title = "Receive ticket"
		placeholder = "paste ticket (blob…)"
	}
	prompt := "> "
	body := m.transferBuf
	placeholderShown := false
	if body == "" {
		body = placeholder
		placeholderShown = true
	}
	var line string
	if placeholderShown {
		p := styleDialogHintKey().Render(prompt)
		rest := styleDialogHint().Render(padFit(body, inner-lipgloss.Width(prompt)))
		line = panelFillLine(inner, p+rest)
	} else {
		// Show end of long paths/tickets.
		plain := prompt + body + "▌"
		if lipgloss.Width(plain) > inner {
			plain = "…" + padFit(prompt+body+"▌", inner-1)
			// simpler: take right side
			rs := []rune(prompt + body + "▌")
			for lipgloss.Width(string(rs)) > inner && len(rs) > 2 {
				rs = rs[1:]
			}
			plain = string(rs)
		} else {
			plain = padFit(plain, inner)
		}
		line = styleDialogHintKey().Width(inner).MaxHeight(1).Render(plain)
	}
	footer := styleDialogHintKey().Render("enter") +
		styleDialogHint().Render(" start  ") +
		styleDialogHintKey().Render("esc") +
		styleDialogHint().Render(" cancel")
	return renderDialogCard(outer, title, []string{line}, footer)
}

func (m Model) renderTransferPanel(windowCols int) string {
	outer := clampDialogWidth(56, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}

	title := "Transfer"
	switch m.transferMode {
	case TransferModeSend:
		title = "Sending"
	case TransferModeReceive:
		title = "Receiving"
	}
	if m.transferPhase == "done" {
		title = "Transfer complete"
	}
	if m.transferPhase == "error" {
		title = "Transfer failed"
	}
	if m.transferPhase == "stopped" {
		title = "Transfer stopped"
	}

	var lines []string
	phase := m.transferPhase
	if phase == "" {
		phase = "…"
	}
	lines = append(lines, styleDialogLabel().Width(inner).MaxHeight(1).Render(
		padFit("phase  "+phase, inner),
	))

	if m.transferTicket != "" {
		t := m.transferTicket
		// Show head…tail so the blob prefix stays readable.
		if len(t) > inner-2 {
			keep := (inner - 5) / 2
			if keep < 8 {
				keep = 8
			}
			if 2*keep+3 < len(t) {
				t = t[:keep] + "…" + t[len(t)-keep:]
			}
		}
		lines = append(lines, styleDialogValue().Width(inner).MaxHeight(1).Render(padFit(t, inner)))
	}

	// Progress bar
	if m.transferTotal > 0 || m.transferDone > 0 {
		barW := inner - 12
		if barW < 8 {
			barW = 8
		}
		pct := 0.0
		if m.transferTotal > 0 {
			pct = float64(m.transferDone) / float64(m.transferTotal)
			if pct > 1 {
				pct = 1
			}
		}
		filled := int(pct * float64(barW))
		if filled > barW {
			filled = barW
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
		label := fmt.Sprintf("%3.0f%%", pct*100)
		lines = append(lines, styleDialogActive().Width(inner).MaxHeight(1).Render(
			padFit(bar+" "+label, inner),
		))
		lines = append(lines, styleDialogHint().Width(inner).MaxHeight(1).Render(
			padFit(fmt.Sprintf("%s / %s", humanBytes(m.transferDone), humanBytes(m.transferTotal)), inner),
		))
	}

	if m.transferMsg != "" {
		lines = append(lines, styleDialogHint().Width(inner).MaxHeight(2).Render(
			padFit(m.transferMsg, inner),
		))
	}

	var footer string
	switch m.transferPhase {
	case "done", "error", "stopped":
		footer = styleDialogHintKey().Render("enter") +
			styleDialogHint().Render(" dismiss")
		if m.transferTicket != "" {
			footer += styleDialogHint().Render("  ") +
				styleDialogHintKey().Render("c") +
				styleDialogHint().Render(" copy ticket")
		}
	case "ready":
		footer = styleDialogHintKey().Render("c") +
			styleDialogHint().Render(" copy ticket  ") +
			styleDialogHintKey().Render("esc") +
			styleDialogHint().Render(" stop")
	default:
		footer = styleDialogHintKey().Render("esc") +
			styleDialogHint().Render(" cancel")
		if m.transferTicket != "" {
			footer += styleDialogHint().Render("  ") +
				styleDialogHintKey().Render("c") +
				styleDialogHint().Render(" copy")
		}
	}
	return renderDialogCard(outer, title, lines, footer)
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
