// Package config holds suzuri product settings with JSON persistence under
// %LOCALAPPDATA%\suzuri\config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
)

// Shell ANSI map modes.
const (
	ANSIMapNone = "none"
	ANSIMapSoft = "soft"
	ANSIMapFull = "full"
)

// Startup intro styles (shell curtain after launch).
const (
	IntroMatrix = "matrix" // digital rain
	IntroRipple = "ripple" // 猫咪 puddle from center mark
	IntroNone   = "none"   // skip curtain
)

// Profile is a named shell launch recipe (cwd + command + optional theme).
type Profile struct {
	Name  string `json:"name"`
	Shell string `json:"shell,omitempty"` // empty → DefaultShell
	Cwd   string `json:"cwd,omitempty"`   // empty → process cwd
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
	// Intro is the post-launch shell curtain (matrix | ripple | none).
	Intro string
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
	FontFace      string           `json:"font_face"`
	FontSizePx    int              `json:"font_size_px"`
	Cursor        string           `json:"cursor"`
	Theme         string           `json:"theme"`
	ShellANSIMap  string           `json:"shell_ansi_map"`
	Profiles      []Profile        `json:"profiles,omitempty"`
	ActiveProfile string           `json:"active_profile,omitempty"`
	FirstRunDone  bool            `json:"first_run_done,omitempty"`
	Intro         string          `json:"intro,omitempty"`
	Window        WindowPlacement `json:"window,omitempty"`
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
		Cursor:        CursorBlock,
		FontFace:      DefaultFontFace,
		FontSizePx:    DefaultFontSizePx,
		Theme:         ThemeHighContrast,
		ShellANSIMap:  ANSIMapSoft,
		Intro:         IntroMatrix,
		Profiles:      DefaultProfiles(),
		ActiveProfile: "Default",
		FirstRunDone:  false,
	}
}

// DefaultProfiles are built-in launch recipes.
func DefaultProfiles() []Profile {
	return []Profile{
		{Name: "Default", Shell: "", Cwd: ""},
		{Name: "PowerShell", Shell: `powershell.exe -NoLogo -NoProfile`, Cwd: ""},
		{Name: "Cmd", Shell: `cmd.exe`, Cwd: ""},
	}
}

// Path is %LOCALAPPDATA%\suzuri\config.json (or temp fallback).
func Path() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "suzuri", "config.json")
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
	switch strings.ToLower(strings.TrimSpace(c.Theme)) {
	case ThemeInkstone, ThemeCharmtone, ThemeHighContrast:
		c.Theme = strings.ToLower(strings.TrimSpace(c.Theme))
	default:
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
	switch strings.ToLower(strings.TrimSpace(c.Intro)) {
	case IntroMatrix, IntroRipple, IntroNone:
		c.Intro = strings.ToLower(strings.TrimSpace(c.Intro))
	case "":
		c.Intro = d.Intro
	default:
		c.Intro = IntroMatrix
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
	return c
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

// ThemeIDs lists selectable themes.
func ThemeIDs() []string {
	return []string{ThemeInkstone, ThemeCharmtone, ThemeHighContrast}
}

// ThemeLabel is a human title for a theme id.
func ThemeLabel(id string) string {
	switch id {
	case ThemeCharmtone:
		return "Charmtone"
	case ThemeHighContrast:
		return "High contrast"
	default:
		return "Inkstone"
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
	return []string{IntroMatrix, IntroRipple, IntroNone}
}

// IntroLabel is a human title for a startup intro id.
func IntroLabel(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case IntroRipple:
		return "Ripple"
	case IntroNone:
		return "None"
	default:
		return "Matrix"
	}
}

// MonoFontFaces are preferred faces for the settings cycle.
// DefaultFontFace is first (bundled into the binary when available).
func MonoFontFaces() []string {
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
	return Config{
		FontFace:      d.FontFace,
		FontSizePx:    d.FontSizePx,
		Cursor:        ParseCursor(d.Cursor),
		Theme:         d.Theme,
		ShellANSIMap:  d.ShellANSIMap,
		Profiles:      d.Profiles,
		ActiveProfile: d.ActiveProfile,
		FirstRunDone:  d.FirstRunDone,
		Intro:         d.Intro,
		Window:        d.Window,
	}
}

func toDTO(c Config) fileDTO {
	return fileDTO{
		FontFace:      c.FontFace,
		FontSizePx:    c.FontSizePx,
		Cursor:        CursorString(c.Cursor),
		Theme:         c.Theme,
		ShellANSIMap:  c.ShellANSIMap,
		Profiles:      c.Profiles,
		ActiveProfile: c.ActiveProfile,
		FirstRunDone:  c.FirstRunDone,
		Intro:         c.Intro,
		Window:        c.Window,
	}
}
