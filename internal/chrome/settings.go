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

// Fixed label column so values sit on a clean right column.
const settingsLabelCols = 10

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
	outer := clampDialogWidth(52, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 24 {
		inner = 24
	}

	// Two columns: fixed label width, values start at a consistent column.
	// Avoid nested Width()+space-pad math (that reflowed into staggered lines).
	labW := settingsLabelCols
	valW := inner - labW - 1
	if valW < 8 {
		valW = 8
	}

	var body []string
	for f := settingsField(0); f < settingsFieldCount; f++ {
		label := s.fieldLabel(f)
		val := s.valueLabel(f)
		active := f == s.field
		body = append(body, settingsRow(inner, labW, valW, label, val, active))
	}

	footer := styleDialogHintKey().Render("up/down") + styleDialogHint().Render("  ") +
		styleDialogHintKey().Render("left/right") + styleDialogHint().Render(" change  ") +
		styleDialogHintKey().Render("enter") + styleDialogHint().Render(" save  ") +
		styleDialogHintKey().Render("esc")

	return renderDialogCard(outer, "Settings", body, footer)
}

// settingsRow is one label | value line.
// Built as a single plain string first (fixed columns), then styled once —
// nested Width/JoinHorizontal reflowed into staggered stacks with some fonts.
func settingsRow(inner, labW, valW int, label, val string, active bool) string {
	_ = valW
	lab := padFit(label, labW)
	// Value starts immediately after label column.
	plain := padFit(lab+val, inner)
	if active {
		// Whole-row selection; keep plain columns (no fancy ‹› glyphs).
		return styleDialogActive().Width(inner).MaxHeight(1).Render(plain)
	}
	// Dim label, bright value — style segments of the same fixed layout.
	labPart := styleDialogLabel().Render(lab)
	valPart := styleDialogValue().Render(padFit(val, inner-labW))
	row := labPart + valPart
	if lipgloss.Width(row) > inner {
		return styleDialogNormalItem().Width(inner).MaxHeight(1).Render(plain)
	}
	return row
}

// padFit truncates with ellipsis and right-pads with spaces to width n (runes).
func padFit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) > n {
		if n == 1 {
			return "…"
		}
		return string(rs[:n-1]) + "…"
	}
	if len(rs) < n {
		return s + strings.Repeat(" ", n-len(rs))
	}
	return s
}

func indexFold(list []string, want string) int {
	for i, s := range list {
		if strings.EqualFold(s, want) {
			return i
		}
	}
	return -1
}

