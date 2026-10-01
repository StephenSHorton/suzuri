package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeDefaults(t *testing.T) {
	c := Normalize(Config{})
	if c.FontFace == "" || c.FontSizePx < 10 || c.Theme == "" {
		t.Fatalf("normalize empty: %+v", c)
	}
}

func TestBackdropNormalizeAndRoundTrip(t *testing.T) {
	c := Normalize(Config{})
	if c.Backdrop != BackdropSolid {
		t.Fatalf("default backdrop %q", c.Backdrop)
	}
	c = Normalize(Config{Backdrop: "GLASS"})
	if c.Backdrop != BackdropGlass {
		t.Fatalf("glass normalize %q", c.Backdrop)
	}
	c = Normalize(Config{Backdrop: "nope"})
	if c.Backdrop != BackdropSolid {
		t.Fatalf("unknown backdrop %q", c.Backdrop)
	}
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	want := Default()
	want.Backdrop = BackdropGlass
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Backdrop != BackdropGlass {
		t.Fatalf("round trip %q", got.Backdrop)
	}
}

func TestShellMatrixOpacityClamp(t *testing.T) {
	c := Normalize(Config{ShellMatrixOpacity: 150})
	if c.ShellMatrixOpacity != 100 {
		t.Fatalf("clamp high: %d", c.ShellMatrixOpacity)
	}
	c = Normalize(Config{ShellMatrixOpacity: -10})
	if c.ShellMatrixOpacity != 0 {
		t.Fatalf("clamp low: %d", c.ShellMatrixOpacity)
	}
	if Default().ShellMatrixOpacity != 100 {
		t.Fatalf("default opacity %d", Default().ShellMatrixOpacity)
	}
	if Default().ShellMatrixOpacity01() != 1 {
		t.Fatalf("default 01=%v", Default().ShellMatrixOpacity01())
	}
}

func TestShellMatrixOpacityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	want := Default()
	want.ShellMatrixOpacity = 45
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellMatrixOpacity != 45 {
		t.Fatalf("got opacity %d", got.ShellMatrixOpacity)
	}
	// Missing key → default 100
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"font_face":"x","font_size_px":14,"cursor":"block","theme":"inkstone","shell_ansi_map":"soft"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellMatrixOpacity != 100 {
		t.Fatalf("missing opacity should default 100, got %d", got.ShellMatrixOpacity)
	}
}

func TestGlassKnobsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	want := Default()
	want.GlassBlur = 16
	want.GlassVeil = 40
	want.GlassRim = 0
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.GlassBlur != 16 || got.GlassVeil != 40 || got.GlassRim != 0 {
		t.Fatalf("round trip blur %d veil %d rim %d", got.GlassBlur, got.GlassVeil, got.GlassRim)
	}
	if Default().GlassBlur != GlassBlurDefault || Default().GlassRim != GlassRimDefault || Default().GlassVeil != 0 {
		t.Fatalf("defaults %+v", Default())
	}
	c := Normalize(Config{GlassBlur: 200, GlassVeil: -3, GlassRim: 140})
	if c.GlassBlur != GlassBlurMax || c.GlassVeil != 0 || c.GlassRim != 100 {
		t.Fatalf("clamp blur %d veil %d rim %d", c.GlassBlur, c.GlassVeil, c.GlassRim)
	}
	// Missing keys keep the original fixed look.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"font_face":"x","font_size_px":14}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.GlassBlur != GlassBlurDefault || got.GlassVeil != 0 || got.GlassRim != GlassRimDefault {
		t.Fatalf("missing keys blur %d veil %d rim %d", got.GlassBlur, got.GlassVeil, got.GlassRim)
	}
	if got.GlassVeilAlpha() != 0 {
		t.Fatalf("veil alpha %d", got.GlassVeilAlpha())
	}
	// 25% of 255 rounds to 64.
	if Default().GlassRimAlpha() != 64 {
		t.Fatalf("rim alpha %d", Default().GlassRimAlpha())
	}
}

