// Package config holds suzuri product settings with JSON persistence under
// the OS config directory (Windows: %LOCALAPPDATA%\suzuri\,
// macOS: ~/Library/Application Support/suzuri/,
// Linux: ~/.config/suzuri/).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CursorStyle is how the caret is drawn.
type CursorStyle int

const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Theme IDs for chrome (+ shell ANSI remap).
const (
	ThemeInkstone     = "inkstone"
	ThemeCharmtone    = "charmtone"
	ThemeHighContrast = "high_contrast"
	ThemeNord         = "nord"
	ThemeDracula      = "dracula"
	ThemeTokyoNight   = "tokyo_night"
	ThemeCatppuccin   = "catppuccin"
	ThemeGruvbox      = "gruvbox"
	ThemeOneDark      = "one_dark"
	ThemeSolarized    = "solarized"
	ThemeRosePine     = "rose_pine"
	ThemeKanagawa     = "kanagawa"
	ThemeMonokai      = "monokai"
	ThemeForest       = "forest"
	ThemeOcean        = "ocean"
	ThemeAmber        = "amber"
)

// Shell ANSI map modes.
const (
	ANSIMapNone = "none"
	ANSIMapSoft = "soft"
	ANSIMapFull = "full"
)

// Startup intro styles (shell curtain after launch).
const (
	IntroMatrix  = "matrix"   // digital rain
	IntroRipple  = "ripple"   // 猫咪 puddle from center mark
	IntroInkWash = "ink_wash" // ink blot from 硯
	IntroCRT     = "crt"      // scanline / phosphor boot
	IntroNone    = "none"     // skip curtain
)

// Always-on shell ambient under empty cells (settings "Ambient").
const (
	AmbientNone      = "none"
	AmbientRain      = "rain"      // matrix-style digital rain (legacy ShellMatrix)
	AmbientGrain     = "grain"     // paper/film noise
	AmbientWaves     = "waves"     // slow seigaiha-like waves
	AmbientFireflies = "fireflies" // sparse drifting sparks
	AmbientCRT       = "crt"       // scanlines + soft vignette
)

// Window backdrop behind empty / near-black shell cells.
const (
	BackdropSolid = "solid" // opaque void (shipping default)
	BackdropGlass = "glass" // desktop shows through those cells, blurred on macOS
)

// Glass knobs apply when Backdrop is glass. Blur is the macOS desktop blur
// radius in points. Windows has no radius: 0 is Mica, 1–39 Acrylic, 40–80
// Tabbed, and the slider also drives the acrylic frost alpha so every step
// is visible. Veil is a black wash over the holes, 0–100. Rim is the dark
// glyph outline on those holes, 0–100.
const (
	GlassBlurDefault = 48
	GlassBlurMax     = 80
	GlassVeilDefault = 0
	GlassRimDefault  = 25 // light outline; a saved glass_rim value wins
	ShellLogoDefault = 20 // quiet center mark; 100 is solid
)

// logoValue is the center-mark opacity. It accepts a percent, or the earlier
// on/off bool (true = 100, false = 0).
type logoValue struct {
	set bool
	n   int
}

func (v logoValue) IsZero() bool { return !v.set }

func (v logoValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.n)
}

