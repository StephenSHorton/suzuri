package chrome

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/config"
	"github.com/charmbracelet/lipgloss"
)

// settingsField is one row in the settings dialog.
type settingsField int

const (
	fieldFontFace settingsField = iota
	fieldFontSize
	fieldCursor
	fieldTheme
	fieldANSIMap
	fieldProfile
	settingsFieldCount
)

type settingsState struct {
	snap  config.Config
	edit  config.Config
	field settingsField
	fonts []string
}

func newSettingsState(cfg config.Config) settingsState {
	cfg = config.Normalize(cfg)
	fonts := config.MonoFontFaces()
	found := false
	for _, f := range fonts {
		if strings.EqualFold(f, cfg.FontFace) {
			found = true
			cfg.FontFace = f
			break
		}
	}
	if !found {
		fonts = append([]string{cfg.FontFace}, fonts...)
	}
	return settingsState{
		snap:  cfg,
		edit:  cfg,
		field: fieldFontFace,
		fonts: fonts,
	}
}

func (s *settingsState) moveField(delta int) {
	n := int(settingsFieldCount)
	s.field = settingsField((int(s.field) + delta%n + n) % n)
}

func (s *settingsState) nudge(delta int) {
	switch s.field {
	case fieldFontFace:
		i := indexFold(s.fonts, s.edit.FontFace)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(s.fonts)) % len(s.fonts)
		s.edit.FontFace = s.fonts[i]
	case fieldFontSize:
		s.edit.FontSizePx += delta
		s.edit = config.Normalize(s.edit)
	case fieldCursor:
		s.edit.Cursor = config.CursorStyle((int(s.edit.Cursor) + delta%3 + 3) % 3)
	case fieldTheme:
		ids := config.ThemeIDs()
		i := indexFold(ids, s.edit.Theme)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.Theme = ids[i]
	case fieldANSIMap:
		ids := config.ANSIMapIDs()
		i := indexFold(ids, s.edit.ShellANSIMap)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.ShellANSIMap = ids[i]
	case fieldProfile:
		names := config.ProfileNames(s.edit)
		if len(names) == 0 {
			return
		}
		i := indexFold(names, s.edit.ActiveProfile)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(names)) % len(names)
		s.edit.ActiveProfile = names[i]
	}
}

func (s settingsState) valueLabel(f settingsField) string {
	switch f {
	case fieldFontFace:
		return s.edit.FontFace
	case fieldFontSize:
		return fmt.Sprintf("%d", s.edit.FontSizePx)
	case fieldCursor:
		cs := config.CursorString(s.edit.Cursor)
		if cs == "" {
			return cs
		}
		return strings.ToUpper(cs[:1]) + cs[1:]
	case fieldTheme:
		return config.ThemeLabel(s.edit.Theme)
	case fieldANSIMap:
		return config.ANSIMapLabel(s.edit.ShellANSIMap)
	case fieldProfile:
		if s.edit.ActiveProfile == "" {
			return "Default"
		}
		return s.edit.ActiveProfile
	default:
		return ""
	}
}

func (s settingsState) fieldLabel(f settingsField) string {
	switch f {
	case fieldFontFace:
		return "Font"
	case fieldFontSize:
		return "Size"
	case fieldCursor:
		return "Cursor"
	case fieldTheme:
		return "Theme"
	case fieldANSIMap:
		return "ANSI"
	case fieldProfile:
		return "Profile"
	default:
		return ""
	}
}

func (s settingsState) render(windowCols int) string {
	outer := clampDialogWidth(44, windowCols)
	inner := dialogInnerWidth(outer)

	var body []string
	for f := settingsField(0); f < settingsFieldCount; f++ {
		label := s.fieldLabel(f)
		val := s.valueLabel(f)
		maxVal := inner - 16
		if maxVal < 6 {
			maxVal = 6
		}
		if len([]rune(val)) > maxVal {
			rs := []rune(val)
			val = string(rs[:maxVal-1]) + "…"
		}
		if f == s.field {
			// Crush SelectedItem: primary + onPrimary, Padding(0,1)
			line := fmt.Sprintf("%-8s  ‹ %s ›", label, val)
			body = append(body, styleDialogActive().Width(inner).Render(line))
		} else {
			// Crush NormalItem
			lab := styleDialogLabel().Width(10).Render(label)
			v := styleDialogValue().Render(val)
			pad := inner - lipgloss.Width(lab) - lipgloss.Width(v) - 2
			if pad < 1 {
				pad = 1
			}
			row := " " + lab + strings.Repeat(" ", pad) + v
			body = append(body, styleDialogNormalItem().Width(inner).Render(strings.TrimRight(row, " ")))
		}
	}

	footer := styleDialogHintKey().Render("↑↓") + styleDialogHint().Render("  ") +
		styleDialogHintKey().Render("‹›") + styleDialogHint().Render(" change  ") +
		styleDialogHintKey().Render("enter") + styleDialogHint().Render(" save  ") +
		styleDialogHintKey().Render("esc")

	return renderDialogCard(outer, "Settings", body, footer)
}

func indexFold(list []string, want string) int {
	for i, s := range list {
		if strings.EqualFold(s, want) {
			return i
		}
	}
	return -1
}
