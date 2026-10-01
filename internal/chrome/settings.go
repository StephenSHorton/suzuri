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
	fieldShellAmbient
	fieldBackdrop
	fieldShellLogo
	fieldGlassBlur
	fieldGlassVeil
	fieldGlassRim
	fieldRainOpacity // ambient intensity (JSON: shell_matrix_opacity)
	fieldAnimateUnfocused
	fieldNotice
	fieldProfile
	settingsFieldCount
)

// settingsTab is one page of the settings dialog.
type settingsTab int

const (
	tabLook settingsTab = iota
	tabShell
	tabSession
	settingsTabCount
)

// settingsTabFields is the row order on each page.
var settingsTabFields = [settingsTabCount][]settingsField{
	tabLook: {
		fieldFontFace, fieldFontSize, fieldCursor, fieldTheme, fieldANSIMap,
	},
	tabShell: {
		fieldIntro, fieldShellAmbient, fieldRainOpacity, fieldBackdrop, fieldShellLogo,
		fieldGlassBlur, fieldGlassVeil, fieldGlassRim, fieldAnimateUnfocused,
	},
	tabSession: {
		fieldNotice, fieldProfile,
	},
}

var settingsTabNames = [settingsTabCount]string{"Look", "Shell", "Session"}

// rainOpacityStep is left/right nudge size for percent sliders.
const rainOpacityStep = 5

// glassBlurStep is left/right nudge size for the desktop blur radius (points).
const glassBlurStep = 4

// rainOpacityBarCells is the filled/empty bar width in the settings value column.
const rainOpacityBarCells = 10

// Fixed label column so values sit on a clean right column.
const settingsLabelCols = 10

type settingsState struct {
	snap  config.Config
	edit  config.Config
	tab   settingsTab
	field settingsField
	fonts []string
}

// ApplyFontSize updates lastCfg (and live settings edit/snap if open) so zoom
// shortcuts stay consistent with Cancel / palette rebuild.
func (m *Model) ApplyFontSize(px int) {
	if m == nil {
		return
	}
	if px < 10 {
		px = 10
	}
	if px > 36 {
		px = 36
	}
	m.lastCfg.FontSizePx = px
	if m.SettingsOpen {
		m.settings.edit.FontSizePx = px
		m.settings.snap.FontSizePx = px
	}
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
		tab:   tabLook,
		field: fieldFontFace,
		fonts: fonts,
	}
}

func (s *settingsState) fields() []settingsField {
	if s.tab < 0 || s.tab >= settingsTabCount {
		s.tab = tabLook
	}
	return settingsTabFields[s.tab]
}

func (s *settingsState) moveTab(delta int) {
	n := int(settingsTabCount)
	s.tab = settingsTab((int(s.tab) + delta%n + n) % n)
	s.field = s.fields()[0]
}

func (s *settingsState) moveField(delta int) {
	fs := s.fields()
	if len(fs) == 0 {
		return
	}
	i := 0
	for n, f := range fs {
		if f == s.field {
			i = n
			break
		}
	}
	i = (i + delta%len(fs) + len(fs)) % len(fs)
	s.field = fs[i]
}

// SettingsShowcaseIntro is true when Settings should preview the startup
// curtain under the modal (Intro row focused). Otherwise the underlay
// showcases Ambient (default for every other field, including Ambient /
// Intensity while cycling styles).
func (m Model) SettingsShowcaseIntro() bool {
	return m.SettingsOpen && m.settings.field == fieldIntro
}