func (v *logoValue) UnmarshalJSON(b []byte) error {
	switch strings.TrimSpace(string(b)) {
	case "true":
		v.set, v.n = true, ShellLogoDefault
		return nil
	case "false":
		v.set, v.n = true, 0
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	v.set, v.n = true, n
	return nil
}

// Profile is a named shell launch recipe (cwd + command + optional theme).
type Profile struct {
	Name  string `json:"name"`
	Shell string `json:"shell,omitempty"` // empty → DefaultShell
	Cwd   string `json:"cwd,omitempty"`   // empty → user home directory
	Theme string `json:"theme,omitempty"` // empty → keep current theme
}

// Config holds product settings.
type Config struct {
	Cursor        CursorStyle
	FontFace      string
	FontSizePx    int
	Theme         string
	ShellANSIMap  string
	Profiles      []Profile
	ActiveProfile string // name of default profile for new tabs
	FirstRunDone  bool
	// Intro is the post-launch shell curtain (matrix | ripple | ink_wash | crt | none).
	Intro string
	// ShellAmbient is the always-on underlay under empty shell cells
	// (none | rain | grain | waves | fireflies | crt).
	ShellAmbient string
	// ShellMatrix is retained for older configs/code: true when ambient is rain.
	// Prefer ShellAmbient. Normalize keeps them in sync.
	ShellMatrix bool
	// ShellMatrixOpacity is 0–100 intensity for any always-on ambient
	// (multiplies host base strength). 100 = designed default; 0 = invisible.
	// JSON key stays shell_matrix_opacity for backward compatibility.
	ShellMatrixOpacity int
	// AnimateUnfocused keeps the paint clock running when another app has focus
	// (matrix rain, tab spinner, caret). Off freezes chrome animation in background.
	AnimateUnfocused bool
	// Backdrop is what shows through empty and near-black shell cells
	// (solid | glass). Glass is a Mac blur; Windows uses DWM Mica/Acrylic.
	Backdrop string
	// ShellLogo is the center 硯 opacity, 0–100. 0 hides it, 100 is solid.
	// The default is the quiet mark. Same scale on Solid and Glass.
	// The tab-strip mark stays.
	ShellLogo int
	// GlassBlur, GlassVeil, and GlassRim tune glass. See the Glass*Default constants.
	GlassBlur int
	GlassVeil int
	GlassRim  int
	// NoticePosition is where desktop notification cards sit
	// (bottom-left, bottom-center, center, top-right, …). See NoticePositionIDs.
	NoticePosition string
	// Window is last outer frame placement (multi-monitor). Zero = use default.
	Window WindowPlacement
}

// WindowPlacement is the outer frame rect in screen coordinates (Win32).
// Used to reopen on the same monitor/size as last session.
type WindowPlacement struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized,omitempty"`
}

// Valid reports whether placement is usable for CreateWindow / SetWindowPos.
func (w WindowPlacement) Valid() bool {
	if w.Width < 320 || w.Height < 200 {
		return false
	}
	if w.Width > 16000 || w.Height > 16000 {
		return false
	}
	// Reject "empty" zero structs from old configs.
	if w.X == 0 && w.Y == 0 && w.Width == 0 && w.Height == 0 {
		return false
	}
	return true
}

type fileDTO struct {
	FontFace      string    `json:"font_face"`
	FontSizePx    int       `json:"font_size_px"`
	Cursor        string    `json:"cursor"`
	Theme         string    `json:"theme"`
	ShellANSIMap  string    `json:"shell_ansi_map"`
	Profiles      []Profile `json:"profiles,omitempty"`
	ActiveProfile string    `json:"active_profile,omitempty"`
	FirstRunDone  bool      `json:"first_run_done,omitempty"`
	Intro         string    `json:"intro,omitempty"`
	ShellAmbient  string    `json:"shell_ambient,omitempty"`
	Backdrop      string    `json:"backdrop,omitempty"`
	GlassBlurPtr  *int      `json:"glass_blur,omitempty"`
	GlassVeilPtr  *int      `json:"glass_veil,omitempty"`
	GlassRimPtr   *int      `json:"glass_rim,omitempty"`
	ShellLogo     logoValue `json:"shell_logo,omitempty"`
	// Ptr fields distinguish "missing" from false / 0 when loading JSON.
	ShellMatrixPtr        *bool           `json:"shell_matrix,omitempty"`
	ShellMatrixOpacityPtr *int            `json:"shell_matrix_opacity,omitempty"`
	AnimateUnfocusedPtr   *bool           `json:"animate_unfocused,omitempty"`
	NoticePosition        string          `json:"notice_position,omitempty"`
	Window                WindowPlacement `json:"window,omitempty"`
}

