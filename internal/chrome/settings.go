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

func (s settingsState) render(width int) string {
	if width < 30 {
		width = 30
	}
	inner := width - 6
	if inner < 20 {
		inner = 20
	}

	title := styleDialogTitle().Render("Settings")
	rule := styleDialogRule().Render(strings.Repeat("─", inner))

	var rows []string
	rows = append(rows, title)
	rows = append(rows, rule)

	for f := settingsField(0); f < settingsFieldCount; f++ {
		label := s.fieldLabel(f)
		val := s.valueLabel(f)
		maxVal := inner - 14
		if maxVal < 6 {
			maxVal = 6
		}
		if len([]rune(val)) > maxVal {
			rs := []rune(val)
			val = string(rs[:maxVal-1]) + "…"
		}
		if f == s.field {
			// Calm selected row: no neon side bar, soft fill + arrows.
			line := fmt.Sprintf("%-8s  %s  %s  %s", label, "‹", val, "›")
			rows = append(rows, styleDialogActive().Width(inner).Render(line))
		} else {
			lab := styleDialogLabel().Width(10).Render(label)
			v := styleDialogValue().Render(val)
			pad := inner - lipgloss.Width(lab) - lipgloss.Width(v)
			if pad < 1 {
				pad = 1
			}
			rows = append(rows, lab+strings.Repeat(" ", pad)+v)
		}
	}

	rows = append(rows, rule)
	rows = append(rows, styleDialogHint().Render("↑↓  ·  ‹› change  ·  enter save  ·  esc"))
	return stylePaletteBorder().Width(width).Render(strings.Join(rows, "\n"))
}

func indexFold(list []string, want string) int {
	for i, s := range list {
		if strings.EqualFold(s, want) {
			return i
		}
	}
	return -1
}
