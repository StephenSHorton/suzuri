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
	fieldIntro
	fieldShellMatrix
	fieldAnimateUnfocused
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
	case fieldIntro:
		ids := config.IntroIDs()
		i := indexFold(ids, s.edit.Intro)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.Intro = ids[i]
	case fieldShellMatrix:
		s.edit.ShellMatrix = !s.edit.ShellMatrix
	case fieldAnimateUnfocused:
		s.edit.AnimateUnfocused = !s.edit.AnimateUnfocused
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
	case fieldIntro:
		return config.IntroLabel(s.edit.Intro)
	case fieldShellMatrix:
		if s.edit.ShellMatrix {
			return "On"
		}
		return "Off"
	case fieldAnimateUnfocused:
		if s.edit.AnimateUnfocused {
			return "On"
		}
		return "Off"
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
	case fieldIntro:
		return "Intro"
	case fieldShellMatrix:
		return "Rain"
	case fieldAnimateUnfocused:
		return "Bg anim"
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

	// Title carries the running build so users can see version without CLI.
	title := "Settings · v" + AppVersion()
	main := renderDialogCard(outer, title, body, footer)
	// Match the rendered settings card width (includes border) so the help
	// panel centers flush under it rather than left-aligning narrower.
	mainW := lipgloss.Width(main)
	info := s.renderHelpCard(mainW, inner)
	return lipgloss.JoinVertical(lipgloss.Center, main, info)
}

// renderHelpCard is a caption under Settings for the active field.
// Panel fill for readability, but no border — not a second interactive dialog.
func (s settingsState) renderHelpCard(width, inner int) string {
	title, paras := s.helpContent()
	if title == "" {
		title = "About"
	}
	if width < 20 {
		width = 20
	}
	// Content width inside help padding (Padding 1,2 → 4 horizontal cells).
	contentW := width - 4
	if contentW < 12 {
		contentW = 12
	}
	if contentW > inner {
		// Prefer settings body column when help is as wide as the bordered card.
		contentW = inner
	}
	var lines []string
	lines = append(lines, styleSettingsHelpTitle().
		Background(colPanel).
		Width(contentW).
		MaxHeight(1).
		Render(title))
	for i, p := range paras {
		if i > 0 {
			lines = append(lines, panelFillLine(contentW, ""))
		}
		for _, line := range wrapWords(p, contentW) {
			lines = append(lines, styleSettingsHelpBody().
				Background(colPanel).
				Width(contentW).
				MaxHeight(1).
				Render(line))
		}
	}
	content := joinLines(lines)
	// Filled panel, no outline — full width matches settings card for centering.
	block := styleSettingsHelpPanel().Width(width).Render(content)
	return lipgloss.NewStyle().MarginTop(1).Render(block)
}

// helpContent returns a short title + 1–2 paragraphs for the focused setting.
// Title includes the current value so left/right updates the blurb live.
func (s settingsState) helpContent() (title string, paras []string) {
	val := s.valueLabel(s.field)
	switch s.field {
	case fieldFontFace:
		title = "Font · " + val
		paras = []string{
			"Monospaced face for the shell grid, tab strip, and input bar. Bundled Gohu is tuned for 14px cells; other faces use Windows metrics.",
		}
	case fieldFontSize:
		title = "Size · " + val + "px"
		paras = []string{
			"Cell height in pixels. Larger sizes grow the grid; the window keeps the same pixel size so you see fewer rows/columns.",
		}
	case fieldCursor:
		title = "Cursor · " + val
		switch s.edit.Cursor {
		case config.CursorUnderline:
			paras = []string{"Thin underline caret in the Warp input bar (and alt-screen apps that show a cursor)."}
		case config.CursorBar:
			paras = []string{"Vertical bar caret — classic editor style in the input bar."}
		default:
			paras = []string{"Block caret that fills the cell. Default, easy to spot while typing."}
		}
	case fieldTheme:
		title = "Theme · " + val
		switch s.edit.Theme {
		case config.ThemeCharmtone:
			paras = []string{"Warm violet/pink chrome inspired by Charm. Shell ANSI colors follow when ANSI is Soft or Full."}
		case config.ThemeHighContrast:
			paras = []string{"Punchy green-on-black chrome for maximum contrast. Best for bright rooms or low vision."}
		default:
			paras = []string{"Inkstone — cool mauve on dark grey. The default suzuri look (硯)."}
		}
	case fieldANSIMap:
		title = "ANSI · " + val
		switch s.edit.ShellANSIMap {
		case config.ANSIMapNone:
			paras = []string{
				"Stock: paint shell SGR 0–15 with the conventional VT palette.",
				"No theme tint — closest to a raw terminal (PowerShell, ls, etc.).",
			}
		case config.ANSIMapFull:
			paras = []string{
				"Full theme: remap every basic ANSI color to the active theme.",
				"Strongest match to chrome; some tools may look less “classic”.",
			}
		default:
			paras = []string{
				"Soft Charm: blend stock VT colors with the theme (~50/50).",
				"Keeps tools readable while picking up a light theme tint. Default.",
			}
		}
	case fieldIntro:
		title = "Intro · " + val
		switch s.edit.Intro {
		case config.IntroRipple:
			paras = []string{
				"Puddle of 猫/咪 rings expanding from the center mark.",
				"Wave colors: theme → white → theme → black. Save & relaunch to preview.",
			}
		case config.IntroNone:
			paras = []string{"Skip the startup curtain. The center 硯 still fades in quietly."}
		default:
			paras = []string{
				"Digital rain (Matrix-style) over the shell for ~2s, then streams fall off.",
				"Skipped automatically when Rain (always-on shell matrix) is On — no double curtain.",
			}
		}
	case fieldShellMatrix:
		title = "Rain · " + val
		if s.edit.ShellMatrix {
			paras = []string{
				"Always-on digital rain under empty shell cells — very dim so text stays readable.",
				"Hides under full-screen apps and while the startup intro is playing.",
			}
		} else {
			paras = []string{"No background rain in the shell. Intro and Settings rain are unchanged."}
		}
	case fieldAnimateUnfocused:
		title = "Bg anim · " + val
		if s.edit.AnimateUnfocused {
			paras = []string{
				"Keep repainting when suzuri is not the focused window — rain, tab spinners, and caret stay smooth in the background.",
			}
		} else {
			paras = []string{
				"Pause animation clocks when another app has focus. Saves a bit of CPU; rain and spinners freeze until you return.",
			}
		}
	case fieldProfile:
		title = "Profile · " + val
		paras = []string{
			"Launch recipe for new tabs (shell command + working directory). The active profile is used when you open + or Ctrl+Shift+T.",
		}
	default:
		title = "About"
		paras = []string{"Select a row for details."}
	}
	return title, paras
}

// wrapWords hard-wraps plain text to at most width columns (rune-based).
func wrapWords(s string, width int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if width < 8 {
		width = 8
	}
	words := strings.Fields(s)
	var lines []string
	var cur string
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		// +1 for the space
		if lipgloss.Width(cur)+1+lipgloss.Width(w) <= width {
			cur = cur + " " + w
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	// Hard-split any single token longer than width.
	var out []string
	for _, line := range lines {
		for lipgloss.Width(line) > width {
			rs := []rune(line)
			// Approximate: cut by rune count (mono UI; CJK rare in help).
			n := width
			if n > len(rs) {
				n = len(rs)
			}
			out = append(out, string(rs[:n]))
			line = string(rs[n:])
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
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