// DefaultFontFace is the shipping monospaced face (bundled GohuFont uni14 Mono).
// The binary embeds the TTF and registers it process-privately at startup.
// Gohu is a bitmap-rooted face designed for 14px cells — keep DefaultFontSizePx=14.
const DefaultFontFace = "GohuFont uni14 Nerd Font Mono"

// DefaultFontSizePx matches GohuFont uni14's design size.
const DefaultFontSizePx = 14

// Default returns shipping defaults.
func Default() Config {
	return Config{
		Cursor:             CursorBlock,
		FontFace:           DefaultFontFace,
		FontSizePx:         DefaultFontSizePx,
		Theme:              ThemeHighContrast,
		ShellANSIMap:       ANSIMapSoft,
		Intro:              IntroMatrix,
		ShellAmbient:       AmbientRain,      // quiet always-on rain under shell cells
		ShellMatrix:        true,             // mirrors ambient==rain for legacy
		ShellMatrixOpacity: 100,              // full designed intensity
		AnimateUnfocused:   true,             // keep ambient/spinners smooth in the background
		Backdrop:           BackdropSolid,    // opaque shell; glass is opt-in
		ShellLogo:          ShellLogoDefault, // quiet center 硯; 100 is solid
		GlassBlur:          GlassBlurDefault,
		GlassVeil:          GlassVeilDefault,
		GlassRim:           GlassRimDefault,
		NoticePosition:     NoticeBottomLeft,
		Profiles:           DefaultProfiles(),
		ActiveProfile:      "Default",
		FirstRunDone:       false,
	}
}

// DefaultProfiles are built-in launch recipes.
func DefaultProfiles() []Profile {
	if runtime.GOOS == "windows" {
		return []Profile{
			{Name: "Default", Shell: "", Cwd: ""},
			{Name: "PowerShell", Shell: `powershell.exe -NoLogo -NoProfile`, Cwd: ""},
			{Name: "Cmd", Shell: `cmd.exe`, Cwd: ""},
		}
	}
	return []Profile{
		{Name: "Default", Shell: "", Cwd: ""},
		{Name: "Zsh", Shell: "/bin/zsh", Cwd: ""},
		{Name: "Bash", Shell: "/bin/bash", Cwd: ""},
	}
}

// Dir is the suzuri config/data directory for this OS.
func Dir() string {
	// Windows portable layout prefers LOCALAPPDATA when set.
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "suzuri")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "suzuri")
	}
	return filepath.Join(os.TempDir(), "suzuri")
}

// Path is the config.json location under Dir().
func Path() string {
	return filepath.Join(Dir(), "config.json")
}

// Load reads config from disk, or returns Default if missing/invalid.
func Load() (Config, error) {
	path := Path()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Default(), err
	}
	var dto fileDTO
	if err := json.Unmarshal(b, &dto); err != nil {
		return Default(), fmt.Errorf("parse config: %w", err)
	}
	return Normalize(fromDTO(dto)), nil
}