// SettingsLogoPreview is true while the Logo row is focused, so the host can
// draw the center 硯 where the settings card does not cover it.
func (m Model) SettingsLogoPreview() bool {
	return m.SettingsOpen && m.settings.field == fieldShellLogo
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
	case fieldShellAmbient:
		ids := config.AmbientIDs()
		i := indexFold(ids, s.edit.ShellAmbient)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.ShellAmbient = ids[i]
		s.edit = config.Normalize(s.edit)
	case fieldBackdrop:
		ids := config.BackdropIDs()
		i := indexFold(ids, s.edit.Backdrop)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.Backdrop = ids[i]
	case fieldShellLogo:
		s.edit.ShellLogo += delta * rainOpacityStep
		s.edit = config.Normalize(s.edit)
	case fieldGlassBlur:
		s.edit.GlassBlur += delta * glassBlurStep
		s.edit = config.Normalize(s.edit)
	case fieldGlassVeil:
		s.edit.GlassVeil += delta * rainOpacityStep
		s.edit = config.Normalize(s.edit)
	case fieldGlassRim:
		s.edit.GlassRim += delta * rainOpacityStep
		s.edit = config.Normalize(s.edit)
	case fieldRainOpacity:
		// Slider: left/right steps by 5%. Charm has no native slider widget;
		// we render a block bar and nudge like font size.
		s.edit.ShellMatrixOpacity += delta * rainOpacityStep
		s.edit = config.Normalize(s.edit)
	case fieldAnimateUnfocused:
		s.edit.AnimateUnfocused = !s.edit.AnimateUnfocused
	case fieldNotice:
		ids := config.NoticePositionIDs()
		i := indexFold(ids, s.edit.NoticePosition)
		if i < 0 {
			i = 0
		}
		i = (i + delta + len(ids)) % len(ids)
		s.edit.NoticePosition = ids[i]
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
	case fieldShellAmbient:
		return config.AmbientLabel(s.edit.ShellAmbient)
	case fieldBackdrop:
		return config.BackdropLabel(s.edit.Backdrop)
	case fieldShellLogo:
		return formatRainOpacitySlider(s.edit.ShellLogo, rainOpacityBarCells)
	case fieldGlassBlur:
		return formatScaledSlider(s.edit.GlassBlur, config.GlassBlurMax, rainOpacityBarCells, "pt")
	case fieldGlassVeil:
		return formatRainOpacitySlider(s.edit.GlassVeil, rainOpacityBarCells)
	case fieldGlassRim:
		return formatRainOpacitySlider(s.edit.GlassRim, rainOpacityBarCells)
	case fieldRainOpacity:
		return formatRainOpacitySlider(s.edit.ShellMatrixOpacity, rainOpacityBarCells)
	case fieldAnimateUnfocused:
		if s.edit.AnimateUnfocused {
			return "On"
		}
		return "Off"
	case fieldNotice:
		return config.NoticePositionLabel(s.edit.NoticePosition)
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
	case fieldShellAmbient:
		return "Ambient"
	case fieldBackdrop:
		return "Backdrop"
	case fieldShellLogo:
		return "Logo"
	case fieldGlassBlur:
		return "Blur"
	case fieldGlassVeil:
		return "Veil"
	case fieldGlassRim:
		return "Rim"
	case fieldRainOpacity:
		return "Intensity"
	case fieldAnimateUnfocused:
		return "Bg anim"
	case fieldNotice:
		return "Notices"
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
	body = append(body, s.renderTabRow(inner))
	for _, f := range s.fields() {
		label := s.fieldLabel(f)
		val := s.valueLabel(f)
		active := f == s.field
		body = append(body, settingsRow(inner, labW, valW, label, val, active))
	}

	// One line inside the card (about 46 columns). "change" does not fit with tab.
	footer := styleDialogHintKey().Render("tab") + styleDialogHint().Render(" page  ") +
		styleDialogHintKey().Render("up/down") + styleDialogHint().Render("  ") +
		styleDialogHintKey().Render("left/right") + styleDialogHint().Render("  ") +
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
		paras = []string{config.ThemeDesc(s.edit.Theme)}
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
		paras = []string{config.IntroDesc(s.edit.Intro)}
	case fieldShellAmbient:
		title = "Ambient · " + val
		paras = []string{
			config.AmbientDesc(s.edit.ShellAmbient),
			"Left/right cycles styles. Freezes while typing in the Warp bar so input stays snappy. Use Intensity below to dim or strengthen.",
		}
	case fieldBackdrop:
		title = "Backdrop · " + val
		paras = []string{
			config.BackdropDesc(s.edit.Backdrop),
			"Left/right switches. Empty cells update immediately. Enter saves, Esc restores the previous choice.",
		}
	case fieldShellLogo:
		title = "Logo · " + fmt.Sprintf("%d%%", s.edit.ShellLogo)
		paras = []string{
			"How solid the center 硯 is, fill and outline together. 0% hides it, 20% is the quiet mark, 100% is solid. Same slider on Solid and Glass.",
			"The small mark in the tab strip stays. Left/right steps by 5%. Enter saves.",
		}
	case fieldGlassBlur:
		title = "Blur · " + fmt.Sprintf("%dpt", s.edit.GlassBlur)
		paras = []string{
			"How much the desktop behind empty cells is frosted, in points. 48 is the original look. 0 is sharp.",
			"Used when Backdrop is Glass. Mac only — Windows keeps a solid shell. Enter saves.",
		}
	case fieldGlassVeil:
		title = "Veil · " + fmt.Sprintf("%d%%", s.edit.GlassVeil)
		paras = []string{
			"Black wash over the glass holes. 0% is fully clear. Higher values darken the desktop so text is easier to read.",
			"The tab strip, typed text, and real cell colors stay solid. Enter saves.",
		}
	case fieldGlassRim:
		title = "Rim · " + fmt.Sprintf("%d%%", s.edit.GlassRim)
		paras = []string{
			"Dark outline behind shell text on a glass hole. 25% is the default. 0% leaves the letter with no outline. The center logo uses the Logo slider instead.",
			"Raise it when the desktop behind the shell is bright. Enter saves.",
		}
	case fieldRainOpacity:
		title = "Intensity · " + fmt.Sprintf("%d%%", s.edit.ShellMatrixOpacity)
		paras = []string{
			"Strength of always-on Ambient underlay (left/right, 5% steps). 100% is the designed default; lower values fade the effect under text and TUIs.",
			"Only applies when Ambient is not Off. Live-previews as you change it — Enter saves.",
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
	case fieldNotice:
		title = "Notices · " + val
		paras = []string{
			"Where notification cards sit on the screen. The nine spots are the four corners, the middle of each edge, and the center.",
			"Left/right moves through them. A sample card follows the spot while you change it. Enter saves.",
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

// formatRainOpacitySlider renders a text slider, e.g. "██████░░░░  60%".
// No Charm slider widget — settings use left/right nudge like Size.
func formatRainOpacitySlider(pct, barW int) string {
	return formatScaledSlider(pct, 100, barW, "%")
}

// formatScaledSlider renders value against max, e.g. "██████░░░░  48pt".
func formatScaledSlider(value, max, barW int, suffix string) string {
	if value < 0 {
		value = 0
	}
	if max < 1 {
		max = 1
	}
	if value > max {
		value = max
	}
	if barW < 6 {
		barW = 6
	}
	filled := (value*barW + max/2) / max
	if filled > barW {
		filled = barW
	}
	var b strings.Builder
	b.Grow(barW + 8)
	for i := 0; i < barW; i++ {
		if i < filled {
			b.WriteRune('█')
		} else {
			b.WriteRune('░')
		}
	}
	return fmt.Sprintf("%s %3d%s", b.String(), value, suffix)
}

// renderTabRow is the Look / Shell / Session switcher at the top of Settings.
func (s settingsState) renderTabRow(inner int) string {
	var parts []string
	for i := settingsTab(0); i < settingsTabCount; i++ {
		name := settingsTabNames[i]
		if i == s.tab {
			parts = append(parts, styleDialogActive().Render(name))
		} else {
			parts = append(parts, styleDialogNormalItem().Render(name))
		}
	}
	gap := styleDialogHint().Render(" ")
	row := strings.Join(parts, gap)
	if lipgloss.Width(row) > inner {
		return styleDialogNormalItem().Width(inner).MaxHeight(1).Render(strings.Join(settingsTabNames[:], " "))
	}
	return row
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
