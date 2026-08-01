package chrome

import (
	"fmt"
	"strings"

	"github.com/StephenSHorton/suzuri/internal/config"
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
	snap  config.Config // opened-with (Esc restores)
	edit  config.Config // live edit
	field settingsField
	fonts []string
}

func newSettingsState(cfg config.Config) settingsState {
	cfg = config.Normalize(cfg)
	fonts := config.MonoFontFaces()
	// Ensure current face is in the cycle list.
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
		return fmt.Sprintf("%d px", s.edit.FontSizePx)
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
		return "Font face"
	case fieldFontSize:
		return "Font size"
	case fieldCursor:
		return "Cursor"
	case fieldTheme:
		return "Theme"
	case fieldANSIMap:
		return "Shell ANSI"
	case fieldProfile:
		return "Profile"
	default:
		return ""
	}
}

func (s settingsState) render(width int) string {
	if width < 28 {
		width = 28
	}
	title := styleDialogTitle().Render("Settings")
	var rows []string
	rows = append(rows, title)
	rows = append(rows, "")
	for f := settingsField(0); f < settingsFieldCount; f++ {
		label := s.fieldLabel(f)
		val := s.valueLabel(f)
		// Truncate long font names.
		maxVal := width - 18
		if maxVal < 8 {
			maxVal = 8
		}
		if len([]rune(val)) > maxVal {
			rs := []rune(val)
			val = string(rs[:maxVal-1]) + "…"
		}
		if f == s.field {
			line := fmt.Sprintf("%-12s  ◂ %s ▸", label, val)
			rows = append(rows, styleDialogActive().Width(width-4).Render(line))
		} else {
			lab := styleDialogLabel().Render(fmt.Sprintf("%-12s", label))
			v := styleDialogValue().Render("  " + val)
			rows = append(rows, lab+v)
		}
	}
	rows = append(rows, "")
	rows = append(rows, styleDialogHint().Render("↑↓ field  ←→ change  enter save  esc cancel"))
	body := strings.Join(rows, "\n")
	return stylePaletteBorder().Width(width).Render(body)
}

func indexFold(list []string, want string) int {
	for i, s := range list {
		if strings.EqualFold(s, want) {
			return i
		}
	}
	return -1
}