// Save writes config atomically (temp + rename).
func Save(c Config) error {
	c = Normalize(c)
	dir := filepath.Dir(Path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(toDTO(c), "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := Path()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	// On Windows, Rename may fail if the destination exists — replace explicitly.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmp, path); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
	}
	return nil
}

// Normalize clamps fields to safe values.
func Normalize(c Config) Config {
	d := Default()
	if strings.TrimSpace(c.FontFace) == "" {
		c.FontFace = d.FontFace
	}
	if c.FontSizePx < 10 {
		c.FontSizePx = 10
	}
	if c.FontSizePx > 36 {
		c.FontSizePx = 36
	}
	switch c.Cursor {
	case CursorBlock, CursorUnderline, CursorBar:
	default:
		c.Cursor = CursorBlock
	}
	if id := strings.ToLower(strings.TrimSpace(c.Theme)); ValidTheme(id) {
		c.Theme = id
	} else {
		c.Theme = ThemeHighContrast
	}
	switch strings.ToLower(strings.TrimSpace(c.ShellANSIMap)) {
	case ANSIMapNone, ANSIMapSoft, ANSIMapFull:
		c.ShellANSIMap = strings.ToLower(strings.TrimSpace(c.ShellANSIMap))
	case "":
		c.ShellANSIMap = d.ShellANSIMap
	default:
		c.ShellANSIMap = ANSIMapSoft
	}
	if id := strings.ToLower(strings.TrimSpace(c.Intro)); ValidIntro(id) {
		c.Intro = id
	} else if strings.TrimSpace(c.Intro) == "" {
		c.Intro = d.Intro
	} else {
		c.Intro = IntroMatrix
	}
	// Ambient: prefer shell_ambient; migrate legacy shell_matrix bool.
	amb := strings.ToLower(strings.TrimSpace(c.ShellAmbient))
	if ValidAmbient(amb) {
		c.ShellAmbient = amb
	} else if amb == "" {
		// No ambient key — derive from ShellMatrix (old configs).
		if c.ShellMatrix {
			c.ShellAmbient = AmbientRain
		} else {
			c.ShellAmbient = AmbientNone
		}
	} else {
		c.ShellAmbient = AmbientRain
	}
	// Keep ShellMatrix in sync so older code paths (matrix intro skip) still work.
	c.ShellMatrix = c.ShellAmbient == AmbientRain
	if id := strings.ToLower(strings.TrimSpace(c.Backdrop)); ValidBackdrop(id) {
		c.Backdrop = id
	} else {
		c.Backdrop = BackdropSolid
	}
	if len(c.Profiles) == 0 {
		c.Profiles = DefaultProfiles()
	}
	// Ensure unique non-empty names.
	seen := map[string]bool{}
	var cleaned []Profile
	for _, p := range c.Profiles {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" || seen[strings.ToLower(p.Name)] {
			continue
		}
		seen[strings.ToLower(p.Name)] = true
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		cleaned = DefaultProfiles()
	}
	c.Profiles = cleaned
	if c.ActiveProfile == "" || FindProfile(c, c.ActiveProfile) == nil {
		c.ActiveProfile = c.Profiles[0].Name
	}
	if c.ShellMatrixOpacity < 0 {
		c.ShellMatrixOpacity = 0
	}
	if c.ShellMatrixOpacity > 100 {
		c.ShellMatrixOpacity = 100
	}
	if c.GlassBlur < 0 {
		c.GlassBlur = 0
	}
	if c.GlassBlur > GlassBlurMax {
		c.GlassBlur = GlassBlurMax
	}
	if c.GlassVeil < 0 {
		c.GlassVeil = 0
	}
	if c.GlassVeil > 100 {
		c.GlassVeil = 100
	}
	if c.GlassRim < 0 {
		c.GlassRim = 0
	}
	if c.GlassRim > 100 {
		c.GlassRim = 100
	}
	if c.ShellLogo < 0 {
		c.ShellLogo = 0
	}
	if c.ShellLogo > 100 {
		c.ShellLogo = 100
	}
	if id := strings.ToLower(strings.TrimSpace(c.NoticePosition)); ValidNoticePosition(id) {
		c.NoticePosition = id
	} else {
		c.NoticePosition = NoticeBottomLeft
	}
	return c
}

// GlassVeilAlpha is the black wash over glass holes, 0–255.
func (c Config) GlassVeilAlpha() byte {
	return pctAlpha(c.GlassVeil)
}

// GlassRimAlpha is the dark glyph outline on glass holes, 0–255.
// ShellLogoOpacity is the center 硯 strength in [0,1]. 1 is a solid mark.
func (c Config) ShellLogoOpacity() float64 {
	n := c.ShellLogo
	if n < 0 {
		n = 0
	}
	if n > 100 {
		n = 100
	}
	return float64(n) / 100
}

func (c Config) GlassRimAlpha() byte {
	return pctAlpha(c.GlassRim)
}

func pctAlpha(pct int) byte {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return byte((pct*255 + 50) / 100)
}