func TestShellLogoOpacity(t *testing.T) {
	if Default().ShellLogo != ShellLogoDefault {
		t.Fatalf("default logo %d", Default().ShellLogo)
	}
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"font_face":"x","font_size_px":14}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellLogo != ShellLogoDefault {
		t.Fatalf("missing logo %d", got.ShellLogo)
	}
	if err := os.WriteFile(path, []byte(`{"shell_logo":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellLogo != ShellLogoDefault {
		t.Fatalf("true logo %d", got.ShellLogo)
	}
	if err := os.WriteFile(path, []byte(`{"shell_logo":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellLogo != 0 {
		t.Fatalf("false logo %d", got.ShellLogo)
	}
	got.ShellLogo = 35
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	got, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ShellLogo != 35 {
		t.Fatalf("saved logo %d", got.ShellLogo)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// Point LOCALAPPDATA at temp so Path() is isolated.
	t.Setenv("LOCALAPPDATA", dir)
	want := Config{
		FontFace:     "Consolas",
		FontSizePx:   18,
		Cursor:       CursorBar,
		Theme:        ThemeCharmtone,
		ShellANSIMap: ANSIMapFull,
		Window: WindowPlacement{
			X: 120, Y: 80, Width: 1000, Height: 700, Maximized: false,
		},
	}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	path := Path()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// Ensure we wrote under temp suzuri dir.
	if filepath.Dir(path) != filepath.Join(dir, "suzuri") {
		t.Fatalf("path=%s", path)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.FontFace != want.FontFace || got.FontSizePx != want.FontSizePx ||
		got.Cursor != want.Cursor || got.Theme != want.Theme ||
		got.ShellANSIMap != want.ShellANSIMap ||
		got.Window != want.Window {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestWindowPlacementValid(t *testing.T) {
	if (WindowPlacement{}).Valid() {
		t.Fatal("zero placement should be invalid")
	}
	if !(WindowPlacement{X: 10, Y: 10, Width: 800, Height: 600}).Valid() {
		t.Fatal("normal placement should be valid")
	}
	if (WindowPlacement{Width: 100, Height: 100}).Valid() {
		t.Fatal("tiny placement should be invalid")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	if c.FontFace != d.FontFace || c.Theme != d.Theme {
		t.Fatalf("got %+v", c)
	}
}

func TestNoticePositionRoundTrip(t *testing.T) {
	if got := Normalize(Config{}).NoticePosition; got != NoticeBottomLeft {
		t.Fatalf("default %q", got)
	}
	if got := Normalize(Config{NoticePosition: "nope"}).NoticePosition; got != NoticeBottomLeft {
		t.Fatalf("unknown %q", got)
	}
	ids := NoticePositionIDs()
	if len(ids) != 9 {
		t.Fatalf("ids %v", ids)
	}
	// Native placement switches on this index order.
	want := []string{
		"bottom-left", "bottom-center", "bottom-right",
		"center-left", "center", "center-right",
		"top-left", "top-center", "top-right",
	}
	for i, id := range want {
		if ids[i] != id || NoticePositionIndex(id) != i || NoticePositionLabel(id) == "" || !ValidNoticePosition(id) {
			t.Fatalf("id %d %q idx %d", i, ids[i], NoticePositionIndex(id))
		}
	}
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	wantCfg := Default()
	wantCfg.NoticePosition = NoticeCenter
	if err := Save(wantCfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.NoticePosition != NoticeCenter {
		t.Fatalf("loaded %q", got.NoticePosition)
	}
}

func TestAmbientAndIntroIDs(t *testing.T) {
	if len(IntroIDs()) < 5 {
		t.Fatalf("intro ids: %v", IntroIDs())
	}
	if len(AmbientIDs()) < 5 {
		t.Fatalf("ambient ids: %v", AmbientIDs())
	}
	for _, id := range IntroIDs() {
		if !ValidIntro(id) || IntroLabel(id) == "" || IntroDesc(id) == "" {
			t.Fatalf("intro %q", id)
		}
	}
	for _, id := range AmbientIDs() {
		if !ValidAmbient(id) || AmbientLabel(id) == "" || AmbientDesc(id) == "" {
			t.Fatalf("ambient %q", id)
		}
	}
	// Legacy shell_matrix=true → rain ambient
	c := Normalize(Config{ShellMatrix: true})
	if c.ShellAmbient != AmbientRain {
		t.Fatalf("legacy matrix true → ambient %q", c.ShellAmbient)
	}
	c = Normalize(Config{ShellMatrix: false, ShellAmbient: ""})
	// empty ambient + false matrix
	if c.ShellAmbient != AmbientNone {
		t.Fatalf("legacy matrix false → ambient %q", c.ShellAmbient)
	}
	// Explicit ambient wins
	c = Normalize(Config{ShellAmbient: AmbientWaves, ShellMatrix: true})
	if c.ShellAmbient != AmbientWaves {
		t.Fatalf("explicit ambient lost: %q", c.ShellAmbient)
	}
	if c.ShellMatrix {
		t.Fatal("ShellMatrix should be false when ambient is waves")
	}
}

func TestThemeIDsUniqueAndValid(t *testing.T) {
	ids := ThemeIDs()
	if len(ids) < 10 {
		t.Fatalf("expected a bunch of themes, got %d", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty theme id")
		}
		if seen[id] {
			t.Fatalf("duplicate theme %q", id)
		}
		seen[id] = true
		if !ValidTheme(id) {
			t.Fatalf("ThemeIDs entry not ValidTheme: %q", id)
		}
		if ThemeLabel(id) == "" {
			t.Fatalf("empty label for %q", id)
		}
		if ThemeDesc(id) == "" {
			t.Fatalf("empty desc for %q", id)
		}
	}
	// Unknown → Normalize falls back to high contrast
	c := Normalize(Config{Theme: "not_a_theme"})
	if c.Theme != ThemeHighContrast {
		t.Fatalf("unknown theme: %q", c.Theme)
	}
	// Known themes survive Normalize
	for _, id := range ids {
		c := Normalize(Config{Theme: id})
		if c.Theme != id {
			t.Fatalf("Normalize dropped %q → %q", id, c.Theme)
		}
	}
}
