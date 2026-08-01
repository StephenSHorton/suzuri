// Package config holds suzuri product settings (font, cursor, theme) with
// JSON persistence under %LOCALAPPDATA%\suzuri\config.json.
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

// Shell ANSI map modes: remap classic 16-color SGR onto theme-friendly hues.
const (
	ANSIMapNone = "none" // stock VT colors
	ANSIMapSoft = "soft" // blend theme accents (default)
	ANSIMapFull = "full" // strong on-brand remap (Crush-style)
)

// Config holds product settings.
type Config struct {
	Cursor       CursorStyle
	FontFace     string
	FontSizePx   int
	Theme        string
	ShellANSIMap string
}

// fileDTO is the on-disk JSON shape (stable snake_case keys).
type fileDTO struct {
	FontFace     string `json:"font_face"`
	FontSizePx   int    `json:"font_size_px"`
	Cursor       string `json:"cursor"`
	Theme        string `json:"theme"`
	ShellANSIMap string `json:"shell_ansi_map"`
}

// Default returns shipping defaults.
func Default() Config {
	return Config{
		Cursor:       CursorBlock,
		FontFace:     "Cascadia Mono",
		FontSizePx:   16,
		Theme:        ThemeInkstone,
		ShellANSIMap: ANSIMapSoft,
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
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
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
		c.Theme = ThemeInkstone
	}
	switch strings.ToLower(strings.TrimSpace(c.ShellANSIMap)) {
	case ANSIMapNone, ANSIMapSoft, ANSIMapFull:
		c.ShellANSIMap = strings.ToLower(strings.TrimSpace(c.ShellANSIMap))
	case "":
		c.ShellANSIMap = d.ShellANSIMap
	default:
		c.ShellANSIMap = ANSIMapSoft
	}
	return c
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

// MonoFontFaces are preferred faces for the settings cycle (host may fall back).
func MonoFontFaces() []string {
	return []string{
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
		FontFace:     d.FontFace,
		FontSizePx:   d.FontSizePx,
		Cursor:       ParseCursor(d.Cursor),
		Theme:        d.Theme,
		ShellANSIMap: d.ShellANSIMap,
	}
}

func toDTO(c Config) fileDTO {
	return fileDTO{
		FontFace:     c.FontFace,
		FontSizePx:   c.FontSizePx,
		Cursor:       CursorString(c.Cursor),
		Theme:        c.Theme,
		ShellANSIMap: c.ShellANSIMap,
	}
}