// ShellMatrixOpacity01 returns always-on rain strength in [0,1].
func (c Config) ShellMatrixOpacity01() float64 {
	op := c.ShellMatrixOpacity
	if op < 0 {
		return 0
	}
	if op > 100 {
		return 1
	}
	return float64(op) / 100
}

// FindProfile returns a pointer to a profile by name (case-insensitive), or nil.
func FindProfile(c Config, name string) *Profile {
	for i := range c.Profiles {
		if strings.EqualFold(c.Profiles[i].Name, name) {
			return &c.Profiles[i]
		}
	}
	return nil
}

// ProfileNames lists profile names in order.
func ProfileNames(c Config) []string {
	out := make([]string, len(c.Profiles))
	for i, p := range c.Profiles {
		out[i] = p.Name
	}
	return out
}

// CursorString is the JSON/label form.
func CursorString(c CursorStyle) string {
	switch c {
	case CursorUnderline:
		return "underline"
	case CursorBar:
		return "bar"
	default:
		return "block"
	}
}

// ParseCursor maps a string to CursorStyle.
func ParseCursor(s string) CursorStyle {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "underline":
		return CursorUnderline
	case "bar":
		return CursorBar
	default:
		return CursorBlock
	}
}

// ThemeIDs lists selectable themes in settings cycle order.
func ThemeIDs() []string {
	return []string{
		ThemeInkstone,
		ThemeCharmtone,
		ThemeHighContrast,
		ThemeNord,
		ThemeDracula,
		ThemeTokyoNight,
		ThemeCatppuccin,
		ThemeGruvbox,
		ThemeOneDark,
		ThemeSolarized,
		ThemeRosePine,
		ThemeKanagawa,
		ThemeMonokai,
		ThemeForest,
		ThemeOcean,
		ThemeAmber,
	}
}

// ValidTheme is true for a known theme id (case-sensitive id form).
func ValidTheme(id string) bool {
	for _, t := range ThemeIDs() {
		if t == id {
			return true
		}
	}
	return false
}

// ThemeLabel is a human title for a theme id.
func ThemeLabel(id string) string {
	switch id {
	case ThemeCharmtone:
		return "Charmtone"
	case ThemeHighContrast:
		return "High contrast"
	case ThemeNord:
		return "Nord"
	case ThemeDracula:
		return "Dracula"
	case ThemeTokyoNight:
		return "Tokyo Night"
	case ThemeCatppuccin:
		return "Catppuccin"
	case ThemeGruvbox:
		return "Gruvbox"
	case ThemeOneDark:
		return "One Dark"
	case ThemeSolarized:
		return "Solarized"
	case ThemeRosePine:
		return "Rosé Pine"
	case ThemeKanagawa:
		return "Kanagawa"
	case ThemeMonokai:
		return "Monokai"
	case ThemeForest:
		return "Forest"
	case ThemeOcean:
		return "Ocean"
	case ThemeAmber:
		return "Amber CRT"
	default:
		return "Inkstone"
	}
}

// ThemeDesc is a short settings blurb for a theme id.
func ThemeDesc(id string) string {
	switch id {
	case ThemeCharmtone:
		return "Warm violet/pink chrome inspired by Charm. Shell ANSI follows when ANSI is Soft or Full."
	case ThemeHighContrast:
		return "Punchy green-on-black chrome for maximum contrast. Best for bright rooms or low vision."
	case ThemeNord:
		return "Arctic blue-greys (Nord). Cool, calm, and easy on long sessions."
	case ThemeDracula:
		return "Classic purple-pink Dracula vibes. Bold accents on a deep purple base."
	case ThemeTokyoNight:
		return "Modern night-city blues and magentas. Sharp, dense, and focused."
	case ThemeCatppuccin:
		return "Soft Catppuccin Mocha pastels. Gentle contrast without going pastel-washed."
	case ThemeGruvbox:
		return "Warm retro Gruvbox earth tones. Cozy browns and golds."
	case ThemeOneDark:
		return "Atom One Dark blues and soft greys. Familiar coding default."
	case ThemeSolarized:
		return "Solarized Dark — Ethan Schoonover’s balanced cyan/base palette."
	case ThemeRosePine:
		return "Rosé Pine muted rose and pine. Soft, literary, low glare."
	case ThemeKanagawa:
		return "Kanagawa wave — ink blues and paper golds. Suits the 硯 name."
	case ThemeMonokai:
		return "Classic Monokai magenta/yellow on charcoal. High pop, 2010s energy."
	case ThemeForest:
		return "Deep moss and leaf greens. Quieter than High contrast, still verdant."
	case ThemeOcean:
		return "Deep ocean teal and sky accents. Cool undercurrent for the shell."
	case ThemeAmber:
		return "Amber-on-black CRT terminal nostalgia. Warm phosphor glow."
	default:
		return "Inkstone — cool mauve on dark grey. The default suzuri look (硯)."
	}
}

// ANSIMapIDs lists shell ANSI remap modes.
func ANSIMapIDs() []string {
	return []string{ANSIMapNone, ANSIMapSoft, ANSIMapFull}
}

// ANSIMapLabel is a human title for shell ANSI map mode.
func ANSIMapLabel(id string) string {
	switch id {
	case ANSIMapNone:
		return "Stock"
	case ANSIMapFull:
		return "Full theme"
	default:
		return "Soft Charm"
	}
}

// IntroIDs lists selectable startup intros.
func IntroIDs() []string {
	return []string{IntroMatrix, IntroRipple, IntroInkWash, IntroCRT, IntroNone}
}

// ValidIntro is true for a known intro id.
func ValidIntro(id string) bool {
	for _, x := range IntroIDs() {
		if x == id {
			return true
		}
	}
	return false
}

// IntroLabel is a human title for a startup intro id.
func IntroLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case IntroRipple:
		return "Ripple"
	case IntroInkWash:
		return "Ink wash"
	case IntroCRT:
		return "CRT boot"
	case IntroNone:
		return "None"
	default:
		return "Matrix"
	}
}

// IntroDesc is settings help for an intro id.
func IntroDesc(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case IntroRipple:
		return "Puddle of 猫/咪 rings expanding from the center mark. Live-previews behind Settings while this row is focused (or replay intro anytime)."
	case IntroInkWash:
		return "Ink blot blooms from the 硯 mark, then soaks into the void. On-brand for suzuri. Live-previews behind Settings while this row is focused."
	case IntroCRT:
		return "Scanline phosphor boot — green/amber flash settles into the shell. Pairs with Amber CRT theme. Live-previews behind Settings while this row is focused."
	case IntroNone:
		return "Skip the startup curtain. The center 硯 still fades in quietly."
	default:
		return "Digital rain over the shell for ~2s, then streams fall off. Live-previews behind Settings while this row is focused. Skipped when Ambient is Rain (no double curtain)."
	}
}

// AmbientIDs lists always-on shell underlays.
func AmbientIDs() []string {
	return []string{AmbientRain, AmbientGrain, AmbientWaves, AmbientFireflies, AmbientCRT, AmbientNone}
}

// ValidAmbient is true for a known ambient id.
func ValidAmbient(id string) bool {
	for _, x := range AmbientIDs() {
		if x == id {
			return true
		}
	}
	return false
}

// AmbientLabel is a human title for a shell ambient id.
func AmbientLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case AmbientGrain:
		return "Grain"
	case AmbientWaves:
		return "Waves"
	case AmbientFireflies:
		return "Fireflies"
	case AmbientCRT:
		return "CRT"
	case AmbientNone:
		return "Off"
	default:
		return "Rain"
	}
}

// AmbientDesc is settings help for a shell ambient id.
func AmbientDesc(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case AmbientGrain:
		return "Very sparse, nearly static paper grain (not TV snow). Subtle texture under empty cells."
	case AmbientWaves:
		return "Slow seigaiha-style waves in theme colors. Calm motion under the shell."
	case AmbientFireflies:
		return "A few slow-drifting sparks (not glitter). Quiet night-coding vibe."
	case AmbientCRT:
		return "Scanlines + edge vignette painted over the shell (and a slow bright band). Pair with Amber CRT."
	case AmbientNone:
		return "No always-on underlay. Settings shows a plain matte unless Intro is focused."
	default:
		return "Always-on digital rain under empty/default-bg cells — dim so text stays readable. Shows through TUIs that leave cells transparent. Live-previews behind Settings by default (and while this row is focused)."
	}
}

// Notice card anchors. Index order is the settings cycle and the native
// placement code (0 = bottom-left). Corners, edge centers, and the screen center.
const (
	NoticeBottomLeft   = "bottom-left"
	NoticeBottomCenter = "bottom-center"
	NoticeBottomRight  = "bottom-right"
	NoticeCenterLeft   = "center-left"
	NoticeCenter       = "center"
	NoticeCenterRight  = "center-right"
	NoticeTopLeft      = "top-left"
	NoticeTopCenter    = "top-center"
	NoticeTopRight     = "top-right"
)

// NoticePositionIDs lists anchors in settings left/right order.
// The index is the placement code shared with the native notice panel.
func NoticePositionIDs() []string {
	return []string{
		NoticeBottomLeft,
		NoticeBottomCenter,
		NoticeBottomRight,
		NoticeCenterLeft,
		NoticeCenter,
		NoticeCenterRight,
		NoticeTopLeft,
		NoticeTopCenter,
		NoticeTopRight,
	}
}

// ValidNoticePosition is true for a known anchor id.
func ValidNoticePosition(id string) bool {
	for _, x := range NoticePositionIDs() {
		if x == id {
			return true
		}
	}
	return false
}

// NoticePositionIndex is the placement code for id, or 0 (bottom-left).
func NoticePositionIndex(id string) int {
	id = strings.ToLower(strings.TrimSpace(id))
	for i, x := range NoticePositionIDs() {
		if x == id {
			return i
		}
	}
	return 0
}

// NoticePositionLabel is the settings value for an anchor id.
func NoticePositionLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case NoticeBottomCenter:
		return "Bottom center"
	case NoticeBottomRight:
		return "Bottom right"
	case NoticeCenterLeft:
		return "Center left"
	case NoticeCenter:
		return "Center"
	case NoticeCenterRight:
		return "Center right"
	case NoticeTopLeft:
		return "Top left"
	case NoticeTopCenter:
		return "Top center"
	case NoticeTopRight:
		return "Top right"
	default:
		return "Bottom left"
	}
}

// BackdropIDs lists settings left/right order.
func BackdropIDs() []string {
	return []string{BackdropSolid, BackdropGlass}
}

// ValidBackdrop is true for solid or glass.
func ValidBackdrop(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case BackdropSolid, BackdropGlass:
		return true
	default:
		return false
	}
}

// BackdropLabel is the settings value for a backdrop id.
func BackdropLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case BackdropGlass:
		return "Glass"
	default:
		return "Solid"
	}
}

// BackdropDesc is settings help for a backdrop id.
func BackdropDesc(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case BackdropGlass:
		return "Empty and near-black cells show the desktop. Blur, Veil, and Rim on this page tune how frosted, dark, and outlined that looks. The tab strip, text, and real background colors stay solid. Rain glyphs stay on top. Fullscreen turns this off on Mac. Windows shows Mica (blur 0) or Desktop Acrylic through those cells."
	default:
		return "Opaque shell. Empty cells show the theme void and whatever ambient is on."
	}
}

// AmbientActive is true when an always-on underlay should paint.
func (c Config) AmbientActive() bool {
	return ValidAmbient(c.ShellAmbient) && c.ShellAmbient != AmbientNone
}

// MonoFontFaces are preferred faces for the settings cycle.
// DefaultFontFace is first (bundled into the binary when available).
func MonoFontFaces() []string {
	if runtime.GOOS == "darwin" {
		return []string{
			DefaultFontFace,
			"Menlo",
			"SF Mono",
			"Monaco",
			"JetBrains Mono",
			"Fira Code",
			"Source Code Pro",
			"Courier New",
		}
	}
	return []string{
		DefaultFontFace,
		"Cascadia Mono",
		"Cascadia Code",
		"Consolas",
		"Lucida Console",
		"Courier New",
		"JetBrains Mono",
		"Fira Code",
		"Source Code Pro",
	}
}

func fromDTO(d fileDTO) Config {
	c := Config{
		FontFace:       d.FontFace,
		FontSizePx:     d.FontSizePx,
		Cursor:         ParseCursor(d.Cursor),
		Theme:          d.Theme,
		ShellANSIMap:   d.ShellANSIMap,
		Profiles:       d.Profiles,
		ActiveProfile:  d.ActiveProfile,
		FirstRunDone:   d.FirstRunDone,
		Intro:          d.Intro,
		ShellAmbient:   d.ShellAmbient,
		Backdrop:       d.Backdrop,
		NoticePosition: d.NoticePosition,
		Window:         d.Window,
	}
	dflt := Default()
	if d.ShellMatrixPtr != nil {
		c.ShellMatrix = *d.ShellMatrixPtr
	} else {
		// Only default ShellMatrix when ambient also missing (legacy).
		if strings.TrimSpace(d.ShellAmbient) == "" {
			c.ShellMatrix = dflt.ShellMatrix
		}
	}
	if d.ShellMatrixOpacityPtr != nil {
		c.ShellMatrixOpacity = *d.ShellMatrixOpacityPtr
	} else {
		c.ShellMatrixOpacity = dflt.ShellMatrixOpacity
	}
	if d.AnimateUnfocusedPtr != nil {
		c.AnimateUnfocused = *d.AnimateUnfocusedPtr
	} else {
		c.AnimateUnfocused = dflt.AnimateUnfocused
	}
	if d.GlassBlurPtr != nil {
		c.GlassBlur = *d.GlassBlurPtr
	} else {
		c.GlassBlur = dflt.GlassBlur
	}
	if d.GlassVeilPtr != nil {
		c.GlassVeil = *d.GlassVeilPtr
	} else {
		c.GlassVeil = dflt.GlassVeil
	}
	if d.GlassRimPtr != nil {
		c.GlassRim = *d.GlassRimPtr
	} else {
		c.GlassRim = dflt.GlassRim
	}
	if d.ShellLogo.set {
		c.ShellLogo = d.ShellLogo.n
	} else {
		c.ShellLogo = dflt.ShellLogo
	}
	return c
}

func toDTO(c Config) fileDTO {
	// Keep shell_matrix true only for rain so older builds don't invent rain
	// when ambient is grain/waves/etc.
	sm := c.ShellAmbient == AmbientRain || (c.ShellAmbient == "" && c.ShellMatrix)
	op := c.ShellMatrixOpacity
	au := c.AnimateUnfocused
	blur, veil, rim := c.GlassBlur, c.GlassVeil, c.GlassRim
	logo := logoValue{set: true, n: c.ShellLogo}
	return fileDTO{
		FontFace:              c.FontFace,
		FontSizePx:            c.FontSizePx,
		Cursor:                CursorString(c.Cursor),
		Theme:                 c.Theme,
		ShellANSIMap:          c.ShellANSIMap,
		Profiles:              c.Profiles,
		ActiveProfile:         c.ActiveProfile,
		FirstRunDone:          c.FirstRunDone,
		Intro:                 c.Intro,
		ShellAmbient:          c.ShellAmbient,
		Backdrop:              c.Backdrop,
		GlassBlurPtr:          &blur,
		GlassVeilPtr:          &veil,
		GlassRimPtr:           &rim,
		ShellLogo:             logo,
		ShellMatrixPtr:        &sm,
		ShellMatrixOpacityPtr: &op,
		AnimateUnfocusedPtr:   &au,
		NoticePosition:        c.NoticePosition,
		Window:                c.Window,
	}
}
